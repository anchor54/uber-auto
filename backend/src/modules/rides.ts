import type { FastifyInstance } from 'fastify';
import type pg from 'pg';
import { z } from 'zod';
import type { AppContext } from '../context.js';
import { addDays, CREATE_DAYS_AHEAD, planRides } from '../domain/ride_plan.js';
import {
  etaMinutes,
  invalidTransition,
  isLate,
  newPin,
  paymentStatus,
  transition,
  waitEndsAt,
  WrongPin,
  type ActorType,
  type DriverMark,
  type Move,
  type MoveContext,
  type RideFacts,
  type RideState,
  type RiderMark,
} from '../domain/rides.js';
import { localDateString } from '../domain/schedule.js';
import { haversineKm } from '../domain/trips.js';
import { ApiError, type Place } from '../domain/types.js';
import { requireDriver, requireRider, requireSession } from './auth.js';
import { queueNotification, type NotificationKind } from './notify.js';

type Queryable = Pick<pg.Pool, 'query'> | pg.PoolClient;

export interface RideRow {
  id: string;
  commute_id: string;
  rider_id: string;
  driver_id: string;
  ride_date: string;
  direction: 'outbound' | 'return';
  pickup: Place;
  drop_off: Place;
  pickup_at: Date;
  window_start: Date;
  window_end: Date;
  trip_minutes: number;
  fare: number;
  ride_type: string;
  pin: string;
  pin_tries: number;
  state: RideState;
  departed_at: Date | null;
  arrived_at: Date | null;
  started_at: Date | null;
  ended_at: Date | null;
  closed_at: Date | null;
  wait_ends_at: Date | null;
  rider_late_used: boolean;
  late_flagged_at: Date | null;
  end_reason: string | null;
  rider_payment: RiderMark;
  rider_marked_at: Date | null;
  driver_payment: DriverMark;
  driver_marked_at: Date | null;
  route: string | null;
}

const RIDE_COLUMNS = `r.*, to_char(r.ride_date, 'YYYY-MM-DD') as ride_date`;

export async function loadRide(db: Queryable, id: string, forUpdate = false): Promise<RideRow | undefined> {
  return (
    await db.query<RideRow>(`select ${RIDE_COLUMNS} from rides r where r.id = $1${forUpdate ? ' for update of r' : ''}`, [id])
  ).rows[0];
}

const OPEN_STATES: RideState[] = ['on_the_way', 'arrived', 'in_ride'];

// ---------------------------------------------------------------- creation

/**
 * Creates the ride records for active commutes from today to 14 days ahead.
 * Unique on (commute, date, direction), so running it twice does nothing.
 * Runs nightly and straight after a commute becomes active.
 */
export async function createRides(ctx: AppContext, commuteId?: string): Promise<number> {
  const now = ctx.clock();
  const today = localDateString(now, ctx.config.utcOffsetMinutes);
  const commutes = (
    await ctx.db.query(
      `select id, rider_id, driver_id, city, pickup, drop_off, weekdays, pickup_time, return_trip, return_time,
              to_char(start_date, 'YYYY-MM-DD') as start_date, trip_minutes, fare, ride_type, route_out, route_back
       from commutes where status = 'active' and ($1::uuid is null or id = $1)`,
      [commuteId ?? null],
    )
  ).rows;
  if (!commutes.length) return 0;
  const holidays = new Set(
    (
      await ctx.db.query<{ day: string }>(
        `select to_char(day, 'YYYY-MM-DD') as day from holidays where city = $1 and day between $2 and $3`,
        [ctx.config.city, today, addDays(today, CREATE_DAYS_AHEAD)],
      )
    ).rows.map((r) => r.day),
  );
  let created = 0;
  for (const c of commutes) {
    const plan = planRides(
      {
        pickup: c.pickup,
        drop: c.drop_off,
        weekdays: c.weekdays,
        pickupTime: c.pickup_time,
        returnTrip: c.return_trip,
        returnTime: c.return_time,
        startDate: c.start_date,
      },
      { from: today, to: addDays(today, CREATE_DAYS_AHEAD), now, utcOffsetMinutes: ctx.config.utcOffsetMinutes, holidays },
    );
    for (const p of plan) {
      const res = await ctx.db.query(
        `insert into rides (commute_id, rider_id, driver_id, ride_date, direction, pickup, drop_off, pickup_at,
                            window_start, window_end, trip_minutes, fare, ride_type, pin, state, closed_at, route)
         values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
         on conflict (commute_id, ride_date, direction) do nothing`,
        [
          c.id, c.rider_id, c.driver_id, p.rideDate, p.direction, JSON.stringify(p.pickup), JSON.stringify(p.drop),
          p.pickupAt, p.windowStart, p.windowEnd, c.trip_minutes, c.fare, c.ride_type, newPin(),
          p.skipped ? 'skipped' : 'scheduled', p.skipped ? now : null,
          p.direction === 'outbound' ? c.route_out : c.route_back,
        ],
      );
      created += res.rowCount ?? 0;
    }
  }
  return created;
}

