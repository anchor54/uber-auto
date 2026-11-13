-- Flow D2: drivers sign in and accept or decline commutes.

-- Drivers sign in with their phone, like riders.
alter table drivers add column phone text unique;
alter table drivers add column on_duty boolean not null default false;

-- Sessions belong to a rider or a driver.
alter table sessions add column role text not null default 'rider' check (role in ('rider', 'driver'));
alter table sessions add column driver_id text references drivers(id) on delete cascade;
alter table sessions alter column rider_id drop not null;
alter table sessions add constraint sessions_one_owner check (
  (role = 'rider' and rider_id is not null and driver_id is null) or
  (role = 'driver' and driver_id is not null and rider_id is null)
);

-- One pending code per phone per role: a driver can also ride.
alter table otp_codes add column role text not null default 'rider' check (role in ('rider', 'driver'));
alter table otp_codes drop constraint otp_codes_pkey;
alter table otp_codes add primary key (phone, role);

-- A confirmed commute waits for the driver until respond_by. A decline, or
-- no answer in time, moves it to 'declined' and frees the driver's slot.
alter table commutes drop constraint commutes_status_check;
alter table commutes add constraint commutes_status_check
  check (status in ('awaiting_driver', 'active', 'paused', 'cancelled', 'declined'));
alter table commutes add column respond_by timestamptz;
alter table commutes add column responded_at timestamptz;
alter table commutes add column decline_reason text check (decline_reason in ('declined', 'no_answer'));
create index commutes_awaiting_idx on commutes (respond_by) where status = 'awaiting_driver';
