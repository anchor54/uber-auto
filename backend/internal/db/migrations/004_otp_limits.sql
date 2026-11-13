-- Limits on sign-in codes before real SMS goes out: a cooldown between
-- sends, a cap on sends per hour, and wrong-code attempts that count across
-- resends (a new code no longer means five fresh guesses).
alter table otp_codes add column sends int not null default 1;
alter table otp_codes add column window_started_at timestamptz not null default now();
alter table otp_codes add column last_sent_at timestamptz not null default now();

-- Codes were plain SHA-256; they are now HMAC-SHA256 with a server secret.
-- Pending codes from before can't be checked any more: clear them (they
-- expire within 5 minutes anyway).
delete from otp_codes;
