import type { FastifyInstance } from 'fastify';
import type pg from 'pg';
import { z } from 'zod';
import type { AppContext } from '../context.js';
import { firstRideDate, legsClash, localDateString, pickupForArrival, weeklyLegs } from '../domain/schedule.js';
import { estimateTrip, haversineKm, suggestedFare, type TripEstimate } from '../domain/trips.js';
import { ApiError, commuteRequestSchema, flowFor, paymentMethods, type CommuteRequest, type Place } from '../domain/types.js';
import { requireRider } from './auth.js';
import { bookingOptions, fareRules } from './booking.js';

interface DriverRow {
  id: string;
  name: string;
  ride_type: string;
  vehicle: string;
  plate: string;
  rating: string;
  upi_id: string;
  gender: string;
  has_ac: boolean;
  base_lat: number;
  base_lng: number;
  service_radius_km: string;
  work_start: number;
  work_end: number;
}

export interface Candidate {
  driver: DriverRow;
  distanceToPickupKm: number;
}

type Queryable = Pick<pg.Pool, 'query'> | pg.PoolClient;

/** The commute as the server will run it: pickup time worked out, start date set. */
export interface ResolvedCommute extends CommuteRequest {
  pickupTime: number;
  startDate: string;
  trip: TripEstimate;
}

export function resolveRequest(ctx: AppContext, req: CommuteRequest): ResolvedCommute {
  const trip = estimateTrip(req.pickup, req.drop);
  const pickupTime = req.timing === 'pickup_at' ? req.time : pickupForArrival(req.time, trip.minutes);
  const today = localDateString(ctx.clock(), ctx.config.utcOffsetMinutes);
  return { ...req, pickupTime, startDate: firstRideDate(today, req.weekdays), trip };
}

/**
 * Can [driver] take every leg of [c] on top of what they already drive?
 * The shared timeline check used by recurring matching.
 */
export async function canInsert(db: Queryable, driver: DriverRow, c: ResolvedCommute): Promise<boolean> {
  const legs = weeklyLegs({ ...c, tripMinutes: c.trip.minutes });
  if (legs.some((l) => l.start < driver.work_start || l.end > driver.work_end)) return false;
  const existing = (
    await db.query<{ weekdays: number[]; pickup_time: number; return_trip: boolean; return_time: number; trip_minutes: number }>(
      `select weekdays, pickup_time, return_trip, return_time, trip_minutes from commutes
       where driver_id = $1 and status in ('awaiting_driver', 'active')`,
      [driver.id],
    )
  ).rows;
  for (const e of existing) {
    const theirs = weeklyLegs({
      weekdays: e.weekdays,
      pickupTime: e.pickup_time,
      returnTrip: e.return_trip,
      returnTime: e.return_time,
      tripMinutes: e.trip_minutes,
    });
    if (legs.some((a) => theirs.some((b) => legsClash(a, b)))) return false;
  }
  return true;
}

/** Drivers who can serve the commute, best first (nearest base, then rating). */
export async function findCandidates(ctx: AppContext, c: ResolvedCommute, limit = 3): Promise<Candidate[]> {
  const rows = (
    await ctx.db.query<DriverRow>(
      `select id, name, ride_type, vehicle, plate, rating, upi_id, gender, has_ac, base_lat, base_lng,
              service_radius_km, work_start, work_end
       from drivers where city = $1 and active and ride_type = $2`,
      [ctx.config.city, c.rideType],
    )
  ).rows;
  const candidates: Candidate[] = [];
  for (const d of rows) {
    if (c.preferences.includes('woman_driver') && d.gender !== 'female') continue;
    if (c.preferences.includes('ac_on') && !d.has_ac) continue;
    const distanceToPickupKm = haversineKm(d.base_lat, d.base_lng, c.pickup.lat, c.pickup.lng);
    if (distanceToPickupKm > Number(d.service_radius_km)) continue;
    if (!(await canInsert(ctx.db, d, c))) continue;
    candidates.push({ driver: d, distanceToPickupKm });
  }
  candidates.sort((a, b) => a.distanceToPickupKm - b.distanceToPickupKm || Number(b.driver.rating) - Number(a.driver.rating));
  return candidates.slice(0, limit);
}

