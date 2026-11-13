import type { AppContext } from '../context.js';
import { alongPath, decodePolyline, type LatLng } from '../domain/polyline.js';
import { ApiError } from '../domain/types.js';
import { applyMove, loadRide, logContact, recordLocation, refreshApproachRoute, setDuty, type RideRow } from './rides.js';

/**
 * Dev-only driver for R2 rides. Every move goes through the same service
 * functions the driver endpoints call (applyMove, recordLocation, …), so a
 * simulated ride obeys the same rules a real one does.
 */

export const simMoves = ['depart', 'arrive', 'start', 'end', 'no_show', 'contact'] as const;
export type SimMove = (typeof simMoves)[number];

const asDriver = (r: RideRow) => ({ type: 'driver' as const, id: r.driver_id });

async function at(ctx: AppContext, driverId: string, lat: number, lng: number) {
  await recordLocation(ctx, driverId, [{ lat, lng, heading: null, speed: null, at: ctx.clock().toISOString() }]);
}

/** One move, with whatever a real driver would have done first (go on duty, drive to the pickup, …). */
export async function simStep(ctx: AppContext, rideId: string, move: SimMove): Promise<RideRow> {
  const r = await loadRide(ctx.db, rideId);
  if (!r) throw new ApiError(404, 'ride_not_found', 'No such ride');
  switch (move) {
    case 'depart':
      await setDuty(ctx, r.driver_id, true);
      return applyMove(ctx, r.id, 'depart', asDriver(r));
    case 'arrive':
      await at(ctx, r.driver_id, r.pickup.lat, r.pickup.lng);
      return applyMove(ctx, r.id, 'arrive', asDriver(r));
    case 'start':
      return applyMove(ctx, r.id, 'start', asDriver(r), { pin: r.pin });
    case 'end':
      await at(ctx, r.driver_id, r.drop_off.lat, r.drop_off.lng);
      return applyMove(ctx, r.id, 'end', asDriver(r), { lat: r.drop_off.lat, lng: r.drop_off.lng });
    case 'contact':
      await logContact(ctx, r.id, r.driver_id, 'call');
      return (await loadRide(ctx.db, r.id))!;
    case 'no_show':
      await logContact(ctx, r.id, r.driver_id, 'call');
      return applyMove(ctx, r.id, 'no_show', asDriver(r));
  }
}

const playing = new Set<string>();

export interface PlayOptions {
  /** 1 = real time. 60 plays a 6-minute approach in 6 seconds. */
  speed: number;
  /** Minutes the driver takes to reach the pickup. */
  approachMinutes?: number;
  /** Minutes the driver waits at the pickup before the PIN start. */
  waitMinutes?: number;
  sleep?: (ms: number) => Promise<void>;
}

/**
 * Plays a whole ride: depart from about 2 km out, drive to the pickup
 * sending a position every 5 simulated seconds, arrive, start with the real
 * PIN, drive to the drop-off, end. Returns at once; runs in the background.
 */
export function simPlay(ctx: AppContext, rideId: string, opts: PlayOptions, log: (m: string) => void = console.error) {
  if (playing.has(rideId)) throw new ApiError(409, 'already_playing', 'This ride is already being played');
  playing.add(rideId);
  const run = playRide(ctx, rideId, opts)
    .catch((e) => log(`[sim] ride ${rideId} stopped: ${(e as Error).message}`))
    .finally(() => playing.delete(rideId));
  return run;
}

async function playRide(ctx: AppContext, rideId: string, o: PlayOptions) {
  const sleep = o.sleep ?? ((ms: number) => new Promise((r) => setTimeout(r, ms)));
  const tick = 5_000 / o.speed; // a position every 5 simulated seconds
  const r = (await loadRide(ctx.db, rideId))!;
  const start = { lat: r.pickup.lat + 0.018, lng: r.pickup.lng }; // ~2 km north
  await at(ctx, r.driver_id, start.lat, start.lng);
  await simStep(ctx, rideId, 'depart');

  /** Moves along [path] (or straight from its first to last point) over [minutes]. */
  const drive = async (path: LatLng[], minutes: number) => {
    const point = alongPath(path);
    const steps = Math.max(2, Math.round((minutes * 60) / 5));
    for (let i = 1; i <= steps; i++) {
      await sleep(tick);
      const p = point(i / steps);
      await at(ctx, r.driver_id, p.lat, p.lng);
    }
  };

  // The same road route the rider's map shows (asked for at departure), so
  // the marker and the line agree; a straight line in the demo city.
  await refreshApproachRoute(ctx, (await loadRide(ctx.db, rideId))!, start, { force: true });
  const departed = (await loadRide(ctx.db, rideId))!;
  await drive(routeOf(departed.approach_route, start, r.pickup), o.approachMinutes ?? 6);
  await simStep(ctx, rideId, 'arrive');
  await sleep(((o.waitMinutes ?? 1) * 60_000) / o.speed);
  await simStep(ctx, rideId, 'start');
  await drive(routeOf(r.route, r.pickup, r.drop_off), r.trip_minutes);
  await simStep(ctx, rideId, 'end');
}

/** The ride's stored road route, or the straight line when it has none. */
export function routeOf(encoded: string | null, from: LatLng, to: LatLng): LatLng[] {
  if (encoded) {
    try {
      const path = decodePolyline(encoded);
      if (path.length >= 2) return path;
    } catch {
      // A damaged route: drive straight.
    }
  }
  return [from, to];
}
