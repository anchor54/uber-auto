-- Flow D1: drivers register themselves, upload documents and vehicle
-- details, are reviewed by an admin, pay the first month's subscription
-- outside the app and go live.

-- ---------------------------------------------------------------- drivers

-- Existing drivers were onboarded by ops: they are live and paid up.
alter table drivers add column status text not null default 'live' check (status in
  ('signing_up', 'submitted', 'changes_needed', 'approved', 'live', 'paused', 'rejected'));
alter table drivers alter column status set default 'signing_up';
alter table drivers add column reject_reason text;
alter table drivers add column reference text unique;           -- DRV-xxxxx, quoted on payments
alter table drivers add column day_off int check (day_off between 1 and 7);  -- ISO weekday, 1 = Monday
alter table drivers add column day_off_set boolean not null default false;   -- null day_off + set = no fixed day off
alter table drivers add column subscription_until date;
alter table drivers add column version int not null default 1;  -- +1 on every change
alter table drivers add column terms_accepted_at timestamptz;
alter table drivers add column submitted_at timestamptz;        -- last submit, for the admin's queue
alter table drivers add column created_at timestamptz not null default now();
update drivers set subscription_until = '2099-12-31', day_off_set = true;
update drivers d set reference = 'DRV-' || (80000 + n)::text
  from (select id, row_number() over (order by id) as n from drivers) x where x.id = d.id;

-- Filled in as the driver adds their vehicle and UPI ID.
alter table drivers alter column ride_type set default '';
alter table drivers alter column vehicle set default '';
alter table drivers alter column plate set default '';
alter table drivers alter column upi_id set default '';

create index drivers_status_idx on drivers (status);

-- One vehicle per driver. plate is as shown; plate_key (upper case, no
-- spaces or hyphens) is unique across drivers.
create table vehicles (
  driver_id text primary key references drivers(id) on delete cascade,
  type text not null check (type in ('bike', 'auto', 'cab')),
  cab_class text check (cab_class in ('hatchback', 'sedan', 'suv')),
  make_model text not null,
  year int not null,
  plate text not null,
  plate_key text not null unique,
  colour text not null,
  ac boolean not null default false,
  cng_or_electric boolean not null default false,
  review_state text not null default 'uploaded' check (review_state in ('uploaded', 'verified', 'rejected')),
  reject_reason text,
  reviewed_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  check ((type = 'cab') = (cab_class is not null))
);

-- The documents, the UPI ID (no file: the value is drivers.upi_id) and the
-- vehicle photos. A row exists once the item has been added.
create table driver_items (
  driver_id text not null references drivers(id) on delete cascade,
  kind text not null check (kind in ('profile_photo', 'id_proof', 'licence', 'rc', 'insurance', 'permit', 'upi_id',
    'vehicle_front', 'vehicle_back', 'vehicle_interior')),
  storage_path text,
  state text not null default 'uploaded' check (state in ('uploaded', 'verified', 'rejected')),
  reject_reason text,
  reviewed_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (driver_id, kind),
  check ((kind = 'upi_id') = (storage_path is null))
);

-- One plan per ride type. Prices in rupees a month; ops edit them here.
create table plans (
  ride_type text primary key,
  vehicle_type text not null check (vehicle_type in ('bike', 'auto', 'cab')),
  cab_class text check (cab_class in ('hatchback', 'sedan', 'suv')),
  monthly_price int not null check (monthly_price > 0),
  created_at timestamptz not null default now()
);
insert into plans (ride_type, vehicle_type, cab_class, monthly_price) values
  ('bike', 'bike', null, 499),
  ('auto', 'auto', null, 799),
  ('cab_hatchback', 'cab', 'hatchback', 1499),
  ('cab_sedan', 'cab', 'sedan', 1499),
  ('cab_suv', 'cab', 'suv', 1999);

-- Paid outside the app; the driver claims, an admin confirms or rejects.
create table subscription_payments (
  id uuid primary key default gen_random_uuid(),
  driver_id text not null references drivers(id) on delete cascade,
  period_start date not null,
  period_end date not null,
  amount int not null,
  status text not null default 'claimed' check (status in ('claimed', 'confirmed', 'rejected')),
  claimed_at timestamptz not null,
  reviewed_at timestamptz,
  reviewed_by text,
  reject_reason text,
  created_at timestamptz not null default now()
);
create unique index subscription_payments_one_open on subscription_payments (driver_id, period_start)
  where status in ('claimed', 'confirmed');
create index subscription_payments_claimed_idx on subscription_payments (claimed_at) where status = 'claimed';

-- Append-only: every driver status change and every payment change.
create table driver_events (
  id bigserial primary key,
  driver_id text not null references drivers(id) on delete cascade,
  kind text not null,
  from_status text,
  to_status text,
  actor_type text not null check (actor_type in ('driver', 'admin', 'system')),
  actor_id text,
  at timestamptz not null,
  data jsonb not null default '{}',
  created_at timestamptz not null default now()
);
create index driver_events_driver_idx on driver_events (driver_id, id);

-- ---------------------------------------------------------------- admins and sessions

-- Admins sign in with a phone listed in ADMIN_PHONES; their sessions,
-- codes, devices and notifications are keyed by that phone.
alter table sessions add column phone text;
update sessions s set phone = d.phone from drivers d where s.driver_id = d.id;
delete from sessions where role = 'driver' and phone is null;
alter table sessions drop constraint sessions_role_check;
alter table sessions add constraint sessions_role_check check (role in ('rider', 'driver', 'admin'));
-- A driver session exists before the driver has registered (driver_id null).
alter table sessions drop constraint sessions_one_owner;
alter table sessions add constraint sessions_one_owner check (
  (role = 'rider' and rider_id is not null and driver_id is null) or
  (role = 'driver' and phone is not null and rider_id is null) or
  (role = 'admin' and phone is not null and rider_id is null and driver_id is null)
);
create index sessions_phone_idx on sessions (phone) where phone is not null;

alter table otp_codes drop constraint otp_codes_role_check;
alter table otp_codes add constraint otp_codes_role_check check (role in ('rider', 'driver', 'admin'));

alter table devices drop constraint devices_owner_type_check;
alter table devices add constraint devices_owner_type_check check (owner_type in ('rider', 'driver', 'admin'));

alter table notifications drop constraint notifications_recipient_type_check;
alter table notifications add constraint notifications_recipient_type_check
  check (recipient_type in ('rider', 'driver', 'admin'));
