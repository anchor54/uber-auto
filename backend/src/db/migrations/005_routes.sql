-- The road route from the maps provider, as an encoded polyline (precision 5),
-- so the app can draw it. Null when the maps provider gives none (the demo
-- city). The way back is fetched separately: it can take other roads.
alter table commutes add column route_out text;
alter table commutes add column route_back text;
alter table rides add column route text;
