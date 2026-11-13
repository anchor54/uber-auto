/** Which ride records a commute needs over a range of dates. Pure. */
import { isoWeekday } from './schedule.js';
import { WINDOW_MINUTES } from './rides.js';
import type { Place } from './types.js';

export const CREATE_DAYS_AHEAD = 14;

export interface PlannedRide {
  rideDate: string; // YYYY-MM-DD, local
  direction: 'outbound' | 'return';
  pickup: Place;
  drop: Place;
  pickupAt: Date;
  windowStart: Date;
  windowEnd: Date;
  /** On a holiday the ride is created already skipped. */
  skipped: boolean;
}

export interface CommuteForPlan {
  pickup: Place;
  drop: Place;
  weekdays: number[];
  pickupTime: number;
  returnTrip: boolean;
  returnTime: number;
  startDate: string;
}

const DAY = 86_400_000;
const MIN = 60_000;

export function addDays(date: string, days: number): string {
  return new Date(Date.parse(`${date}T00:00:00Z`) + days * DAY).toISOString().slice(0, 10);
}

/** The instant of [minuteOfDay] on local [date], given the zone's offset. */
export function localInstant(date: string, minuteOfDay: number, utcOffsetMinutes: number): Date {
  return new Date(Date.parse(`${date}T00:00:00Z`) + (minuteOfDay - utcOffsetMinutes) * MIN);
}

/**
 * Rides for every chosen weekday from max([from], start date) to [to]
 * inclusive: the outbound leg, and the return leg (pickup and drop swapped)
 * when there is one. Legs whose pickup is already past [now] are left out.
 */
export function planRides(
  c: CommuteForPlan,
  opts: { from: string; to: string; now: Date; utcOffsetMinutes: number; holidays: Set<string> },
): PlannedRide[] {
  const out: PlannedRide[] = [];
  let day = c.startDate > opts.from ? c.startDate : opts.from;
  while (day <= opts.to) {
    if (c.weekdays.includes(isoWeekday(new Date(`${day}T00:00:00Z`)))) {
      const legs: Array<['outbound' | 'return', number, Place, Place]> = [['outbound', c.pickupTime, c.pickup, c.drop]];
      if (c.returnTrip) legs.push(['return', c.returnTime, c.drop, c.pickup]);
      for (const [direction, minute, pickup, drop] of legs) {
        const pickupAt = localInstant(day, minute, opts.utcOffsetMinutes);
        if (pickupAt <= opts.now) continue;
        out.push({
          rideDate: day,
          direction,
          pickup,
          drop,
          pickupAt,
          windowStart: new Date(pickupAt.getTime() - WINDOW_MINUTES * MIN),
          windowEnd: new Date(pickupAt.getTime() + WINDOW_MINUTES * MIN),
          skipped: opts.holidays.has(day),
        });
      }
    }
    day = addDays(day, 1);
  }
  return out;
}