// ------------------------------------------------------------------- moves

function facts(r: RideRow): RideFacts {
  const activity = [r.departed_at, r.arrived_at, r.started_at].filter((d): d is Date => d !== null);
  return {
    state: r.state,
    pickupAt: r.pickup_at,
    windowStart: r.window_start,
    windowEnd: r.window_end,
    arrivedAt: r.arrived_at,
    waitEndsAt: r.wait_ends_at,
    riderLateUsed: r.rider_late_used,
    pin: r.pin,
    pinTries: r.pin_tries,
    lastActivityAt: activity.length ? new Date(Math.max(...activity.map((d) => d.getTime()))) : r.pickup_at,
  };
}

const metres = (a: { lat: number; lng: number }, b: { lat: number; lng: number }) =>
  haversineKm(a.lat, a.lng, b.lat, b.lng) * 1000;

export interface Actor {
  type: ActorType;
  id: string | null;
}

export interface MoveInput {
  pin?: string;
  lat?: number;
  lng?: number;
  reason?: string;
}

/**
 * Makes [move] on ride [rideId] as [actor], in one transaction: checks the
 * rules, writes the ride and a ride_events row, and queues the
 * notifications the move causes. Only this function changes a ride's state.
 */
export async function applyMove(ctx: AppContext, rideId: string, move: Move, actor: Actor, input: MoveInput = {}): Promise<RideRow> {
  const client = await ctx.db.connect();
  try {
    await client.query('begin');
    const ride = await loadRide(client, rideId, true);
    if (!ride) throw new ApiError(404, 'ride_not_found', 'No such ride');
    if (actor.type === 'driver' && ride.driver_id !== actor.id) throw new ApiError(403, 'not_your_ride', 'This ride is not yours');
    if (actor.type === 'rider' && ride.rider_id !== actor.id) throw new ApiError(403, 'not_your_ride', 'This ride is not yours');

    const now = ctx.clock();
    const c: MoveContext = { now, actor: actor.type };
    if (move === 'depart') {
      // Lock the driver so two departures can't both pass the "busy" check.
      const d = (await client.query<{ on_duty: boolean }>('select on_duty from drivers where id = $1 for update', [ride.driver_id])).rows[0];
      c.driverOnDuty = d?.on_duty ?? false;
      c.driverBusy = !!(
        await client.query(`select 1 from rides where driver_id = $1 and id <> $2 and state = any($3)`, [ride.driver_id, ride.id, OPEN_STATES])
      ).rowCount;
    }
    if (move === 'arrive') {
      const loc = (await client.query<{ lat: number; lng: number }>('select lat, lng from driver_locations where driver_id = $1', [ride.driver_id])).rows[0];
      c.metresToPickup = loc ? metres(loc, ride.pickup) : null;
    }
    if (move === 'start') c.pin = input.pin;
    if (move === 'end') {
      c.metresToDrop = input.lat != null && input.lng != null ? metres({ lat: input.lat, lng: input.lng }, ride.drop_off) : undefined;
      c.endReason = input.reason;
    }
    if (move === 'no_show') {
      c.contactLogged = !!(await client.query('select 1 from ride_contacts where ride_id = $1', [ride.id])).rowCount;
    }

    let result;
    try {
      result = transition(facts(ride), move, c);
    } catch (e) {
      if (e instanceof WrongPin && ride.pin_tries < 3) {
        // Count the wrong try even though the move fails.
        await client.query('update rides set pin_tries = pin_tries + 1 where id = $1', [ride.id]);
        await client.query('commit');
      }
      throw e;
    }

    const sets = Object.entries(result.set);
    await client.query(
      `update rides set state = $2${sets.map(([k], i) => `, ${k} = $${i + 3}`).join('')} where id = $1`,
      [ride.id, result.to, ...sets.map(([, v]) => v)],
    );
    await client.query(
      'insert into ride_events (ride_id, from_state, to_state, actor_type, actor_id, at, data) values ($1, $2, $3, $4, $5, $6, $7)',
      [ride.id, ride.state, result.to, actor.type, actor.id, now, JSON.stringify(move === 'end' && input.reason ? { reason: input.reason } : {})],
    );
    const notify: Partial<Record<Move, NotificationKind>> = { depart: 'driver_on_the_way', arrive: 'driver_arrived' };
    const kind = notify[move];
    if (kind) {
      await queueNotification(client, {
        to: { type: 'rider', id: ride.rider_id },
        kind,
        rideId: ride.id,
        sendAfter: now,
        payload: await notifyPayload(client, ride, ctx),
      });
    }
    await client.query('commit');
    return (await loadRide(ctx.db, ride.id))!;
  } catch (e) {
    await client.query('rollback').catch(() => {});
    throw e;
  } finally {
    client.release();
  }
}

