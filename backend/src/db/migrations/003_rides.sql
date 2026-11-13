-- Flow R2: one ride record per commute leg, its lifecycle, live location,
-- payment marks, ratings and push notifications.

create table rides (
  id uuid primary key default gen_random_uuid(),
  commute_id uuid not null references commutes(id),
  rider_id uuid not null references riders(id),
  driver_id text not null references drivers(id),
  ride_date date not null,                       -- local (IST) calendar date
  direction text not null check (direction in ('outbound', 'return')),
  pickup jsonb not null,
  drop_off jsonb not null,
  pickup_at timestamptz not null,
  window_start timestamptz not null,             -- pickup ± 5 minutes
  window_end timestamptz not null,
  trip_minutes int not null,
  fare int not null,
  ride_type text not null,
  pin text not null,                             -- shown to the rider, so kept as text
  pin_tries int not null default 0,
  state text not null default 'scheduled' check (state in
    ('scheduled', 'on_the_way', 'arrived', 'in_ride', 'completed', 'no_show', 'skipped', 'cancelled')),
  departed_at timestamptz,
  arrived_at timestamptz,
  started_at timestamptz,
  ended_at timestamptz,
  closed_at timestamptz,                         -- set when the ride left the main path (exit or stale close)
  wait_ends_at timestamptz,
  rider_late_used boolean not null default false,
  late_flagged_at timestamptz,
  end_reason text,                               -- given when ending away from the drop-off
  rider_payment text check (rider_payment in ('upi', 'cash')),
  rider_marked_at timestamptz,
  driver_payment text check (driver_payment in ('upi', 'cash', 'not_paid')),
  driver_marked_at timestamptz,
  created_at timestamptz not null default now(),
  unique (commute_id, ride_date, direction)
);
create index rides_rider_idx on rides (rider_id, pickup_at);
create index rides_driver_idx on rides (driver_id, pickup_at);
create index rides_open_idx on rides (state, pickup_at) where state in ('scheduled', 'on_the_way', 'arrived', 'in_ride');

-- Append-only audit trail of every move.
create table ride_events (
  id bigserial primary key,
  ride_id uuid not null references rides(id),
  from_state text not null,
  to_state text not null,
  actor_type text not null check (actor_type in ('rider', 'driver', 'system', 'ops')),
  actor_id text,
  at timestamptz not null,
  data jsonb not null default '{}'
);
create index ride_events_ride_idx on ride_events (ride_id, id);

-- Calls and alerts before a no-show.
create table ride_contacts (
  id bigserial primary key,
  ride_id uuid not null references rides(id),
  driver_id text not null references drivers(id),
  kind text not null check (kind in ('call', 'alert')),
  at timestamptz not null
);

-- Latest position per driver, overwritten every few seconds.
create table driver_locations (
  driver_id text primary key references drivers(id),
  lat double precision not null,
  lng double precision not null,
  heading double precision,
  speed double precision,
  at timestamptz not null,
  ride_id uuid references rides(id)
);

-- One point every 30 s while a ride is under way, for disputes. Kept 30 days.
create table ride_track (
  id bigserial primary key,
  ride_id uuid not null references rides(id),
  lat double precision not null,
  lng double precision not null,
  at timestamptz not null
);
create index ride_track_ride_idx on ride_track (ride_id, at);

create table ride_ratings (
  ride_id uuid primary key references rides(id),
  stars int not null check (stars between 1 and 5),
  tags text[] not null default '{}',
  note text,
  dont_match boolean not null default false,
  created_at timestamptz not null,
  updated_at timestamptz not null
);

-- Outbox: the worker sends these. Unique so nothing is queued twice.
create table notifications (
  id bigserial primary key,
  recipient_type text not null check (recipient_type in ('rider', 'driver')),
  recipient_id text not null,
  kind text not null,
  ride_id uuid references rides(id),
  payload jsonb not null default '{}',
  send_after timestamptz not null,
  sent_at timestamptz,
  tries int not null default 0,
  last_error text,
  created_at timestamptz not null default now(),
  unique (kind, ride_id, recipient_type, recipient_id)
);
create index notifications_due_idx on notifications (send_after) where sent_at is null;

create table devices (
  push_token text primary key,
  owner_type text not null check (owner_type in ('rider', 'driver')),
  owner_id text not null,
  platform text not null check (platform in ('android', 'ios', 'web')),
  updated_at timestamptz not null
);
create index devices_owner_idx on devices (owner_type, owner_id);

-- Rides on these dates are created already skipped.
create table holidays (
  city text not null,
  day date not null,
  name text not null,
  primary key (city, day)
);

-- When a scheduled job last ran, for once-a-night jobs.
create table job_runs (
  name text primary key,
  last_run_on date not null
);
