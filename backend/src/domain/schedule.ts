/** Date and leg-timing rules shared by booking and matching. */

/** ISO weekday: 1 = Monday … 7 = Sunday. */
export function isoWeekday(d: Date): number {
  const w = d.getUTCDay();
  return w === 0 ? 7 : w;
}

/** "YYYY-MM-DD" of a calendar date in the rider's local time zone. */
export function localDateString(now: Date, utcOffsetMinutes: number): string {
  return new Date(now.getTime() + utcOffsetMinutes * 60_000).toISOString().slice(0, 10);
}

/** The first day after [today] whose weekday is in [weekdays]. Rides never start on the booking day. */
export function firstRideDate(today: string, weekdays: number[]): string {
  const d = new Date(`${today}T00:00:00Z`);
  for (let i = 1; i <= 7; i++) {
    const c = new Date(d.getTime() + i * 86_400_000);
    if (weekdays.includes(isoWeekday(c))) return c.toISOString().slice(0, 10);
  }
  throw new Error('weekdays must not be empty');
}

/** Pickup for an arrive-by commute: trip time plus a 10-minute buffer, rounded down to 5 minutes. */
export function pickupForArrival(arriveBy: number, tripMinutes: number): number {
  const raw = arriveBy - tripMinutes - 10;
  return raw - (((raw % 5) + 5) % 5);
}

export interface Leg {
  weekday: number;
  start: number; // minutes after midnight
  end: number;
}

/** Every leg a commute occupies in a week. */
export function weeklyLegs(c: { weekdays: number[]; pickupTime: number; returnTrip: boolean; returnTime: number; tripMinutes: number }): Leg[] {
  const legs: Leg[] = [];
  for (const weekday of c.weekdays) {
    legs.push({ weekday, start: c.pickupTime, end: c.pickupTime + c.tripMinutes });
    if (c.returnTrip) legs.push({ weekday, start: c.returnTime, end: c.returnTime + c.tripMinutes });
  }
  return legs;
}

/** Minimum time between one drop and the next pickup for the same driver. */
export const LEG_GAP_MINUTES = 20;

export function legsClash(a: Leg, b: Leg, gap = LEG_GAP_MINUTES): boolean {
  return a.weekday === b.weekday && a.start < b.end + gap && b.start < a.end + gap;
}
