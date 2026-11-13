/**
 * Loads the city's setup into a live database without touching anything
 * else: fare rules, the ride-type config and the demo places. Rows that
 * already exist are left alone, so ops changes survive a re-run.
 *
 *   npm run seed:config                      # config only
 *   npm run seed:config -- --demo-drivers    # also the 15 demo drivers
 *
 * Unlike `npm run seed`, this never deletes data.
 */
import { fileURLToPath } from 'node:url';
import { defaultFareRules } from '../domain/trips.js';
import { bookingFlows, rideTypes } from '../domain/types.js';
import { migrate } from './migrate.js';
import type { Db } from './pool.js';
import { createPool } from './pool.js';
import { DEMO_CITY, demoDriverPhone, demoDrivers, demoPlaces } from './seed.js';

export async function seedConfig(db: Db, opts: { demoDrivers: boolean }): Promise<Record<string, number>> {
  await migrate(db);
  const added = { fareRules: 0, rideTypeConfig: 0, places: 0, drivers: 0 };
  for (const type of rideTypes) {
    const r = defaultFareRules[type];
    const res = await db.query(
      `insert into fare_rules (city, ride_type, base, per_km, minimum) values ($1, $2, $3, $4, $5) on conflict do nothing`,
      [DEMO_CITY, type, r.base, r.perKm, r.minimum],
    );
    added.fareRules += res.rowCount ?? 0;
  }
  for (const flow of bookingFlows) {
    const res = await db.query(
      `insert into ride_type_config (city, flow, allowed, default_type) values ($1, $2, $3, $4) on conflict do nothing`,
      [DEMO_CITY, flow, [...rideTypes], flow === 'one_time' ? 'auto' : 'cab_sedan'],
    );
    added.rideTypeConfig += res.rowCount ?? 0;
  }
  for (const p of demoPlaces) {
    const res = await db.query(
      `insert into places (id, city, title, detail, lat, lng) values ($1, $2, $3, $4, $5, $6) on conflict do nothing`,
      [p.id, DEMO_CITY, p.title, p.detail, p.lat, p.lng],
    );
    added.places += res.rowCount ?? 0;
  }
  if (opts.demoDrivers) {
    for (const [i, [id, name, rideType, vehicle, plate, rating, upi, lat, lng]] of demoDrivers.entries()) {
      const res = await db.query(
        `insert into drivers (id, city, name, ride_type, vehicle, plate, rating, upi_id, has_ac, base_lat, base_lng,
                              service_radius_km, work_start, work_end, phone)
         values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 8, 420, 1290, $12) on conflict do nothing`,
        [id, DEMO_CITY, name, rideType, vehicle, plate, rating, upi, rideType.startsWith('cab_'), lat, lng, demoDriverPhone(i)],
      );
      added.drivers += res.rowCount ?? 0;
    }
  }
  return added;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const db = createPool();
  seedConfig(db, { demoDrivers: process.argv.includes('--demo-drivers') })
    .then((added) => console.log('Added', JSON.stringify(added)))
    .finally(() => db.end());
}