export function localTimeLabel(at: Date, utcOffsetMinutes: number): string {
  const m = Math.round((at.getTime() / 60_000 + utcOffsetMinutes) % 1440 + 1440) % 1440;
  const h = Math.floor(m / 60);
  return `${((h + 11) % 12) + 1}:${String(m % 60).padStart(2, '0')} ${h < 12 ? 'AM' : 'PM'}`;
}

async function notifyPayload(db: Queryable, ride: RideRow, ctx: AppContext) {
  const d = (await db.query<{ name: string }>('select name from drivers where id = $1', [ride.driver_id])).rows[0];
  return {
    driverFirstName: d?.name.split(' ')[0] ?? 'Your driver',
    pickupTime: localTimeLabel(ride.pickup_at, ctx.config.utcOffsetMinutes),
  };
}

// --------------------------------------------------------------- live view

export interface LiveView {
  state: RideState;
  driver: { lat: number; lng: number; heading: number | null; at: string } | null;
  etaMinutes: number | null;
  etaAt: string | null;
  waitEndsAt: string | null;
  late: boolean;
  pinLocked: boolean;
  updatedAt: string | null;
  /** The server's clock, so the app's countdowns don't depend on the phone's. */
  serverTime: string;
}

/**
 * Where the driver is and when they'll get there. Also sets the late flag
 * the moment the ETA crosses the threshold (the worker does the same for
 * riders who aren't looking), and queues "Driver running late" once.
 */
export async function liveView(ctx: AppContext, ride: RideRow): Promise<LiveView> {
  const now = ctx.clock();
  const loc = OPEN_STATES.includes(ride.state)
    ? (
        await ctx.db.query<{ lat: number; lng: number; heading: number | null; at: Date }>(
          'select lat, lng, heading, at from driver_locations where driver_id = $1',
          [ride.driver_id],
        )
      ).rows[0]
    : undefined;
  let eta: number | null = null;
  if (loc && ride.state === 'on_the_way') eta = etaMinutes(haversineKm(loc.lat, loc.lng, ride.pickup.lat, ride.pickup.lng));
  if (loc && ride.state === 'in_ride') eta = inRideEta(ride, loc);
  if (ride.state === 'arrived') eta = 0;
  const late = ride.late_flagged_at !== null || (eta !== null && (await flagIfLate(ctx, ride, eta)));
  return {
    state: ride.state,
    driver: loc ? { lat: loc.lat, lng: loc.lng, heading: loc.heading, at: loc.at.toISOString() } : null,
    etaMinutes: eta,
    etaAt: eta === null ? null : new Date(now.getTime() + eta * 60_000).toISOString(),
    waitEndsAt: ride.wait_ends_at?.toISOString() ?? null,
    late,
    pinLocked: ride.pin_tries >= 3,
    updatedAt: loc?.at.toISOString() ?? null,
    serverTime: now.toISOString(),
  };
}

