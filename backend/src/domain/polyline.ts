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
