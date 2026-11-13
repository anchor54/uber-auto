import { describe, expect, it } from 'vitest';
import {
  isLate,
  etaMinutes,
  moves,
  paymentStatus,
  rideStates,
  transition,
  waitEndsAt,
  type Move,
  type MoveContext,
  type RideFacts,
  type RideState,
} from '../../src/domain/rides.js';
import { ApiError } from '../../src/domain/types.js';

/** Every move in the R2 transition table, allowed and refused. Pure: no database. */

const pickupAt = new Date('2026-10-22T03:10:00Z'); // Thu 8:40 IST
const MIN = 60_000;
const t = (minutesFromPickup: number) => new Date(pickupAt.getTime() + minutesFromPickup * MIN);

function ride(state: RideState, extra: Partial<RideFacts> = {}): RideFacts {
  return {
    state,
    pickupAt,
    windowStart: t(-5),
    windowEnd: t(5),
    arrivedAt: null,
    waitEndsAt: null,
    riderLateUsed: false,
    pin: '4821',
    pinTries: 0,
    lastActivityAt: t(0),
    ...extra,
  };
}

/** A context in which [move] is allowed from its usual state. */
function okContext(move: Move): MoveContext {
  const base = { now: t(0) };
  switch (move) {
    case 'depart':
      return { ...base, now: t(-20), actor: 'driver', driverOnDuty: true, driverBusy: false };
    case 'arrive':
      return { ...base, actor: 'driver', metresToPickup: 120 };
    case 'start':
      return { ...base, actor: 'driver', pin: '4821' };
    case 'end':
      return { ...base, actor: 'driver', metresToDrop: 80 };
    case 'no_show':
      return { ...base, now: t(10), actor: 'driver', contactLogged: true };
    case 'skip':
      return { ...base, now: t(-600), actor: 'rider' };
    case 'cancel':
      return { ...base, actor: 'ops' };
    case 'close_stale':
      return { ...base, now: t(200), actor: 'system' };
  }
}

const expected: Record<Move, { from: RideState[]; to: RideState }> = {
  depart: { from: ['scheduled'], to: 'on_the_way' },
  arrive: { from: ['on_the_way'], to: 'arrived' },
  start: { from: ['arrived'], to: 'in_ride' },
  end: { from: ['in_ride'], to: 'completed' },
  no_show: { from: ['arrived'], to: 'no_show' },
  skip: { from: ['scheduled'], to: 'skipped' },
  cancel: { from: ['scheduled', 'on_the_way'], to: 'cancelled' },
  close_stale: { from: ['on_the_way', 'arrived', 'in_ride'], to: 'completed' },
};

function code(f: () => unknown): string | undefined {
  try {
    f();
  } catch (e) {
    return (e as ApiError).code;
  }
  return undefined;
}

describe('the transition table', () => {
  for (const move of moves) {
    for (const state of rideStates) {
      const allowed = expected[move].from.includes(state);
      it(`${move} from ${state} is ${allowed ? 'allowed' : 'refused'}`, () => {
        const facts = ride(state, { waitEndsAt: t(5) });
        if (allowed) {
          const r = transition(facts, move, okContext(move));
          const to = move === 'close_stale' && state !== 'in_ride' ? 'cancelled' : expected[move].to;
          expect(r.to).toBe(to);
        } else {
          expect(code(() => transition(facts, move, okContext(move)))).toBe('invalid_transition');
        }
      });
    }
  }

  it('only the right actor can make each move', () => {
    expect(code(() => transition(ride('scheduled'), 'depart', { ...okContext('depart'), actor: 'rider' }))).toBe('not_allowed');
    expect(code(() => transition(ride('scheduled'), 'skip', { ...okContext('skip'), actor: 'driver' }))).toBe('not_allowed');
    expect(code(() => transition(ride('on_the_way'), 'close_stale', { ...okContext('close_stale'), actor: 'driver' }))).toBe('not_allowed');
  });
});

describe('depart', () => {
  it('needs the driver on duty and not busy with another ride', () => {
    expect(code(() => transition(ride('scheduled'), 'depart', { ...okContext('depart'), driverOnDuty: false }))).toBe('off_duty');
    expect(code(() => transition(ride('scheduled'), 'depart', { ...okContext('depart'), driverBusy: true }))).toBe('driver_busy');
  });

  it('opens 90 minutes before pickup', () => {
    expect(code(() => transition(ride('scheduled'), 'depart', { ...okContext('depart'), now: t(-91) }))).toBe('too_early');
    expect(transition(ride('scheduled'), 'depart', { ...okContext('depart'), now: t(-90) }).to).toBe('on_the_way');
  });
});