/**
 * Minutes left in the ride: the trip time it was booked with (a road time
 * from the maps provider) times the share of the straight-line distance
 * still to go. Agrees with the trip time shown at booking, and needs no
 * maps call on every 5-second poll.
 */
export function inRideEta(ride: Pick<RideRow, 'pickup' | 'drop_off' | 'trip_minutes'>, at: { lat: number; lng: number }): number {
  const total = haversineKm(ride.pickup.lat, ride.pickup.lng, ride.drop_off.lat, ride.drop_off.lng);
  const left = haversineKm(at.lat, at.lng, ride.drop_off.lat, ride.drop_off.lng);
  if (total < 0.05) return 1;
  return Math.max(1, Math.round(ride.trip_minutes * Math.min(1, left / total)));
}

/** Sets late_flagged_at once and queues the push. Returns whether the ride is late. */
export async function flagIfLate(ctx: AppContext, ride: RideRow, eta: number): Promise<boolean> {
  if (!['scheduled', 'on_the_way'].includes(ride.state)) return ride.late_flagged_at !== null;
  const now = ctx.clock();
  if (!isLate(now, eta, ride.window_end)) return false;
  const res = await ctx.db.query('update rides set late_flagged_at = $2 where id = $1 and late_flagged_at is null', [ride.id, now]);
  if (res.rowCount) {
    await queueNotification(ctx.db, {
      to: { type: 'rider', id: ride.rider_id },
      kind: 'driver_running_late',
      rideId: ride.id,
      sendAfter: now,
      payload: await notifyPayload(ctx.db, ride, ctx),
    });
  }
  return true;
}

// ------------------------------------------------------------ JSON shapes

async function driverCard(db: Queryable, driverId: string) {
  const d = (
    await db.query('select id, name, vehicle, plate, rating, upi_id, phone from drivers where id = $1', [driverId])
  ).rows[0];
  return { id: d.id, name: d.name, vehicle: d.vehicle, plate: d.plate, rating: Number(d.rating), upiId: d.upi_id, phone: d.phone };
}

async function ratingFor(db: Queryable, rideId: string) {
  const r = (await db.query('select stars, tags, note, dont_match from ride_ratings where ride_id = $1', [rideId])).rows[0];
  return r ? { stars: r.stars, tags: r.tags, note: r.note, dontMatch: r.dont_match } : null;
}

const iso = (d: Date | null) => d?.toISOString() ?? null;

/** A ride as the rider sees it. The PIN shows once the driver has departed. */
export async function riderRideJson(ctx: AppContext, r: RideRow) {
  const showPin = ['on_the_way', 'arrived'].includes(r.state);
  return {
    id: r.id,
    commuteId: r.commute_id,
    date: r.ride_date,
    direction: r.direction,
    state: r.state,
    pickup: r.pickup,
    drop: r.drop_off,
    pickupAt: r.pickup_at.toISOString(),
    windowStart: r.window_start.toISOString(),
    windowEnd: r.window_end.toISOString(),
    tripMinutes: r.trip_minutes,
    fare: r.fare,
    rideType: r.ride_type,
    /** Encoded polyline of the road route, or null (draw a straight line). */
    route: r.route,
    pin: showPin ? r.pin : null,
    pinLocked: r.pin_tries >= 3,
    departedAt: iso(r.departed_at),
    arrivedAt: iso(r.arrived_at),
    startedAt: iso(r.started_at),
    endedAt: iso(r.ended_at),
    waitEndsAt: iso(r.wait_ends_at),
    riderLateUsed: r.rider_late_used,
    late: r.late_flagged_at !== null,
    riderPayment: r.rider_payment,
    driverPayment: r.driver_payment,
    paymentStatus: paymentStatus(r.rider_payment, r.driver_payment),
    rating: await ratingFor(ctx.db, r.id),
    driver: await driverCard(ctx.db, r.driver_id),
  };
}

