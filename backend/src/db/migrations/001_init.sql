-- Rider accounts, sign-in, places, fares, ride-type config, drivers,
-- commutes and match proposals. Payments are off-platform in v1, so there
-- are no payment tables: riders pay drivers directly.

create table riders (
  id uuid primary key default gen_random_uuid(),
  phone text not null unique,
  name text,
  usual_payment text not null default 'upi' check (usual_payment in ('upi', 'cash')),
  created_at timestamptz not null default now()
);

create table otp_codes (
  phone text primary key,
  code_hash text not null,
  expires_at timestamptz not null,
  attempts int not null default 0
);

create table sessions (
  token_hash text primary key,
  rider_id uuid not null references riders(id) on delete cascade,
  created_at timestamptz not null default now(),
  expires_at timestamptz not null
);

create table places (
  id text primary key,
  city text not null,
  title text not null,
  detail text not null default '',
  lat double precision not null,
  lng double precision not null
);

create table fare_rules (
  city text not null,
  ride_type text not null,
  base int not null,
  per_km int not null,
  minimum int not null,
  primary key (city, ride_type)
);

-- The backend config that decides which ride types each booking flow
-- offers in each city.
create table ride_type_config (
  city text not null,
  flow text not null check (flow in ('one_time', 'recurring_weekday', 'recurring_weekly')),
  allowed text[] not null,
  default_type text,
  primary key (city, flow)
);

create table drivers (
  id text primary key,
  city text not null,
  name text not null,
  ride_type text not null,
  vehicle text not null,
  plate text not null,
  rating numeric(2, 1) not null,
  upi_id text not null,
  gender text not null default 'unspecified',
  has_ac boolean not null default false,
  base_lat double precision not null,
  base_lng double precision not null,
  service_radius_km numeric not null,
  work_start int not null,  -- minutes after midnight
  work_end int not null,
  active boolean not null default true
);

create table commutes (
  id uuid primary key default gen_random_uuid(),
  rider_id uuid not null references riders(id),
  driver_id text not null references drivers(id),
  city text not null,
  pickup jsonb not null,
  drop_off jsonb not null,
  repeat text not null check (repeat in ('every_weekday', 'chosen_days')),
  weekdays int[] not null,
  timing text not null check (timing in ('pickup_at', 'arrive_by')),
  time_min int not null,
  pickup_time int not null,
  return_trip boolean not null,
  return_time int not null,
  start_date date not null,
  ride_type text not null,
  preferences text[] not null default '{}',
  fare int not null,
  trip_minutes int not null,
  distance_km numeric not null,
  usual_payment text not null check (usual_payment in ('upi', 'cash')),
  status text not null check (status in ('awaiting_driver', 'active', 'paused', 'cancelled')),
  created_at timestamptz not null default now()
);
create index commutes_driver_idx on commutes (driver_id) where status in ('awaiting_driver', 'active');
create index commutes_rider_idx on commutes (rider_id);

-- A proposed driver is held for the rider for 15 minutes while they confirm.
create table match_proposals (
  id uuid primary key default gen_random_uuid(),
  rider_id uuid not null references riders(id),
  driver_id text not null references drivers(id),
  request jsonb not null,
  fare int not null,
  trip_minutes int not null,
  distance_km numeric not null,
  rank int not null,
  expires_at timestamptz not null,
  created_at timestamptz not null default now()
);

create table waitlist (
  id uuid primary key default gen_random_uuid(),
  rider_id uuid not null references riders(id),
  request jsonb not null,
  created_at timestamptz not null default now()
);