function area(p: Place) {
  return p.detail ? (p.detail.split(', ').at(-1) ?? p.title) : p.title;
}

function driverJson(d: DriverRow) {
  return { id: d.id, name: d.name, vehicle: d.vehicle, plate: d.plate, rating: Number(d.rating), upiId: d.upi_id };
}

function firstName(name: string) {
  return name.split(' ')[0] ?? name;
}

export function commuteRoutes(app: FastifyInstance, ctx: AppContext) {
  /** R1.4: propose drivers and hold them for the rider while they confirm. */
  app.post('/v1/commutes/proposals', async (req) => {
    const riderId = await requireRider(ctx, req);
    const { request } = z.object({ request: commuteRequestSchema }).parse(req.body);
    const c = resolveRequest(ctx, request);

    const options = await bookingOptions(ctx, flowFor(c.repeat), c.pickup, c.drop, c.pickupTime);
    const option = options.options.find((o) => o.type === c.rideType);
    if (!option) throw new ApiError(422, 'ride_type_not_allowed', 'That ride type is not offered for this booking in your city');

    const candidates = await findCandidates(ctx, c);
    const expiresAt = new Date(ctx.clock().getTime() + ctx.config.proposalHoldMinutes * 60_000);
    const proposals = [];
    for (const [rank, cand] of candidates.entries()) {
      const d = cand.driver;
      const row = (
        await ctx.db.query<{ id: string }>(
          `insert into match_proposals (rider_id, driver_id, request, fare, trip_minutes, distance_km, rank, expires_at)
           values ($1, $2, $3, $4, $5, $6, $7, $8) returning id`,
          [riderId, d.id, JSON.stringify(c), option.fare, c.trip.minutes, c.trip.distanceKm, rank, expiresAt],
        )
      ).rows[0]!;
      proposals.push({
        id: row.id,
        driver: driverJson(d),
        fare: option.fare,
        trip: c.trip,
        expiresAt: expiresAt.toISOString(),
        reasons: [
          c.returnTrip ? 'Same driver every day, both directions' : 'Same driver on every ride',
          rank === 0 ? `Already drives near ${area(c.pickup)} at this time` : 'Has a free slot at that time on your days',
          `If ${firstName(d.name)} can't make it, a backup driver is confirmed the night before`,
        ],
      });
    }
    return { pickupTime: c.pickupTime, startDate: c.startDate, proposals };
  });

  /** R1.5: confirm a held proposal. Nothing is paid; riders pay drivers directly. */
  app.post('/v1/commutes', async (req, reply) => {
    const riderId = await requireRider(ctx, req);
    const body = z.object({ proposalId: z.string().uuid(), usualPayment: z.enum(paymentMethods) }).parse(req.body);
    const client = await ctx.db.connect();
    try {
      await client.query('begin');
      const p = (
        await client.query<{ rider_id: string; driver_id: string; request: ResolvedCommute; fare: number; expires_at: Date }>(
          'select rider_id, driver_id, request, fare, expires_at from match_proposals where id = $1',
          [body.proposalId],
        )
      ).rows[0];
      if (!p || p.rider_id !== riderId) throw new ApiError(404, 'proposal_not_found', 'That match is no longer available');
      if (p.expires_at <= ctx.clock()) throw new ApiError(410, 'proposal_expired', 'The driver hold ran out. Search again.');

      // Lock the driver row so two riders can't take the same slot at once.
      const driver = (
        await client.query<DriverRow>(
          `select id, name, ride_type, vehicle, plate, rating, upi_id, gender, has_ac, base_lat, base_lng,
                  service_radius_km, work_start, work_end
           from drivers where id = $1 and active for update`,
          [p.driver_id],
        )
      ).rows[0];
      if (!driver || !(await canInsert(client, driver, p.request))) {
        throw new ApiError(409, 'driver_unavailable', 'That driver just filled this slot. Pick another driver.');
      }
      const c = p.request;
      const status = ctx.config.autoAcceptCommutes ? 'active' : 'awaiting_driver';
      const row = (
        await client.query<{ id: string }>(
          `insert into commutes (rider_id, driver_id, city, pickup, drop_off, repeat, weekdays, timing, time_min, pickup_time,
                                 return_trip, return_time, start_date, ride_type, preferences, fare, trip_minutes, distance_km,
                                 usual_payment, status)
           values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
           returning id`,
          [
            riderId, driver.id, ctx.config.city, JSON.stringify(c.pickup), JSON.stringify(c.drop), c.repeat, c.weekdays,
            c.timing, c.time, c.pickupTime, c.returnTrip, c.returnTime, c.startDate, c.rideType, c.preferences, p.fare,
            c.trip.minutes, c.trip.distanceKm, body.usualPayment, status,
          ],
        )
      ).rows[0]!;
      await client.query('update riders set usual_payment = $2 where id = $1', [riderId, body.usualPayment]);
      await client.query('delete from match_proposals where rider_id = $1', [riderId]);
      await client.query('commit');
      return reply.code(201).send(await loadCommute(ctx, row.id));
    } catch (e) {
      await client.query('rollback');
      throw e;
    } finally {
      client.release();
    }
  });

  /** No driver yet: keep the request and tell the rider when one is free. */
  app.post('/v1/waitlist', async (req, reply) => {
    const riderId = await requireRider(ctx, req);
    const { request } = z.object({ request: commuteRequestSchema }).parse(req.body);
    const row = (
      await ctx.db.query<{ id: string }>('insert into waitlist (rider_id, request) values ($1, $2) returning id', [
        riderId,
        JSON.stringify(request),
      ])
    ).rows[0]!;
    return reply.code(201).send({ id: row.id });
  });

  /** R1.6 / R1.6b: what Home shows. */
  app.get('/v1/home', async (req) => {
    const riderId = await requireRider(ctx, req);
    const rider = (await ctx.db.query('select id, phone, name, usual_payment from riders where id = $1', [riderId])).rows[0];
    const current = (
      await ctx.db.query<{ id: string }>(
        `select id from commutes where rider_id = $1 and status <> 'cancelled' order by created_at desc limit 1`,
        [riderId],
      )
    ).rows[0];
    return {
      rider: { id: rider.id, phone: rider.phone, name: rider.name, usualPayment: rider.usual_payment },
      commute: current ? await loadCommute(ctx, current.id) : null,
      recentRides: [],
    };
  });
}

