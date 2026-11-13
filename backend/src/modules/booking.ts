import type { FastifyInstance } from 'fastify';
import { z } from 'zod';
import { cityArea, mapsOf, type AppContext } from '../context.js';
import { haversineKm, suggestedFare, type FareRule } from '../domain/trips.js';
import { bookingFlows, placeSchema, type BookingFlow, type Place, type RideType } from '../domain/types.js';
import { requireRider } from './auth.js';

export interface RideTypeOption {
  type: RideType;
  fare: number;
  driversOnRoute: number;
}

export interface BookingOptions {
  flow: BookingFlow;
  defaultType: RideType | null;
  options: RideTypeOption[];
}

export async function fareRules(ctx: AppContext): Promise<Map<RideType, FareRule>> {
  const rows = (
    await ctx.db.query<{ ride_type: RideType; base: number; per_km: number; minimum: number }>(
      'select ride_type, base, per_km, minimum from fare_rules where city = $1',
      [ctx.config.city],
    )
  ).rows;
  return new Map(rows.map((r) => [r.ride_type, { base: r.base, perKm: r.per_km, minimum: r.minimum }]));
}

/** Ride types the config allows for [flow] in the rider's city, each with its fare. */
export async function bookingOptions(
  ctx: AppContext,
  flow: BookingFlow,
  pickup: Place,
  drop: Place,
  pickupTime: number,
): Promise<BookingOptions> {
  const cfg = (
    await ctx.db.query<{ allowed: RideType[]; default_type: RideType | null }>(
      'select allowed, default_type from ride_type_config where city = $1 and flow = $2',
      [ctx.config.city, flow],
    )
  ).rows[0];
  if (!cfg) return { flow, defaultType: null, options: [] };
  const rules = await fareRules(ctx);
  const trip = await mapsOf(ctx).estimateTrip(pickup, drop);
  const drivers = (
    await ctx.db.query<{ ride_type: RideType; base_lat: number; base_lng: number; service_radius_km: string; work_start: number; work_end: number }>(
      'select ride_type, base_lat, base_lng, service_radius_km, work_start, work_end from drivers where city = $1 and active',
      [ctx.config.city],
    )
  ).rows;
  const options: RideTypeOption[] = [];
  for (const type of cfg.allowed) {
    const rule = rules.get(type);
    if (!rule) continue; // A type with no fare rule can't be offered.
    const driversOnRoute = drivers.filter(
      (d) =>
        d.ride_type === type &&
        haversineKm(d.base_lat, d.base_lng, pickup.lat, pickup.lng) <= Number(d.service_radius_km) &&
        pickupTime >= d.work_start &&
        pickupTime + trip.minutes <= d.work_end,
    ).length;
    options.push({ type, fare: suggestedFare(rule, trip.distanceKm), driversOnRoute });
  }
  const defaultType = cfg.default_type && options.some((o) => o.type === cfg.default_type) ? cfg.default_type : (options[0]?.type ?? null);
  return { flow, defaultType, options };
}

export function bookingRoutes(app: FastifyInstance, ctx: AppContext) {
  app.get('/v1/places', async (req) => {
    await requireRider(ctx, req);
    const { q } = z.object({ q: z.string().max(100).default('') }).parse(req.query);
    return { places: await mapsOf(ctx).searchPlaces(q, cityArea(ctx)) };
  });

  app.post('/v1/trips/estimate', async (req) => {
    await requireRider(ctx, req);
    const { pickup, drop } = z.object({ pickup: placeSchema, drop: placeSchema }).parse(req.body);
    return mapsOf(ctx).estimateTrip(pickup, drop);
  });

  app.post('/v1/booking-options', async (req) => {
    await requireRider(ctx, req);
    const body = z
      .object({
        flow: z.enum(bookingFlows),
        pickup: placeSchema,
        drop: placeSchema,
        pickupTime: z.number().int().min(0).max(1439),
      })
      .parse(req.body);
    return bookingOptions(ctx, body.flow, body.pickup, body.drop, body.pickupTime);
  });
}
