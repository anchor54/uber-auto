-- D1: driver registration, verification and subscription. Driver statuses,
-- the documents (driver_items), the vehicle, the weekly day off, the plans
-- and the subscription payments; admin accounts.

-- Admins review drivers and payments (the POC's admin tool).
alter table users drop constraint users_role_check;
alter table users add constraint users_role_check check (role in ('rider', 'driver', 'admin'));
alter table sessions drop constraint sessions_role_check;
alter table sessions add constraint sessions_role_check check (role in ('rider', 'driver', 'admin'));

create table admins (
  user_id uuid primary key references users (id),
  name text not null,
  created_at timestamptz not null default now()
);

-- Driver status: signing_up → submitted → changes_needed | approved | rejected;
-- approved → live (first payment confirmed); live ⇄ paused (D5).
alter table drivers drop constraint drivers_status_check;
update drivers set status = case status when 'pending' then 'signing_up' when 'suspended' then 'paused'
  when 'offline' then 'paused' else status end;
alter table drivers add constraint drivers_status_check check (status in
  ('signing_up', 'submitted', 'changes_needed', 'approved', 'live', 'paused', 'rejected'));
alter table drivers alter column status set default 'signing_up';
-- Copied from the vehicle (type, or cab_<class>; plate; colour and model)
-- when it is set; empty until then.
alter table drivers alter column vehicle_type set default '';
alter table drivers alter column vehicle_number set default '';
alter table drivers
  add column reject_reason text,
  add column reference text,
  add column upi_id text,
  -- day_off null means "No fixed day off" once day_off_set is true.
  add column day_off_set boolean not null default false,
  add column submitted_at timestamptz,
  add column approved_at timestamptz,
  add column version int not null default 1,
  add column updated_at timestamptz not null default now();
update drivers d set reference = 'DRV-' || (10000 + n)::text, day_off_set = true
  from (select user_id, row_number() over (order by created_at, user_id) as n from drivers) x
  where x.user_id = d.user_id;
create unique index drivers_reference_key on drivers (reference);
create index drivers_status_idx on drivers (status, submitted_at);

-- One vehicle per driver. The plate is stored upper-cased; uniqueness
-- ignores spaces and dashes.
create table vehicles (
  driver_id uuid primary key references drivers (user_id),
  type text not null check (type in ('bike', 'auto', 'cab')),
  cab_class text check (cab_class in ('hatchback', 'sedan', 'suv')),
  make_model text not null default '',
  year int,
  plate text not null,
  colour text not null default '',
  ac boolean not null default false,
  cng_or_electric boolean not null default false,
  review_state text not null default 'uploaded' check (review_state in ('uploaded', 'verified', 'rejected')),
  reject_reason text,
  reviewed_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  check ((type = 'cab') = (cab_class is not null))
);
create unique index vehicles_plate_key on vehicles ((regexp_replace(plate, '[^A-Z0-9]', '', 'g')));

insert into vehicles (driver_id, type, cab_class, plate, review_state)
  select user_id,
         case when vehicle_type like 'cab_%' then 'cab' else vehicle_type end,
         case when vehicle_type like 'cab_%' then substr(vehicle_type, 5) end,
         upper(vehicle_number), 'verified'
  from drivers where vehicle_number <> '' and vehicle_type in ('bike', 'auto', 'cab_hatchback', 'cab_sedan', 'cab_suv');

-- The documents and vehicle photos (files in storage), and the UPI ID (a
-- text on drivers; its row holds the review state, with no file).
create table driver_items (
  driver_id uuid not null references drivers (user_id),
  kind text not null check (kind in ('profile_photo', 'id_proof', 'licence', 'rc', 'insurance', 'permit',
    'upi', 'vehicle_front', 'vehicle_back', 'vehicle_interior')),
  storage_path text,
  state text not null check (state in ('uploaded', 'verified', 'rejected')),
  reject_reason text,
  uploaded_at timestamptz,
  reviewed_at timestamptz,
  reviewed_by uuid,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (driver_id, kind),
  check (storage_path is null or storage_path like 'driver-docs/' || driver_id::text || '/%')
);

-- One plan per vehicle type (and cab class). Rows come from config.
create table plans (
  id uuid primary key default gen_random_uuid(),
  vehicle_type text not null check (vehicle_type in ('bike', 'auto', 'cab')),
  -- null for bikes and autos.
  cab_class text check (cab_class in ('hatchback', 'sedan', 'suv')),
  label text not null,
  monthly_price int not null check (monthly_price > 0),
  created_at timestamptz not null default now(),
  check ((vehicle_type = 'cab') = (cab_class is not null))
);
create unique index plans_type_key on plans (vehicle_type, coalesce(cab_class, ''));

create table subscription_payments (
  id uuid primary key default gen_random_uuid(),
  driver_id uuid not null references drivers (user_id),
  period_start date not null,
  period_end date not null,
  amount int not null,
  reference text not null,
  status text not null check (status in ('claimed', 'confirmed', 'rejected')),
  claimed_at timestamptz not null,
  reviewed_at timestamptz,
  reviewed_by uuid,
  reject_reason text,
  created_at timestamptz not null default now(),
  check (period_end >= period_start)
);
-- At most one open or confirmed claim per period.
create unique index subscription_payments_one_per_period on subscription_payments (driver_id, period_start)
  where status in ('claimed', 'confirmed');
create index subscription_payments_status_idx on subscription_payments (status, claimed_at);