/** A ride as the driver sees it on their run. */
async function driverRideJson(ctx: AppContext, r: RideRow) {
  const rider = (await ctx.db.query<{ name: string | null; phone: string }>('select name, phone from riders where id = $1', [r.rider_id])).rows[0];
  const usual = (await ctx.db.query<{ usual_payment: string }>('select usual_payment from commutes where id = $1', [r.commute_id])).rows[0];
  return {
    id: r.id,
    date: r.ride_date,
    direction: r.direction,
    state: r.state,
    riderName: rider?.name ?? 'Rider',
    riderPhone: OPEN_STATES.includes(r.state) ? rider?.phone : null,
    pickup: r.pickup,
    drop: r.drop_off,
    pickupAt: r.pickup_at.toISOString(),
    windowStart: r.window_start.toISOString(),
    windowEnd: r.window_end.toISOString(),
    fare: r.fare,
    usualPayment: usual?.usual_payment,
    pinTriesLeft: Math.max(0, 3 - r.pin_tries),
    waitEndsAt: iso(r.wait_ends_at),
    riderLateUsed: r.rider_late_used,
    riderPayment: r.rider_payment,
    driverPayment: r.driver_payment,
    paymentStatus: paymentStatus(r.rider_payment, r.driver_payment),
  };
}

/** The rider's next ride that hasn't finished, for Home. */
export async function nextRideFor(ctx: AppContext, riderId: string) {
  const r = (
    await ctx.db.query<RideRow>(
      `select ${RIDE_COLUMNS} from rides r
       where r.rider_id = $1 and r.state in ('scheduled', 'on_the_way', 'arrived', 'in_ride')
       order by r.pickup_at limit 1`,
      [riderId],
    )
  ).rows[0];
  return r ? riderRideJson(ctx, r) : null;
}

// ---------------------------------------------------------------- location

const pointSchema = z.object({
  lat: z.number().min(-90).max(90),
  lng: z.number().min(-180).max(180),
  heading: z.number().min(0).max(360).nullable().optional(),
  speed: z.number().min(0).nullable().optional(),
  at: z.string().datetime(),
});

export const TRACK_EVERY_MS = 30_000;
const MAX_AGE_MS = 2 * 60_000;
const MAX_AHEAD_MS = 30_000;

/** Stores the driver's latest position, and a track point every 30 s while a ride is under way. */
export async function recordLocation(ctx: AppContext, driverId: string, points: z.infer<typeof pointSchema>[]) {
  const now = ctx.clock().getTime();
  for (const p of points) {
    const at = Date.parse(p.at);
    if (at < now - MAX_AGE_MS || at > now + MAX_AHEAD_MS) {
      throw new ApiError(400, 'stale_location', 'Location points must be from the last 2 minutes');
    }
  }
  const latest = points.reduce((a, b) => (Date.parse(b.at) > Date.parse(a.at) ? b : a));
  const current = (
    await ctx.db.query<{ id: string }>(`select id from rides where driver_id = $1 and state = any($2) order by pickup_at limit 1`, [
      driverId,
      OPEN_STATES,
    ])
  ).rows[0];
  await ctx.db.query(
    `insert into driver_locations (driver_id, lat, lng, heading, speed, at, ride_id) values ($1, $2, $3, $4, $5, $6, $7)
     on conflict (driver_id) do update set lat = excluded.lat, lng = excluded.lng, heading = excluded.heading,
       speed = excluded.speed, at = excluded.at, ride_id = excluded.ride_id
     where driver_locations.at <= excluded.at`,
    [driverId, latest.lat, latest.lng, latest.heading ?? null, latest.speed ?? null, new Date(latest.at), current?.id ?? null],
  );
  if (current) {
    const last = (await ctx.db.query<{ at: Date }>('select max(at) as at from ride_track where ride_id = $1', [current.id])).rows[0]?.at;
    if (!last || Date.parse(latest.at) - last.getTime() >= TRACK_EVERY_MS) {
      await ctx.db.query('insert into ride_track (ride_id, lat, lng, at) values ($1, $2, $3, $4)', [
        current.id,
        latest.lat,
        latest.lng,
        new Date(latest.at),
      ]);
    }
  }
}

