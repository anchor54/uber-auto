-- The first migration (R1 + D2): accounts, drivers, series and their slots,
-- rides with the driver-overlap constraint, offers and their recipients,
-- events, the notification outbox, devices, sweeper runs and the config
-- tables. Times are UTC timestamptz; ids are UUIDs.

create extension if not exists btree_gist;

create table users (
  id uuid primary key default gen_random_uuid(),
  phone text not null unique,
  role text not null check (role in ('rider', 'driver')),
  created_at timestamptz not null default now()
);

create table riders (
  user_id uuid primary key references users (id),
  name text not null default '',
  rating numeric(2, 1) not null default 5.0,
  usual_payment text not null default 'upi' check (usual_payment in ('upi', 'cash')),
  created_at timestamptz not null default now()
);

-- Eligible for offers when status = 'live' and subscription_until >= today.
create table drivers (
  user_id uuid primary key references users (id),
  name text not null,
  city text not null default 'blr',
  vehicle_type text not null,
  vehicle_number text not null,
  vehicle_desc text not null default '',
  status text not null default 'pending' check (status in ('pending', 'live', 'suspended', 'offline')),
  subscription_until date,
  day_off int check (day_off between 1 and 7),
  rating numeric(2, 1) not null default 5.0,
  created_at timestamptz not null default now()
);
create index drivers_eligible_idx on drivers (vehicle_type, city) where status = 'live';

-- Sign-in codes, hashed; one live code per phone and role.
create table otp_codes (
  phone text not null,
  role text not null check (role in ('rider', 'driver')),
  code_hash text not null,
  expires_at timestamptz not null,
  attempts int not null default 0,
  sends int not null default 1,
  window_started_at timestamptz not null,
  last_sent_at timestamptz not null,
  created_at timestamptz not null default now(),
  primary key (phone, role)
);

create table sessions (
  token_hash text primary key,
  user_id uuid not null references users (id),
  role text not null check (role in ('rider', 'driver')),
  expires_at timestamptz not null,
  created_at timestamptz not null default now()
);

-- One row per installed app.
create table devices (
  fcm_token text primary key,
  user_id uuid not null references users (id),
  platform text not null default 'android',
  updated_at timestamptz not null default now(),
  created_at timestamptz not null default now()
);
create index devices_user_idx on devices (user_id);

-- Settings, carried over from the built app's config.
create table app_config (
  key text primary key,
  value jsonb not null,
  created_at timestamptz not null default now()
);

-- Ride types: label, seats, speed, and which flows and cities allow them.
create table ride_type_config (
  ride_type text primary key,
  label text not null,
  description text not null default '',
  seats int not null,
  speed_kmh numeric not null check (speed_kmh > 0),
  flows text[] not null default '{}',
  cities text[] not null default '{}',
  sort int not null default 0,
  created_at timestamptz not null default now()
);

create table fare_rules (
  city text not null,
  ride_type text not null references ride_type_config (ride_type),
  base numeric not null,
  per_km numeric not null,
  minimum numeric not null,
  created_at timestamptz not null default now(),
  primary key (city, ride_type)
);

