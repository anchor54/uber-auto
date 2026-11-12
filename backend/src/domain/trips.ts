import type { Place, RideType } from './types.js';

export interface FareRule {
  base: number;
  perKm: number;
  minimum: number;
}

export interface TripEstimate {
  distanceKm: number;
  minutes: number;
}

/** Straight-line distance in km. */
export function haversineKm(lat1: number, lng1: number, lat2: number, lng2: number): number {
  const r = 6371;
  const rad = (d: number) => (d * Math.PI) / 180;
  const dLat = rad(lat2 - lat1);
  const dLng = rad(lng2 - lng1);
  const a = Math.sin(dLat / 2) ** 2 + Math.cos(rad(lat1)) * Math.cos(rad(lat2)) * Math.sin(dLng / 2) ** 2;
  return 2 * r * Math.asin(Math.sqrt(a));
}

/**
 * Road trip estimate until a maps provider is wired in: roads run ~1.85x
 * the straight line in Bengaluru at ~19 km/h average commute speed.
 * Must match the rider app's demo backend (lib/core/fares.dart).
 */
export function estimateTrip(a: Pick<Place, 'lat' | 'lng'>, b: Pick<Place, 'lat' | 'lng'>): TripEstimate {
  const km = haversineKm(a.lat, a.lng, b.lat, b.lng) * 1.85;
  return { distanceKm: Math.round(km * 10) / 10, minutes: Math.max(5, Math.round((km / 19) * 60)) };
}

/** Suggested fare per ride in rupees, rounded to the nearest ₹5. */
export function suggestedFare(rule: FareRule, distanceKm: number): number {
  const raw = Math.max(rule.minimum, rule.base + rule.perKm * distanceKm);
  return Math.round(raw / 5) * 5;
}

export const defaultFareRules: Record<RideType, FareRule> = {
  bike: { base: 20, perKm: 6, minimum: 35 },
  auto: { base: 30, perKm: 11, minimum: 40 },
  cab_hatchback: { base: 50, perKm: 14, minimum: 90 },
  cab_sedan: { base: 60, perKm: 16, minimum: 110 },
  cab_suv: { base: 80, perKm: 21, minimum: 150 },
};
