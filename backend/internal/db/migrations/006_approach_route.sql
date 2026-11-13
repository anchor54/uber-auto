-- The driver's road route to the pickup while they're on the way, from the
-- maps provider: asked for when the driver departs, and again only if they
-- leave it. The rider's map shows it, and the ETA uses its drive time.
alter table rides add column approach_route text;
alter table rides add column approach_minutes int;
-- Straight-line km from where the route was asked for to the pickup, to
-- scale approach_minutes as the driver gets closer.
alter table rides add column approach_km numeric;
alter table rides add column approach_at timestamptz;
