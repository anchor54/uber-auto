/**
 * Flow R2 ride lifecycle, as pure functions: no database, no clock of its own.
 *
 *   scheduled → on_the_way → arrived → in_ride → completed
 *   exits: no_show (from arrived), skipped (from scheduled),
 *          cancelled (from scheduled or on_the_way); stale rides are closed.
 *
 * The endpoints, the worker and the simulator all go through [transition],
 * so the rules live in one place. Anything it refuses is an ApiError.
 */
import { randomInt } from 'node:crypto';
import { ApiError } from './types.js';

export const rideStates = ['scheduled', 'on_the_way', 'arrived', 'in_ride', 'completed', 'no_show', 'skipped', 'cancelled'] as const;
export type RideState = (typeof rideStates)[number];

export const moves = ['depart', 'arrive', 'start', 'end', 'no_show', 'skip', 'cancel', 'close_stale'] as const;
export type Move = (typeof moves)[number];

export type ActorType = 'rider' | 'driver' | 'system' | 'ops';

/** The fields of a ride the rules read. */
export interface RideFacts {
  state: RideState;
  pickupAt: Date;
  windowStart: Date;
  windowEnd: Date;
  arrivedAt: Date | null;
  waitEndsAt: Date | null;
  riderLateUsed: boolean;
  pin: string;
  pinTries: number;
  lastActivityAt: Date;
}

/** What the caller looked up so the rules can stay pure. */
export interface MoveContext {
  now: Date;
  actor: ActorType;
  /** depart: the driver is on duty. */
  driverOnDuty?: boolean;
  /** depart: the driver already has another ride on the way, arrived or in ride. */
  driverBusy?: boolean;
  /** arrive: metres from the driver's last position to the pickup, or null if none sent. */
  metresToPickup?: number | null;
  /** start: the PIN the driver typed. */
  pin?: string;
  /** end: metres from where the driver ended to the drop-off. */
  metresToDrop?: number;
  /** end: why the driver ended away from the drop-off. */
  endReason?: string;
  /** no_show: the driver called or alerted the rider at least once. */
  contactLogged?: boolean;
}

/** The changes a move makes. The caller writes them and a ride_events row. */
export interface MoveResult {
  to: RideState;
  set: Partial<{
    departed_at: Date;
    arrived_at: Date;
    started_at: Date;
    ended_at: Date;
    closed_at: Date;
    wait_ends_at: Date;
    end_reason: string;
  }>;
}

export const PIN_TRIES = 3;
export const WINDOW_MINUTES = 5;
export const WAIT_MINUTES = 5;
export const RIDER_LATE_EXTRA_MINUTES = 5;
export const DEPART_BEFORE_MINUTES = 90;
export const ARRIVE_WITHIN_METRES = 300;
export const END_WITHIN_METRES = 500;
export const STALE_AFTER_HOURS = 3;
/** ETA this far past the window's end flags the driver as late. */
export const LATE_AFTER_WINDOW_MINUTES = 5;

const MIN = 60_000;

/** Who may make each move. */
const allowedActors: Record<Move, ActorType[]> = {
  depart: ['driver'],
  arrive: ['driver'],
  start: ['driver'],
  end: ['driver'],
  no_show: ['driver'],
  skip: ['rider', 'system'],
  cancel: ['ops', 'system'],
  close_stale: ['system'],
};

/** Which states each move leaves from. */
const fromStates: Record<Move, RideState[]> = {
  depart: ['scheduled'],
  arrive: ['on_the_way'],
  start: ['arrived'],
  end: ['in_ride'],
  no_show: ['arrived'],
  skip: ['scheduled'],
  cancel: ['scheduled', 'on_the_way'],
  close_stale: ['on_the_way', 'arrived', 'in_ride'],
};

export function invalidTransition(state: RideState, move: Move): ApiError {
  const e = new ApiError(409, 'invalid_transition', `Can't ${move.replace('_', ' ')} a ride that is ${state.replace(/_/g, ' ')}`);
  (e as ApiError & { state?: RideState }).state = state;
  return e;
}

/**
 * The rider's wait: 5 minutes after the later of arrival and the window
 * opening (an early driver doesn't start it early), 10 if the rider said
 * they're running late.
 */
export function waitEndsAt(arrivedAt: Date, windowStart: Date, riderLateUsed: boolean): Date {
  const from = Math.max(arrivedAt.getTime(), windowStart.getTime());
  return new Date(from + (WAIT_MINUTES + (riderLateUsed ? RIDER_LATE_EXTRA_MINUTES : 0)) * MIN);
}

/** Thrown for a wrong PIN; the caller records the try before throwing. */
export class WrongPin extends ApiError {
  constructor(public readonly triesLeft: number) {
    super(
      triesLeft > 0 ? 422 : 423,
      triesLeft > 0 ? 'wrong_pin' : 'pin_locked',
      triesLeft > 0
        ? `That PIN isn't right. ${triesLeft} ${triesLeft === 1 ? 'try' : 'tries'} left.`
        : 'Too many wrong PINs. Call support to start this ride.',
    );
  }
}

