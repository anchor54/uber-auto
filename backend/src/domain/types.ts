import { z } from 'zod';

export const rideTypes = ['bike', 'auto', 'cab_hatchback', 'cab_sedan', 'cab_suv'] as const;
export type RideType = (typeof rideTypes)[number];

export const bookingFlows = ['one_time', 'recurring_weekday', 'recurring_weekly'] as const;
export type BookingFlow = (typeof bookingFlows)[number];

export const preferences = ['ac_on', 'quiet', 'woman_driver'] as const;
export type Preference = (typeof preferences)[number];

export const paymentMethods = ['upi', 'cash'] as const;
export type PaymentMethod = (typeof paymentMethods)[number];

export const placeSchema = z.object({
  id: z.string().min(1),
  title: z.string().min(1),
  detail: z.string().default(''),
  lat: z.number().min(-90).max(90),
  lng: z.number().min(-180).max(180),
});
export type Place = z.infer<typeof placeSchema>;

const minuteOfDay = z.number().int().min(0).max(1439);

/** What the rider app sends after R1.2 and R1.3. */
export const commuteRequestSchema = z
  .object({
    pickup: placeSchema,
    drop: placeSchema,
    repeat: z.enum(['every_weekday', 'chosen_days']),
    weekdays: z.array(z.number().int().min(1).max(7)).min(1).max(7),
    timing: z.enum(['pickup_at', 'arrive_by']),
    time: minuteOfDay,
    returnTrip: z.boolean(),
    returnTime: minuteOfDay,
    rideType: z.enum(rideTypes),
    preferences: z.array(z.enum(preferences)).default([]),
  })
  .refine((r) => r.pickup.id !== r.drop.id, { message: "Pickup and drop-off can't be the same place", path: ['drop'] })
  .refine((r) => new Set(r.weekdays).size === r.weekdays.length, { message: 'Weekdays must be unique', path: ['weekdays'] })
  .refine((r) => r.repeat === 'chosen_days' || sameSet(r.weekdays, [1, 2, 3, 4, 5]), {
    message: 'Every-weekday commutes run Monday to Friday',
    path: ['weekdays'],
  });
export type CommuteRequest = z.infer<typeof commuteRequestSchema>;

function sameSet(a: number[], b: number[]) {
  return a.length === b.length && b.every((x) => a.includes(x));
}

export function flowFor(repeat: CommuteRequest['repeat']): BookingFlow {
  return repeat === 'every_weekday' ? 'recurring_weekday' : 'recurring_weekly';
}

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
  ) {
    super(message);
  }
}
