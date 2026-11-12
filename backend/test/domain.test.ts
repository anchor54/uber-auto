import { describe, expect, it } from 'vitest';
import { firstRideDate, legsClash, localDateString, pickupForArrival, weeklyLegs } from '../src/domain/schedule.js';
import { defaultFareRules, estimateTrip, suggestedFare } from '../src/domain/trips.js';
import { commuteRequestSchema } from '../src/domain/types.js';
import { commuteRequest, place, testNow } from './helpers.js';

describe('trips and fares (must match the rider app)', () => {
  it('demo commute is 11.9 km and 38 minutes', () => {
    expect(estimateTrip(place('home'), place('etv'))).toEqual({ distanceKm: 11.9, minutes: 38 });
  });

  it('fares per ride type match the app', () => {
    const fares = Object.fromEntries(Object.entries(defaultFareRules).map(([t, r]) => [t, suggestedFare(r, 11.9)]));
    expect(fares).toEqual({ bike: 90, auto: 160, cab_hatchback: 215, cab_sedan: 250, cab_suv: 330 });
  });

  it('short trips pay the minimum', () => {
    expect(suggestedFare(defaultFareRules.cab_sedan, 1)).toBe(110);
  });
});

describe('schedule', () => {
  it('uses the rider local date (IST)', () => {
    expect(localDateString(testNow, 330)).toBe('2026-10-21');
    // 11:30 PM IST on the 21st is still the 21st locally, though 18:00 UTC.
    expect(localDateString(new Date('2026-10-21T18:00:00Z'), 330)).toBe('2026-10-21');
    expect(localDateString(new Date('2026-10-21T18:31:00Z'), 330)).toBe('2026-10-22');
  });

  it('first ride is the next matching day, never today', () => {
    expect(firstRideDate('2026-10-21', [1, 2, 3, 4, 5])).toBe('2026-10-22');
    expect(firstRideDate('2026-10-23', [1, 2, 3, 4, 5])).toBe('2026-10-26');
    expect(firstRideDate('2026-10-21', [6])).toBe('2026-10-24');
    expect(firstRideDate('2026-10-21', [3])).toBe('2026-10-28');
  });

  it('arrive-by pickup rounds down to five minutes', () => {
    expect(pickupForArrival(570, 38)).toBe(520);
  });

  it('legs clash when they overlap or leave less than 20 minutes between', () => {
    const a = { weekday: 1, start: 520, end: 558 };
    expect(legsClash(a, { weekday: 1, start: 560, end: 600 })).toBe(true);
    expect(legsClash(a, { weekday: 1, start: 578, end: 600 })).toBe(false);
    expect(legsClash(a, { weekday: 2, start: 520, end: 558 })).toBe(false);
  });

  it('a weekday commute with a return trip is ten legs a week', () => {
    expect(weeklyLegs({ weekdays: [1, 2, 3, 4, 5], pickupTime: 520, returnTrip: true, returnTime: 1110, tripMinutes: 38 })).toHaveLength(10);
  });
});

describe('commute request validation', () => {
  it('accepts the demo request', () => {
    expect(commuteRequestSchema.safeParse(commuteRequest()).success).toBe(true);
  });

  it('rejects the same pickup and drop-off', () => {
    const r = commuteRequestSchema.safeParse(commuteRequest({ drop: place('home') }));
    expect(r.success).toBe(false);
  });

  it('rejects an every-weekday commute that is not Monday to Friday', () => {
    expect(commuteRequestSchema.safeParse(commuteRequest({ weekdays: [1, 2, 3] })).success).toBe(false);
    expect(commuteRequestSchema.safeParse(commuteRequest({ repeat: 'chosen_days', weekdays: [1, 2, 3] })).success).toBe(true);
  });

  it('rejects no days and repeated days', () => {
    expect(commuteRequestSchema.safeParse(commuteRequest({ repeat: 'chosen_days', weekdays: [] })).success).toBe(false);
    expect(commuteRequestSchema.safeParse(commuteRequest({ repeat: 'chosen_days', weekdays: [6, 6] })).success).toBe(false);
  });
});
