import type { FastifyInstance } from 'fastify';
import { z } from 'zod';
import type { AppContext } from '../context.js';
import { ApiError } from '../domain/types.js';
import { requireDriver } from './auth.js';
import { createRides } from './rides.js';

/**
 * Flow D2: a driver accepts or declines a commute a rider has confirmed.
 *
 * These service functions are the only code that answers for a driver. The
 * driver endpoints call them, and so does the dev simulator, so tests that
 * use the simulator stay valid when the driver app ships.
 */

export type Response = 'accept' | 'decline';

/** Earliest of: N hours from now, or an hour before the first pickup. Never under 15 minutes away. */
export function respondByFor(ctx: AppContext, startDate: string, pickupTime: number): Date {
  const now = ctx.clock().getTime();
  const firstPickupUtc = Date.parse(`${startDate}T00:00:00Z`) + (pickupTime - ctx.config.utcOffsetMinutes) * 60_000;
  const byHours = now + ctx.config.driverResponseHours * 3_600_000;
  const beforePickup = firstPickupUtc - 60 * 60_000;
  return new Date(Math.max(now + 15 * 60_000, Math.min(byHours, beforePickup)));
}

/** Requests nobody answered in time become declined ("no answer"), freeing the slot. Safe to run any time. */
export async function expireUnanswered(ctx: AppContext): Promise<number> {
  const res = await ctx.db.query(
    `update commutes set status = 'declined', decline_reason = 'no_answer', responded_at = $1
     where status = 'awaiting_driver' and respond_by <= $1`,
    [ctx.clock()],
  );
  return res.rowCount ?? 0;
}

/**
 * Accept or decline commute [commuteId] as driver [driverId]. Pass `null` for
 * the driver only from the dev simulator, which acts as whoever was asked.
 */
export async function respondToCommute(ctx: AppContext, driverId: string | null, commuteId: string, response: Response) {
  await expireUnanswered(ctx);
  const client = await ctx.db.connect();
  try {
    await client.query('begin');
    const c = (
      await client.query<{ driver_id: string; status: string; decline_reason: string | null }>(
        'select driver_id, status, decline_reason from commutes where id = $1 for update',
        [commuteId],
      )
    ).rows[0];
    if (!c || (driverId !== null && c.driver_id !== driverId)) {
      throw new ApiError(404, 'request_not_found', 'That request is not for you');
    }
    if (c.status === 'declined' && c.decline_reason === 'no_answer') {
      throw new ApiError(410, 'request_expired', 'This request ran out of time. The rider has been asked to pick another driver.');
    }
    if (c.status !== 'awaiting_driver') {
      throw new ApiError(409, 'already_answered', 'This request has already been answered');
    }
    const status = response === 'accept' ? 'active' : 'declined';
    await client.query(
      `update commutes set status = $2, responded_at = $3, decline_reason = $4 where id = $1`,
      [commuteId, status, ctx.clock(), response === 'decline' ? 'declined' : null],
    );
    await client.query('commit');
    // The first rides exist before the rider next opens the app.
    if (status === 'active') await createRides(ctx, commuteId);
    return { id: commuteId, status };
  } catch (e) {
    await client.query('rollback');
    throw e;
  } finally {
    client.release();
  }
}

/** Commutes waiting for an answer, oldest first. Every driver's when [driverId] is null (simulator). */
export async function pendingRequests(ctx: AppContext, driverId: string | null) {
  await expireUnanswered(ctx);
  const rows = (
    await ctx.db.query(
      `select c.id, c.driver_id, c.pickup, c.drop_off, c.weekdays, c.pickup_time, c.return_trip, c.return_time,
              to_char(c.start_date, 'YYYY-MM-DD') as start_date, c.fare, c.usual_payment, c.respond_by, c.ride_type,
              r.name as rider_name
       from commutes c join riders r on r.id = c.rider_id
       where c.status = 'awaiting_driver' and ($1::text is null or c.driver_id = $1)
       order by c.created_at`,
      [driverId],
    )
  ).rows;
  return rows.map((r) => ({
    id: r.id,
    driverId: r.driver_id,
    // Riders have no name until R5; the driver sees a neutral label.
    riderName: r.rider_name ?? 'New rider',
    pickup: r.pickup,
    drop: r.drop_off,
    weekdays: r.weekdays,
    pickupTime: r.pickup_time,
    returnTrip: r.return_trip,
    returnTime: r.return_time,
    startDate: r.start_date,
    rideType: r.ride_type,
    fare: r.fare,
    usualPayment: r.usual_payment,
    respondBy: (r.respond_by as Date).toISOString(),
  }));
}

const idParam = z.object({ id: z.string().uuid() });

export function driverRequestRoutes(app: FastifyInstance, ctx: AppContext) {
  app.get('/v1/driver/requests', async (req) => {
    const driverId = await requireDriver(ctx, req);
    return { requests: await pendingRequests(ctx, driverId) };
  });

  app.post('/v1/driver/commutes/:id/accept', async (req) => {
    const driverId = await requireDriver(ctx, req);
    return respondToCommute(ctx, driverId, idParam.parse(req.params).id, 'accept');
  });

  app.post('/v1/driver/commutes/:id/decline', async (req) => {
    const driverId = await requireDriver(ctx, req);
    return respondToCommute(ctx, driverId, idParam.parse(req.params).id, 'decline');
  });
}