-- A rider's regular ride. version goes up on every change.
create table series (
  id uuid primary key default gen_random_uuid(),
  rider_id uuid not null references users (id),
  status text not null check (status in ('finding_driver', 'active', 'paused', 'no_driver_found', 'ended')),
  city text not null,
  vehicle_type text not null,
  preferences text[] not null default '{}',
  pickup_lat double precision not null,
  pickup_lng double precision not null,
  pickup_label text not null default '',
  drop_lat double precision not null,
  drop_lng double precision not null,
  drop_label text not null default '',
  time_zone text not null default 'Asia/Kolkata',
  start_date date not null,
  distance_km numeric(6, 1) not null,
  travel_minutes int not null,
  suggested_fare int not null,
  usual_payment text not null default 'upi' check (usual_payment in ('upi', 'cash')),
  requested_at timestamptz not null,
  -- Goes up on every "Try again": rounds of an earlier attempt don't
  -- exclude drivers (clashed or withdrew) from a later one.
  attempt int not null default 1,
  version int not null default 1,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index series_rider_idx on series (rider_id);

-- One row per weekday and direction, each with its own driver. For a return
-- slot, pickup and drop-off are swapped.
create table series_slots (
  id uuid primary key default gen_random_uuid(),
  series_id uuid not null references series (id) on delete cascade,
  weekday int not null check (weekday between 1 and 7),
  direction text not null check (direction in ('outbound', 'return')),
  timing text not null default 'pickup_at' check (timing in ('pickup_at', 'arrive_by')),
  arrive_by_time time,
  local_pickup_time time not null,
  driver_id uuid references drivers (user_id),
  active boolean not null default true,
  created_at timestamptz not null default now(),
  unique (series_id, weekday, direction)
);
create index series_slots_driver_idx on series_slots (driver_id) where driver_id is not null;

-- One ride per slot and date.
create table rides (
  id uuid primary key default gen_random_uuid(),
  series_id uuid references series (id),
  slot_id uuid references series_slots (id),
  rider_id uuid not null references users (id),
  driver_id uuid references drivers (user_id),
  local_date date not null,
  pickup_at timestamptz not null,
  drop_at timestamptz not null,
  busy_until timestamptz not null,
  status text not null check (status in ('assigned', 'needs_driver', 'on_the_way', 'completed',
    'rider_no_show', 'skipped', 'failed', 'cancelled')),
  rider_changed boolean not null default false,
  reminder_sent_at timestamptz,
  fare_snapshot int not null,
  version int not null default 1,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (slot_id, local_date),
  check (busy_until > pickup_at),
  check (status not in ('assigned', 'on_the_way') or driver_id is not null)
);
create index rides_driver_idx on rides (driver_id, pickup_at);

-- Whatever the code does, one driver can't hold two overlapping rides.
alter table rides add constraint no_driver_overlap
  exclude using gist (driver_id with =, tstzrange(pickup_at, busy_until) with &&)
  where (status in ('assigned', 'on_the_way'));

-- A round of offering a series, a slot or one ride to drivers.
create table offers (
  id uuid primary key default gen_random_uuid(),
  kind text not null check (kind in ('series', 'slot', 'ride')),
  series_id uuid references series (id),
  slot_id uuid references series_slots (id),
  ride_id uuid references rides (id),
  attempt int not null default 1,
  round int not null check (round between 1 and 3),
  status text not null check (status in ('open', 'closing', 'closed')),
  opened_at timestamptz not null,
  expires_at timestamptz not null,
  closing_at timestamptz,
  closed_at timestamptz,
  outcome text check (outcome in ('assigned', 'no_fit', 'cancelled')),
  created_at timestamptz not null default now(),
  check ((kind = 'series' and series_id is not null and slot_id is null and ride_id is null)
      or (kind = 'slot' and slot_id is not null and ride_id is null)
      or (kind = 'ride' and ride_id is not null))
);
-- At most one open or closing offer per target.
create unique index offers_one_live_per_target on offers (kind, coalesce(ride_id, slot_id, series_id))
  where status in ('open', 'closing');
create index offers_due_idx on offers (status, expires_at);
create index offers_series_idx on offers (series_id);

create table offer_recipients (
  offer_id uuid not null references offers (id) on delete cascade,
  driver_id uuid not null references drivers (user_id),
  notified_at timestamptz,
  interested_at timestamptz,
  withdrawn_at timestamptz,
  -- "Not for me": later rounds of the same request skip the driver.
  dismissed_at timestamptz,
  result text check (result in ('picked', 'not_picked', 'clashed', 'withdrew', 'ineligible')),
  created_at timestamptz not null default now(),
  primary key (offer_id, driver_id)
);
create index offer_recipients_driver_idx on offer_recipients (driver_id);

-- Append only: every change, who made it, old and new status.
create table events (
  id uuid primary key default gen_random_uuid(),
  seq bigserial not null,
  at timestamptz not null,
  actor_id uuid,
  entity text not null,
  entity_id uuid not null,
  type text not null,
  data jsonb not null default '{}',
  created_at timestamptz not null default now()
);
create index events_entity_idx on events (entity, entity_id, seq);

-- Pushes are written here in the same transaction as the change; the
-- sweep's send step delivers rows with sent_at null and send_after <= now().
create table notification_outbox (
  id uuid primary key default gen_random_uuid(),
  seq bigserial not null,
  user_id uuid not null references users (id),
  kind text not null,
  payload jsonb not null,
  send_after timestamptz not null,
  sent_at timestamptz,
  attempts int not null default 0,
  last_error text,
  created_at timestamptz not null default now()
);
create index notification_outbox_due_idx on notification_outbox (send_after, seq) where sent_at is null;

create table sweeper_runs (
  id uuid primary key default gen_random_uuid(),
  started_at timestamptz not null,
  finished_at timestamptz,
  counts jsonb not null default '{}',
  error text,
  created_at timestamptz not null default now()
);
