import { describe, expect, it } from 'vitest';
import { planRides, type CommuteForPlan } from '../../src/domain/ride_plan.js';

const home = { id: 'home', title: 'Home', detail: '', lat: 12.9, lng: 77.6 };
const office = { id: 'etv', title: 'Office', detail: '', lat: 12.94, lng: 77.69 };

const weekday: CommuteForPlan = {
  pickup: home,
  drop: office,
  weekdays: [1, 2, 3, 4, 5],
  pickupTime: 520, // 8:40
  returnTrip: true,
  returnTime: 1110, // 18:30
  startDate: '2026-10-22',
};

const opts = (extra: Partial<Parameters<typeof planRides>[1]> = {}) => ({
  from: '2026-10-21',
  to: '2026-11-04',
  now: new Date('2026-10-21T03:30:00Z'), // Wed 9:00 IST
  utcOffsetMinutes: 330,
  holidays: new Set<string>(),
  ...extra,
});

describe('ride creation plan', () => {
  it('two legs per weekday from the start date, 14 days ahead', () => {
    const plan = planRides(weekday, opts());
    // Thu 22 Oct to Wed 4 Nov: 10 weekdays × 2 legs.
    expect(plan).toHaveLength(20);
    expect(plan[0]).toMatchObject({ rideDate: '2026-10-22', direction: 'outbound', pickup: home, drop: office });
    expect(plan[0]!.pickupAt.toISOString()).toBe('2026-10-22T03:10:00.000Z'); // 8:40 IST
    expect(plan[0]!.windowStart.toISOString()).toBe('2026-10-22T03:05:00.000Z');
    expect(plan[0]!.windowEnd.toISOString()).toBe('2026-10-22T03:15:00.000Z');
    expect(plan[1]).toMatchObject({ rideDate: '2026-10-22', direction: 'return', pickup: office, drop: home });
    expect(plan.map((p) => p.rideDate)).not.toContain('2026-10-24'); // Saturday
  });

  it('a start date mid-fortnight starts there', () => {
    const plan = planRides({ ...weekday, startDate: '2026-10-29' }, opts());
    expect(plan[0]!.rideDate).toBe('2026-10-29');
    expect(plan).toHaveLength(10);
  });

  it('a holiday is created already skipped', () => {
    const plan = planRides(weekday, opts({ holidays: new Set(['2026-10-23']) }));
    expect(plan.filter((p) => p.skipped).map((p) => [p.rideDate, p.direction])).toEqual([
      ['2026-10-23', 'outbound'],
      ['2026-10-23', 'return'],
    ]);
  });

  it('legs already past today are left out', () => {
    // Thursday 12:00 IST: the 8:40 leg is gone, the 18:30 return remains.
    const plan = planRides(weekday, opts({ from: '2026-10-22', now: new Date('2026-10-22T06:30:00Z') }));
    expect(plan[0]).toMatchObject({ rideDate: '2026-10-22', direction: 'return' });
  });

  it('one way, Saturdays only', () => {
    const plan = planRides({ ...weekday, weekdays: [6], returnTrip: false }, opts());
    expect(plan.map((p) => p.rideDate)).toEqual(['2026-10-24', '2026-10-31']);
  });

  it('is the same every time it runs', () => {
    expect(planRides(weekday, opts())).toEqual(planRides(weekday, opts()));
  });
});
