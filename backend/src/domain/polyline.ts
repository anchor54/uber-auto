import { haversineKm } from './trips.js';

export interface LatLng {
  lat: number;
  lng: number;
}

/** Decodes an encoded polyline (Google's format, precision 5), as Ola Maps returns routes. */
export function decodePolyline(encoded: string, precision = 5): LatLng[] {
  const factor = 10 ** precision;
  const out: LatLng[] = [];
  let index = 0;
  let lat = 0;
  let lng = 0;
  const next = () => {
    let shift = 0;
    let result = 0;
    let b: number;
    do {
      if (index >= encoded.length) throw new Error('Polyline ends mid-value');
      b = encoded.charCodeAt(index++) - 63;
      result |= (b & 0x1f) << shift;
      shift += 5;
    } while (b >= 0x20);
    return result & 1 ? ~(result >> 1) : result >> 1;
  };
  while (index < encoded.length) {
    lat += next();
    lng += next();
    out.push({ lat: lat / factor, lng: lng / factor });
  }
  return out;
}

/**
 * A function giving the point [f] of the way along [path] (0 = start, 1 = end),
 * by distance, so a simulated car moves at an even speed along the roads.
 */
export function alongPath(path: LatLng[]): (f: number) => LatLng {
  if (path.length === 0) throw new Error('Empty path');
  const cum = [0];
  for (let i = 1; i < path.length; i++) {
    cum.push(cum[i - 1]! + haversineKm(path[i - 1]!.lat, path[i - 1]!.lng, path[i]!.lat, path[i]!.lng));
  }
  const total = cum.at(-1)!;
  return (f) => {
    if (total === 0 || f <= 0) return path[0]!;
    if (f >= 1) return path.at(-1)!;
    const target = f * total;
    let i = 1;
    while (cum[i]! < target) i++;
    const seg = cum[i]! - cum[i - 1]!;
    const t = seg === 0 ? 0 : (target - cum[i - 1]!) / seg;
    const a = path[i - 1]!;
    const b = path[i]!;
    return { lat: a.lat + (b.lat - a.lat) * t, lng: a.lng + (b.lng - a.lng) * t };
  };
}

/** Shortest distance in km from [p] to the path (its segments, not just its points). */
export function distanceToPathKm(p: LatLng, path: LatLng[]): number {
  if (path.length === 0) return Infinity;
  if (path.length === 1) return haversineKm(p.lat, p.lng, path[0]!.lat, path[0]!.lng);
  // Flat projection around p: plenty accurate over a city.
  const kx = 111.32 * Math.cos((p.lat * Math.PI) / 180);
  const ky = 110.57;
  let best = Infinity;
  for (let i = 1; i < path.length; i++) {
    const ax = (path[i - 1]!.lng - p.lng) * kx;
    const ay = (path[i - 1]!.lat - p.lat) * ky;
    const bx = (path[i]!.lng - p.lng) * kx;
    const by = (path[i]!.lat - p.lat) * ky;
    const dx = bx - ax;
    const dy = by - ay;
    const len2 = dx * dx + dy * dy;
    const t = len2 === 0 ? 0 : Math.max(0, Math.min(1, -(ax * dx + ay * dy) / len2));
    best = Math.min(best, Math.hypot(ax + t * dx, ay + t * dy));
  }
  return best;
}
