import type { AppContext } from '../context.js';
import { etaMinutes, paymentStatus, STALE_AFTER_HOURS } from '../domain/rides.js';
import { localDateString } from '../domain/schedule.js';
import { haversineKm } from '../domain/trips.js';
import { queueNotification, sendDueNotifications, type PushSender } from './notify.js';
import { applyMove, createRides, flagIfLate, localTimeLabel, type RideRow } from './rides.js';

/**
 * The worker's jobs (src/worker.ts runs them every 30 seconds). Each one is
 * safe to run twice or from two workers at once: inserts are unique,
 * updates check the state they expect, and sends claim rows with
 * FOR UPDATE SKIP LOCKED.
 */

const MIN = 60_000;
export const NIGHTLY_AT_MINUTE = 30; // 00:30 local
export const LEG_IS_ON_BEFORE_MINUTES = 30;
export const PAYMENT_REMINDER_AFTER_MINUTES = 120;

/** 1. Create rides 14 days ahead, once a night after 00:30 local. */
export async function createRidesNightly(ctx: AppContext): Promise<number> {
  const now = ctx.clock();
  const local = new Date(now.getTime() + ctx.config.utcOffsetMinutes * MIN);
  if (local.getUTCHours() * 60 + local.getUTCMinutes() < NIGHTLY_AT_MINUTE) return 0;
  const today = localDateString(now, ctx.config.utcOffsetMinutes);
  // Claim tonight's run; a second worker gets no row back and skips.
  const claimed = await ctx.db.query(
    `insert into job_runs (name, last_run_on) values ('create_rides', $1)
     on conflict (name) do update set last_run_on = excluded.last_run_on where job_runs.last_run_on < excluded.last_run_on
     returning name`,
    [today],
  );
  return claimed.rowCount ? createRides(ctx) : 0;
}

async function driverFirstName(ctx: AppContext, driverId: string) {
  const d = (await ctx.db.query<{ name: string }>('select name from drivers where id = $1', [driverId])).rows[0];
  return d?.name.split(' ')[0] ?? 'Your driver';
}

/** 2. "Leg is on" for scheduled rides 30 minutes from pickup. */
export async function queueLegIsOn(ctx: AppContext): Promise<number> {
  const now = ctx.clock();
  const rides = (
    await ctx.db.query<RideRow>(
      `select r.* from rides r where r.state = 'scheduled' and r.pickup_at > $1 and r.pickup_at <= $2
         and not exists (select 1 from notifications n where n.kind = 'leg_is_on' and n.ride_id = r.id)`,
      [now, new Date(now.getTime() + LEG_IS_ON_BEFORE_MINUTES * MIN)],
    )
  ).rows;
  for (const r of rides) {
    await queueNotification(ctx.db, {
      to: { type: 'rider', id: r.rider_id },
      kind: 'leg_is_on',
      rideId: r.id,
      sendAfter: now,
      payload: { driverFirstName: await driverFirstName(ctx, r.driver_id), pickupTime: localTimeLabel(r.pickup_at, ctx.config.utcOffsetMinutes) },
    });
  }
  return rides.length;
}

/**
 * 3. Flag late drivers. ETA comes from the driver's latest position; a
 * driver who hasn't sent one is late once the window has been over for
 * 5 minutes.
 */
export async function flagLateDrivers(ctx: AppContext): Promise<number> {
  const now = ctx.clock();
  const rides = (
    await ctx.db.query<RideRow & { lat: number | null; lng: number | null }>(
      `select r.*, l.lat, l.lng from rides r left join driver_locations l on l.driver_id = r.driver_id
       where r.state in ('scheduled', 'on_the_way') and r.late_flagged_at is null and r.pickup_at <= $1`,
      [new Date(now.getTime() + 90 * MIN)],
    )
  ).rows;
  let flagged = 0;
  for (const r of rides) {
    const eta = r.lat != null && r.lng != null ? etaMinutes(haversineKm(r.lat, r.lng, r.pickup.lat, r.pickup.lng)) : 0;
    if (await flagIfLate(ctx, r, eta)) flagged++;
  }
  return flagged;
}

/** 4. Two hours after drop, remind whichever side hasn't marked payment. Disputes go to ops instead. */
export async function queuePaymentReminders(ctx: AppContext): Promise<number> {
  const now = ctx.clock();
  const rides = (
    await ctx.db.query<RideRow>(
      `select r.* from rides r where r.state = 'completed' and r.ended_at <= $1 and r.ended_at > $2`,
      [new Date(now.getTime() - PAYMENT_REMINDER_AFTER_MINUTES * MIN), new Date(now.getTime() - 7 * 24 * 60 * MIN)],
    )
  ).rows;
  let queued = 0;
  for (const r of rides) {
    const status = paymentStatus(r.rider_payment, r.driver_payment);
    const to: Array<{ type: 'rider' | 'driver'; id: string }> = [];
    if (status === 'unmarked') to.push({ type: 'rider', id: r.rider_id }, { type: 'driver', id: r.driver_id });
    if (status === 'paid_by_rider') to.push({ type: 'driver', id: r.driver_id });
    if (status === 'unpaid') to.push({ type: 'rider', id: r.rider_id });
    for (const recipient of to) {
      await queueNotification(ctx.db, { to: recipient, kind: 'payment_reminder', rideId: r.id, sendAfter: now, payload: { status } });
      queued++;
    }
  }
  return queued;
}

/** 5. Close rides nothing has happened to for 3 hours. */
export async function closeStaleRides(ctx: AppContext): Promise<number> {
  const cutoff = new Date(ctx.clock().getTime() - STALE_AFTER_HOURS * 60 * MIN);
  const stale = (
    await ctx.db.query<{ id: string }>(
      `select id from rides where state in ('on_the_way', 'arrived', 'in_ride')
         and greatest(departed_at, arrived_at, started_at) < $1`,
      [cutoff],
    )
  ).rows;
  let closed = 0;
  for (const { id } of stale) {
    try {
      await applyMove(ctx, id, 'close_stale', { type: 'system', id: null });
      closed++;
    } catch {
      // Moved on since we looked (e.g. the driver ended it): nothing to do.
    }
  }
  return closed;
}

/** Runs every job once. Returns what each one did, for the log. */
export async function runJobs(ctx: AppContext, sender: PushSender) {
  return {
    ridesCreated: await createRidesNightly(ctx),
    legIsOn: await queueLegIsOn(ctx),
    lateFlagged: await flagLateDrivers(ctx),
    paymentReminders: await queuePaymentReminders(ctx),
    staleClosed: await closeStaleRides(ctx),
    sent: await sendDueNotifications(ctx, sender),
  };
}

/** Runs [runJobs] every [everyMs] until the returned stop function is called. */
export interface JobLog {
  info(message: string): void;
  error(message: string): void;
}

export function startJobLoop(ctx: AppContext, sender: PushSender, everyMs: number, log: JobLog = console) {
  let stopped = false;
  let timer: NodeJS.Timeout | undefined;
  const tick = async () => {
    try {
      const did = await runJobs(ctx, sender);
      if (Object.values(did).some((n) => n > 0)) log.info(`[jobs] ${JSON.stringify(did)}`);
    } catch (e) {
      log.error(`[jobs] run failed: ${(e as Error).stack ?? e}`);
    }
    if (!stopped) timer = setTimeout(tick, everyMs);
  };
  timer = setTimeout(tick, 0);
  return () => {
    stopped = true;
    clearTimeout(timer);
  };
}
