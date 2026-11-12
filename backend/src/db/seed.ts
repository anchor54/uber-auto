import { fileURLToPath } from 'node:url';
import { defaultFareRules } from '../domain/trips.js';
import { bookingFlows, rideTypes } from '../domain/types.js';
import { migrate } from './migrate.js';
import type { Db } from './pool.js';
import { createPool } from './pool.js';

export const DEMO_CITY = 'blr';

/** Same places as the rider app's demo backend (lib/core/fake_api.dart). */
export const demoPlaces = [
  { id: 'home', title: 'Home', detail: '14th Main, HSR Layout', lat: 12.9121, lng: 77.6446 },
  { id: 'etv', title: 'Embassy Tech Village', detail: 'Gate 3', lat: 12.9406, lng: 77.6964 },
  { id: 'manyata', title: 'Manyata Tech Park', detail: 'Hebbal', lat: 13.0475, lng: 77.6203 },
  { id: 'itpl', title: 'ITPL', detail: 'Whitefield', lat: 12.9866, lng: 77.7378 },
  { id: 'forum', title: 'Forum Mall', detail: 'Koramangala', lat: 12.9346, lng: 77.6113 },
  { id: 'indiranagar', title: 'Indiranagar Metro', detail: '100 Feet Road', lat: 12.9784, lng: 77.6408 },
  { id: 'airport', title: 'Kempegowda Airport', detail: 'Terminal 2', lat: 13.1989, lng: 77.7068 },
];

type SeedDriver = [id: string, name: string, rideType: string, vehicle: string, plate: string, rating: number, upi: string, lat: number, lng: number];

/** Same drivers as the app's demo data, based around south-east Bengaluru. */
const drivers: SeedDriver[] = [
  ['d_arjun', 'Arjun Rao', 'bike', 'Black Splendor · helmet', 'KA 51 EX 2210', 4.8, 'arjun.rao@okhdfc', 12.914, 77.638],
  ['d_vinay', 'Vinay S', 'bike', 'Red Pulsar · helmet', 'KA 03 HY 7781', 4.7, 'vinay.s@ybl', 12.925, 77.65],
  ['d_kiran', 'Kiran Gowda', 'bike', 'Blue Activa · helmet', 'KA 05 KP 9012', 4.6, 'kiran.g@okaxis', 12.93, 77.62],
  ['d_manju', 'Manjunath B', 'auto', 'Green auto · 3 seats', 'KA 01 AC 4420', 4.8, 'manju.b@ybl', 12.913, 77.641],
  ['d_raju', 'Raju K', 'auto', 'Green auto · 3 seats', 'KA 05 AD 1189', 4.7, 'raju.k@okicici', 12.92, 77.655],
  ['d_salim', 'Salim Pasha', 'auto', 'Green auto · 3 seats', 'KA 02 AB 7765', 4.6, 'salim.p@ybl', 12.935, 77.625],
  ['d_naveen', 'Naveen P', 'cab_hatchback', 'Grey Swift · AC · 4 seats', 'KA 03 MB 5521', 4.8, 'naveen.p@okhdfc', 12.912, 77.646],
  ['d_sunil', 'Sunil R', 'cab_hatchback', 'White WagonR · AC · 4 seats', 'KA 04 MC 3390', 4.7, 'sunil.r@ybl', 12.922, 77.66],
  ['d_mohan', 'Mohan D', 'cab_hatchback', 'Red i20 · AC · 4 seats', 'KA 51 MD 1102', 4.6, 'mohan.d@okaxis', 12.94, 77.63],
  ['d_ravi', 'Ravi Kumar', 'cab_sedan', 'White Dzire · AC · 4 seats', 'KA 01 AB 1234', 4.9, 'ravi.kumar@okbank', 12.9125, 77.6445],
  ['d_suresh', 'Suresh M', 'cab_sedan', 'Silver Etios · AC · 4 seats', 'KA 05 MN 4410', 4.8, 'suresh.m@ybl', 12.92, 77.652],
  ['d_imran', 'Imran K', 'cab_sedan', 'Silver Honda City · AC · 4 seats', 'KA 05 MK 2290', 4.8, 'imran.k@okaxis', 12.934, 77.62],
  ['d_prakash', 'Prakash N', 'cab_suv', 'White Ertiga · AC · 6 seats', 'KA 01 MP 8890', 4.8, 'prakash.n@okhdfc', 12.913, 77.643],
  ['d_joseph', 'Joseph A', 'cab_suv', 'Grey Innova · AC · 7 seats', 'KA 02 MJ 3345', 4.7, 'joseph.a@ybl', 12.926, 77.648],
  ['d_ganesh', 'Ganesh T', 'cab_suv', 'White XL6 · AC · 6 seats', 'KA 53 MG 6610', 4.6, 'ganesh.t@okaxis', 12.938, 77.628],
];

/** Wipes app data and loads the demo city. Used by tests and `npm run seed`. */
export async function seedDemoData(db: Db): Promise<void> {
  await migrate(db);
  await db.query(
    'truncate waitlist, match_proposals, commutes, drivers, ride_type_config, fare_rules, places, sessions, otp_codes, riders cascade',
  );
  for (const p of demoPlaces) {
    await db.query('insert into places (id, city, title, detail, lat, lng) values ($1, $2, $3, $4, $5, $6)', [
      p.id, DEMO_CITY, p.title, p.detail, p.lat, p.lng,
    ]);
  }
  for (const type of rideTypes) {
    const r = defaultFareRules[type];
    await db.query('insert into fare_rules (city, ride_type, base, per_km, minimum) values ($1, $2, $3, $4, $5)', [
      DEMO_CITY, type, r.base, r.perKm, r.minimum,
    ]);
  }
  for (const flow of bookingFlows) {
    await db.query('insert into ride_type_config (city, flow, allowed, default_type) values ($1, $2, $3, $4)', [
      DEMO_CITY, flow, [...rideTypes], flow === 'one_time' ? 'auto' : 'cab_sedan',
    ]);
  }
  for (const [id, name, rideType, vehicle, plate, rating, upi, lat, lng] of drivers) {
    await db.query(
      `insert into drivers (id, city, name, ride_type, vehicle, plate, rating, upi_id, has_ac, base_lat, base_lng,
                            service_radius_km, work_start, work_end)
       values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 8, 420, 1290)`,
      [id, DEMO_CITY, name, rideType, vehicle, plate, rating, upi, rideType.startsWith('cab_'), lat, lng],
    );
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const db = createPool();
  seedDemoData(db)
    .then(() => console.log('Seeded demo city'))
    .finally(() => db.end());
}