export async function loadCommute(ctx: AppContext, id: string) {
  const r = (
    await ctx.db.query(
      `select c.*, to_char(c.start_date, 'YYYY-MM-DD') as start_date_text,
              d.id as d_id, d.name as d_name, d.vehicle as d_vehicle, d.plate as d_plate, d.rating as d_rating, d.upi_id as d_upi
       from commutes c join drivers d on d.id = c.driver_id where c.id = $1`,
      [id],
    )
  ).rows[0];
  return {
    id: r.id,
    status: r.status,
    pickup: r.pickup,
    drop: r.drop_off,
    repeat: r.repeat,
    weekdays: r.weekdays,
    timing: r.timing,
    time: r.time_min,
    pickupTime: r.pickup_time,
    returnTrip: r.return_trip,
    returnTime: r.return_time,
    startDate: r.start_date_text,
    rideType: r.ride_type,
    preferences: r.preferences,
    fare: r.fare,
    usualPayment: r.usual_payment,
    trip: { minutes: r.trip_minutes, distanceKm: Number(r.distance_km) },
    driver: { id: r.d_id, name: r.d_name, vehicle: r.d_vehicle, plate: r.d_plate, rating: Number(r.d_rating), upiId: r.d_upi },
  };
}

// Re-exported so tests can check fares without going through HTTP.
export { fareRules, suggestedFare };