describe('arrive and the wait', () => {
  it('needs the last position within 300 m of the pickup', () => {
    expect(code(() => transition(ride('on_the_way'), 'arrive', { ...okContext('arrive'), metresToPickup: 301 }))).toBe('too_far');
    expect(code(() => transition(ride('on_the_way'), 'arrive', { ...okContext('arrive'), metresToPickup: null }))).toBe('too_far');
  });

  it('an early driver does not start the wait early', () => {
    // Arrives 8:28; the window opens 8:35, so the wait ends 8:40.
    const r = transition(ride('on_the_way'), 'arrive', { ...okContext('arrive'), now: t(-12) });
    expect(r.set.wait_ends_at).toEqual(t(0));
  });

  it('on time: 5 minutes from arrival', () => {
    expect(waitEndsAt(t(-2), t(-5), false)).toEqual(t(3)); // the no-show example: 8:38 → 8:43
  });

  it('late arrival: 5 minutes from arrival', () => {
    expect(waitEndsAt(t(12), t(-5), false)).toEqual(t(17));
  });

  it('"I\'m 5 min late" makes it 10', () => {
    expect(waitEndsAt(t(-2), t(-5), true)).toEqual(t(8)); // 8:48
    const r = transition(ride('on_the_way', { riderLateUsed: true }), 'arrive', { ...okContext('arrive'), now: t(-2) });
    expect(r.set.wait_ends_at).toEqual(t(8));
  });
});

describe('start with the PIN', () => {
  it('a wrong PIN says how many tries are left, then locks', () => {
    const wrong = { ...okContext('start'), pin: '0000' };
    expect(code(() => transition(ride('arrived', { pinTries: 0 }), 'start', wrong))).toBe('wrong_pin');
    expect(code(() => transition(ride('arrived', { pinTries: 1 }), 'start', wrong))).toBe('wrong_pin');
    expect(code(() => transition(ride('arrived', { pinTries: 2 }), 'start', wrong))).toBe('pin_locked');
  });

  it('after three wrong tries even the right PIN is refused', () => {
    expect(code(() => transition(ride('arrived', { pinTries: 3 }), 'start', okContext('start')))).toBe('pin_locked');
  });
});

describe('end', () => {
  it('away from the drop-off needs a reason', () => {
    expect(code(() => transition(ride('in_ride'), 'end', { ...okContext('end'), metresToDrop: 900 }))).toBe('too_far');
    const r = transition(ride('in_ride'), 'end', { ...okContext('end'), metresToDrop: 900, endReason: 'Rider asked to stop early' });
    expect(r).toMatchObject({ to: 'completed', set: { end_reason: 'Rider asked to stop early' } });
  });
});

describe('no-show', () => {
  const waiting = ride('arrived', { waitEndsAt: t(3) });
  it('not before the wait ends', () => {
    expect(code(() => transition(waiting, 'no_show', { ...okContext('no_show'), now: t(2) }))).toBe('wait_not_over');
  });
  it('not without a logged call or alert', () => {
    expect(code(() => transition(waiting, 'no_show', { ...okContext('no_show'), now: t(3), contactLogged: false }))).toBe('no_contact_logged');
  });
  it('at the end of the wait, after contact', () => {
    expect(transition(waiting, 'no_show', { ...okContext('no_show'), now: t(3) }).to).toBe('no_show');
  });
});

describe('close stale', () => {
  it('waits for 3 hours of nothing', () => {
    expect(code(() => transition(ride('on_the_way'), 'close_stale', { ...okContext('close_stale'), now: t(179) }))).toBe('not_stale');
  });
  it('a started ride is completed; one that never started is cancelled', () => {
    expect(transition(ride('in_ride'), 'close_stale', okContext('close_stale')).to).toBe('completed');
    expect(transition(ride('arrived'), 'close_stale', okContext('close_stale')).to).toBe('cancelled');
  });
});

describe('payment status from the two marks', () => {
  it.each([
    ['upi', 'cash', 'paid'],
    ['cash', 'cash', 'paid'],
    [null, 'upi', 'paid'],
    ['upi', null, 'paid_by_rider'],
    [null, 'not_paid', 'unpaid'],
    ['cash', 'not_paid', 'disputed'],
    [null, null, 'unmarked'],
  ] as const)('rider %s, driver %s → %s', (rider, driver, status) => {
    expect(paymentStatus(rider, driver)).toBe(status);
  });
});

describe('ETA and the late flag', () => {
  it('straight line × 1.85 at 22 km/h', () => {
    expect(etaMinutes(1)).toBe(5); // 1.85 km at 22 km/h ≈ 5 min
    expect(etaMinutes(0)).toBe(1);
  });

  it('the doc example: at 8:36 an ETA of 15 minutes means late', () => {
    expect(isLate(t(-4), 15, t(5))).toBe(true); // 8:51 > 8:45 + 5
    expect(isLate(t(-4), 14, t(5))).toBe(false); // 8:50 is not more than 5 past
  });
});