/** Applies [move] to [ride], or throws why it isn't allowed. */
export function transition(ride: RideFacts, move: Move, c: MoveContext): MoveResult {
  if (!allowedActors[move].includes(c.actor)) {
    throw new ApiError(403, 'not_allowed', `A ${c.actor} can't ${move.replace('_', ' ')} a ride`);
  }
  if (!fromStates[move].includes(ride.state)) throw invalidTransition(ride.state, move);
  const now = c.now;

  switch (move) {
    case 'depart': {
      if (!c.driverOnDuty) throw new ApiError(409, 'off_duty', 'Go on duty before starting navigation');
      if (c.driverBusy) throw new ApiError(409, 'driver_busy', 'Finish your current ride first');
      if (now.getTime() < ride.pickupAt.getTime() - DEPART_BEFORE_MINUTES * MIN) {
        throw new ApiError(409, 'too_early', `You can start navigation ${DEPART_BEFORE_MINUTES} minutes before pickup`);
      }
      return { to: 'on_the_way', set: { departed_at: now } };
    }
    case 'arrive': {
      if (c.metresToPickup == null || c.metresToPickup > ARRIVE_WITHIN_METRES) {
        throw new ApiError(422, 'too_far', `Mark arrived within ${ARRIVE_WITHIN_METRES} m of the pickup point`);
      }
      return {
        to: 'arrived',
        set: { arrived_at: now, wait_ends_at: waitEndsAt(now, ride.windowStart, ride.riderLateUsed) },
      };
    }
    case 'start': {
      if (ride.pinTries >= PIN_TRIES) throw new WrongPin(0);
      if (c.pin !== ride.pin) throw new WrongPin(PIN_TRIES - ride.pinTries - 1);
      return { to: 'in_ride', set: { started_at: now } };
    }
    case 'end': {
      const far = (c.metresToDrop ?? Infinity) > END_WITHIN_METRES;
      const reason = c.endReason?.trim();
      if (far && !reason) {
        throw new ApiError(422, 'too_far', `You're more than ${END_WITHIN_METRES} m from the drop-off. Say why you're ending here.`);
      }
      return { to: 'completed', set: { ended_at: now, ...(far ? { end_reason: reason } : {}) } };
    }
    case 'no_show': {
      if (!ride.waitEndsAt || now < ride.waitEndsAt) {
        throw new ApiError(425, 'wait_not_over', 'The rider still has time. Wait until the countdown ends.');
      }
      if (!c.contactLogged) throw new ApiError(409, 'no_contact_logged', 'Call or alert the rider before marking a no-show');
      return { to: 'no_show', set: { closed_at: now } };
    }
    case 'skip':
      return { to: 'skipped', set: { closed_at: now } };
    case 'cancel':
      return { to: 'cancelled', set: { closed_at: now } };
    case 'close_stale': {
      if (now.getTime() - ride.lastActivityAt.getTime() < STALE_AFTER_HOURS * 3_600_000) {
        throw new ApiError(409, 'not_stale', 'The ride is still active');
      }
      // A ride that started is assumed done; one that never started didn't happen.
      return ride.state === 'in_ride'
        ? { to: 'completed', set: { ended_at: now, closed_at: now, end_reason: 'closed_stale' } }
        : { to: 'cancelled', set: { closed_at: now } };
    }
  }
}

// ---- payment status, worked out from the two marks and never stored ----

export type RiderMark = 'upi' | 'cash' | null;
export type DriverMark = 'upi' | 'cash' | 'not_paid' | null;
export type PaymentStatus = 'paid' | 'paid_by_rider' | 'unpaid' | 'disputed' | 'unmarked';

/** Paid if either side says so, unless the driver says not paid yet. */
export function paymentStatus(rider: RiderMark, driver: DriverMark): PaymentStatus {
  if (driver === 'not_paid') return rider ? 'disputed' : 'unpaid';
  if (driver) return 'paid';
  if (rider) return 'paid_by_rider';
  return 'unmarked';
}

// ---- ETA and lateness ----

/** Until a maps provider: straight line × 1.85 at 22 km/h. */
export const ETA_ROAD_FACTOR = 1.85;
export const ETA_KMH = 22;

export function etaMinutes(straightLineKm: number): number {
  return Math.max(1, Math.round(((straightLineKm * ETA_ROAD_FACTOR) / ETA_KMH) * 60));
}

/** Late when now + ETA lands more than 5 minutes after the window ends. */
export function isLate(now: Date, etaMins: number, windowEnd: Date): boolean {
  return now.getTime() + etaMins * MIN > windowEnd.getTime() + LATE_AFTER_WINDOW_MINUTES * MIN;
}

/** Four random digits from a secure source, made when the ride is created. */
export function newPin(): string {
  return String(randomInt(0, 10_000)).padStart(4, '0');
}