export async function logContact(ctx: AppContext, rideId: string, driverId: string, kind: 'call' | 'alert') {
  const ride = await loadRide(ctx.db, rideId);
  if (!ride) throw new ApiError(404, 'ride_not_found', 'No such ride');
  if (ride.driver_id !== driverId) throw new ApiError(403, 'not_your_ride', 'This ride is not yours');
  if (!['on_the_way', 'arrived'].includes(ride.state)) {
    throw new ApiError(409, 'invalid_transition', 'You can call or alert the rider once you are on the way');
  }
  await ctx.db.query('insert into ride_contacts (ride_id, driver_id, kind, at) values ($1, $2, $3, $4)', [rideId, driverId, kind, ctx.clock()]);
  if (kind === 'alert') {
    await queueNotification(ctx.db, {
      to: { type: 'rider', id: ride.rider_id },
      kind: 'driver_alert',
      rideId,
      sendAfter: ctx.clock(),
      payload: await notifyPayload(ctx.db, ride, ctx),
    });
  }
}

export async function setDuty(ctx: AppContext, driverId: string, onDuty: boolean) {
  if (!onDuty) {
    const busy = (await ctx.db.query(`select 1 from rides where driver_id = $1 and state = any($2)`, [driverId, OPEN_STATES])).rowCount;
    if (busy) throw new ApiError(409, 'rides_in_progress', 'Finish your current ride before going off duty');
  }
  await ctx.db.query('update drivers set on_duty = $2 where id = $1', [driverId, onDuty]);
}

// ---------------------------------------------------------------- payment

export async function markPayment(ctx: AppContext, rideId: string, side: Actor, mark: string) {
  const ride = await loadRide(ctx.db, rideId);
  if (!ride) throw new ApiError(404, 'ride_not_found', 'No such ride');
  const own = side.type === 'rider' ? ride.rider_id : ride.driver_id;
  if (own !== side.id) throw new ApiError(403, 'not_your_ride', 'This ride is not yours');
  if (ride.state !== 'completed') throw new ApiError(409, 'not_completed', 'Mark payment once the ride has ended');
  const [col, at] = side.type === 'rider' ? ['rider_payment', 'rider_marked_at'] : ['driver_payment', 'driver_marked_at'];
  const row = (
    await ctx.db.query<{ rider_payment: RiderMark; driver_payment: DriverMark }>(
      `update rides set ${col} = $2, ${at} = $3 where id = $1 returning rider_payment, driver_payment`,
      [rideId, mark, ctx.clock()],
    )
  ).rows[0]!;
  return {
    paymentStatus: paymentStatus(row.rider_payment, row.driver_payment),
    riderPayment: row.rider_payment,
    driverPayment: row.driver_payment,
  };
}

export const RATING_EDIT_DAYS = 7;

// ------------------------------------------------------------------ routes

const idParam = z.object({ id: z.string().uuid() });

async function riderRide(ctx: AppContext, riderId: string, id: string) {
  const ride = await loadRide(ctx.db, id);
  if (!ride) throw new ApiError(404, 'ride_not_found', 'No such ride');
  if (ride.rider_id !== riderId) throw new ApiError(403, 'not_your_ride', 'This ride is not yours');
  return ride;
}

