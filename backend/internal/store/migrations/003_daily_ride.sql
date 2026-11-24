-- R2 + D3, the daily ride: the ride's own timestamps, PIN, running-late
-- notice, give-up and fail reasons, both payment marks, the rider's rating
-- and the driver's flags; the drivers' live positions.

alter table rides
  add column on_the_way_at timestamptz,
  add column arrived_at timestamptz,
  add column picked_up_at timestamptz,
  add column completed_at timestamptz,
  add column no_show_at timestamptz,
  add column failed_at timestamptz,
  add column pin text check (pin ~ '^[0-9]{4}$'),
  add column late_notice_at timestamptz,
  add column give_up_reason text,
  add column fail_reason text check (fail_reason in ('no_driver', 'missed_by_outage')),
  -- auto_closed: close_stale_rides completed it.
  add column close_reason text,
  add column driver_paid_mark text check (driver_paid_mark in ('upi', 'cash', 'not_paid')),
  add column driver_paid_at timestamptz,
  add column rider_paid_mark text check (rider_paid_mark in ('upi', 'cash')),
  add column rider_paid_at timestamptz,
  add column rider_stars int check (rider_stars between 1 and 5),
  add column rider_tags text[] not null default '{}',
  add column rider_note text,
  add column rated_at timestamptz,
  add column driver_flags text[] not null default '{}',
  add column flagged_at timestamptz,
  -- The driver who had the ride before it was handed over (gave it up, or
  -- missed the On my way check); not offered it again.
  add column previous_driver_id uuid references drivers (user_id);

-- The sweeper's due work, found from pickup_at and the marker columns.
create index rides_due_idx on rides (status, pickup_at);

-- One row per driver, overwritten on every update. A rider reads it only
-- for their own ride while it is on_the_way.
create table driver_locations (
  driver_id uuid primary key references drivers (user_id),
  ride_id uuid references rides (id),
  lat double precision not null,
  lng double precision not null,
  accuracy_m double precision,
  recorded_at timestamptz not null
);