export function rideRoutes(app: FastifyInstance, ctx: AppContext) {
  // ---- rider ----

  app.get('/v1/rides/upcoming', async (req) => {
    const riderId = await requireRider(ctx, req);
    const { days } = z.object({ days: z.coerce.number().int().min(1).max(14).default(7) }).parse(req.query);
    const today = localDateString(ctx.clock(), ctx.config.utcOffsetMinutes);
    const rows = (
      await ctx.db.query<RideRow>(
        `select ${RIDE_COLUMNS} from rides r
         where r.rider_id = $1 and r.ride_date >= $2 and r.ride_date < $3
         order by r.pickup_at`,
        [riderId, today, addDays(today, days)],
      )
    ).rows;
    return { rides: await Promise.all(rows.map((r) => riderRideJson(ctx, r))) };
  });

  app.get('/v1/rides/unsettled', async (req) => {
    const riderId = await requireRider(ctx, req);
    const rows = (
      await ctx.db.query<RideRow>(
        `select ${RIDE_COLUMNS} from rides r
         where r.rider_id = $1 and r.state = 'completed' and r.rider_payment is null
           and r.ended_at > $2::timestamptz - interval '7 days'
           and not exists (select 1 from ride_ratings x where x.ride_id = r.id)
         order by r.ended_at desc`,
        [riderId, ctx.clock()],
      )
    ).rows;
    return { rides: await Promise.all(rows.map((r) => riderRideJson(ctx, r))) };
  });

  app.get('/v1/rides/:id', async (req) => {
    const riderId = await requireRider(ctx, req);
    return riderRideJson(ctx, await riderRide(ctx, riderId, idParam.parse(req.params).id));
  });

  app.get('/v1/rides/:id/live', async (req) => {
    const riderId = await requireRider(ctx, req);
    return liveView(ctx, await riderRide(ctx, riderId, idParam.parse(req.params).id));
  });

  /** "I'm 5 min late": once per leg; the driver's wait becomes 10 minutes. */
  app.post('/v1/rides/:id/running-late', async (req) => {
    const riderId = await requireRider(ctx, req);
    const ride = await riderRide(ctx, riderId, idParam.parse(req.params).id);
    if (ride.rider_late_used) throw new ApiError(409, 'already_used', 'You can only do this once per ride');
    if (!['scheduled', 'on_the_way', 'arrived'].includes(ride.state)) {
      throw new ApiError(409, 'invalid_transition', 'This ride has already started');
    }
    const waitEnds = ride.arrived_at ? waitEndsAt(ride.arrived_at, ride.window_start, true) : null;
    await ctx.db.query('update rides set rider_late_used = true, wait_ends_at = coalesce($2, wait_ends_at) where id = $1', [ride.id, waitEnds]);
    await ctx.db.query(
      `insert into ride_events (ride_id, from_state, to_state, actor_type, actor_id, at, data) values ($1, $2, $2, 'rider', $3, $4, '{"riderLate":true}')`,
      [ride.id, ride.state, riderId, ctx.clock()],
    );
    await queueNotification(ctx.db, {
      to: { type: 'driver', id: ride.driver_id },
      kind: 'rider_running_late',
      rideId: ride.id,
      sendAfter: ctx.clock(),
    });
    return riderRideJson(ctx, (await loadRide(ctx.db, ride.id))!);
  });

  app.post('/v1/rides/:id/payment', async (req) => {
    const riderId = await requireRider(ctx, req);
    const { method } = z.object({ method: z.enum(['upi', 'cash']) }).parse(req.body);
    return markPayment(ctx, idParam.parse(req.params).id, { type: 'rider', id: riderId }, method);
  });

  app.post('/v1/rides/:id/rating', async (req, reply) => {
    const riderId = await requireRider(ctx, req);
    const body = z
      .object({
        stars: z.number().int().min(1).max(5),
        tags: z.array(z.string().min(1).max(40)).max(10).default([]),
        note: z.string().max(500).optional(),
        dontMatch: z.boolean().default(false),
      })
      .parse(req.body);
    const ride = await riderRide(ctx, riderId, idParam.parse(req.params).id);
    if (ride.state !== 'completed') throw new ApiError(409, 'not_completed', 'Rate the ride once it has ended');
    const now = ctx.clock();
    if (ride.ended_at && now.getTime() - ride.ended_at.getTime() > RATING_EDIT_DAYS * 86_400_000) {
      throw new ApiError(409, 'rating_closed', `Ratings can be given or changed for ${RATING_EDIT_DAYS} days after the ride`);
    }
    await ctx.db.query(
      `insert into ride_ratings (ride_id, stars, tags, note, dont_match, created_at, updated_at) values ($1, $2, $3, $4, $5, $6, $6)
       on conflict (ride_id) do update set stars = excluded.stars, tags = excluded.tags, note = excluded.note,
         dont_match = excluded.dont_match, updated_at = excluded.updated_at`,
      [ride.id, body.stars, body.tags, body.note ?? null, body.dontMatch, now],
    );
    return reply.code(204).send();
  });

  /** Push token for this device; riders and drivers both register. */
  app.put('/v1/devices', async (req, reply) => {
    const s = await requireSession(ctx, req);
    const { pushToken, platform } = z
      .object({ pushToken: z.string().min(10).max(4096), platform: z.enum(['android', 'ios', 'web']) })
      .parse(req.body);
    await ctx.db.query(
      `insert into devices (push_token, owner_type, owner_id, platform, updated_at) values ($1, $2, $3, $4, $5)
       on conflict (push_token) do update set owner_type = excluded.owner_type, owner_id = excluded.owner_id,
         platform = excluded.platform, updated_at = excluded.updated_at`,
      [pushToken, s.role, s.id, platform, ctx.clock()],
    );
    return reply.code(204).send();
  });

  // ---- driver (flow D3's API; the driver app's screens come later) ----

  app.get('/v1/driver/run', async (req) => {
    const driverId = await requireDriver(ctx, req);
    const today = localDateString(ctx.clock(), ctx.config.utcOffsetMinutes);
    const { date } = z.object({ date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/).default(today) }).parse(req.query);
    const rows = (
      await ctx.db.query<RideRow>(`select ${RIDE_COLUMNS} from rides r where r.driver_id = $1 and r.ride_date = $2 order by r.pickup_at`, [
        driverId,
        date,
      ])
    ).rows;
    return { date, rides: await Promise.all(rows.map((r) => driverRideJson(ctx, r))) };
  });

  app.put('/v1/driver/duty', async (req, reply) => {
    const driverId = await requireDriver(ctx, req);
    const { onDuty } = z.object({ onDuty: z.boolean() }).parse(req.body);
    await setDuty(ctx, driverId, onDuty);
    return reply.code(204).send();
  });

  app.post('/v1/driver/location', async (req, reply) => {
    const driverId = await requireDriver(ctx, req);
    const { points } = z.object({ points: z.array(pointSchema).min(1).max(20) }).parse(req.body);
    await recordLocation(ctx, driverId, points);
    return reply.code(204).send();
  });

  const driverMove = (path: string, move: Move, input: (body: unknown) => MoveInput = () => ({})) =>
    app.post(`/v1/driver/rides/:id/${path}`, async (req) => {
      const driverId = await requireDriver(ctx, req);
      const ride = await applyMove(ctx, idParam.parse(req.params).id, move, { type: 'driver', id: driverId }, input(req.body));
      return driverRideJson(ctx, ride);
    });

  driverMove('depart', 'depart');
  driverMove('arrive', 'arrive');
  driverMove('start', 'start', (b) => z.object({ pin: z.string().regex(/^\d{4}$/) }).parse(b));
  driverMove('end', 'end', (b) =>
    z.object({ lat: z.number(), lng: z.number(), reason: z.string().max(300).optional() }).parse(b),
  );
  driverMove('no-show', 'no_show');

  app.post('/v1/driver/rides/:id/contact', async (req, reply) => {
    const driverId = await requireDriver(ctx, req);
    const { kind } = z.object({ kind: z.enum(['call', 'alert']) }).parse(req.body);
    await logContact(ctx, idParam.parse(req.params).id, driverId, kind);
    return reply.code(204).send();
  });

  app.post('/v1/driver/rides/:id/payment', async (req) => {
    const driverId = await requireDriver(ctx, req);
    const { mark } = z.object({ mark: z.enum(['upi', 'cash', 'not_paid']) }).parse(req.body);
    return markPayment(ctx, idParam.parse(req.params).id, { type: 'driver', id: driverId }, mark);
  });
}
