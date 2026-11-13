import type pg from 'pg';
import { estimateTrip, type TripEstimate } from '../domain/trips.js';
import { ApiError, type Place } from '../domain/types.js';

/**
 * Place search and road trip estimates. The demo city (seeded places,
 * straight line × 1.85) is the default; Ola Maps takes over when
 * OLA_MAPS_API_KEY is set. Live ride ETAs (R2) stay on the straight-line
 * estimate: they're asked for every 5 seconds per ride.
 */
export interface MapsProvider {
  readonly name: string;
  searchPlaces(query: string, city: CityArea): Promise<Place[]>;
  estimateTrip(from: Pick<Place, 'lat' | 'lng'>, to: Pick<Place, 'lat' | 'lng'>): Promise<TripEstimate>;
}

export interface CityArea {
  code: string;
  /** Results are biased towards this point and limited to [radiusKm] of it. */
  lat: number;
  lng: number;
  radiusKm: number;
}

/** Bengaluru: the only city until multi-city sign-up exists. */
export const cityAreas: Record<string, CityArea> = {
  blr: { code: 'blr', lat: 12.9716, lng: 77.5946, radiusKm: 45 },
};

/** Seeded places and the straight-line estimate. Used in demo, dev and tests. */
export function demoMaps(db: Pick<pg.Pool, 'query'>): MapsProvider {
  return {
    name: 'demo',
    async searchPlaces(query, city) {
      const like = `%${query.trim().replace(/[%_\\]/g, (c) => `\\${c}`)}%`;
      const rows = (
        await db.query<Place>(
          `select id, title, detail, lat, lng from places
           where city = $1 and (title ilike $2 or detail ilike $2)
           order by title limit 20`,
          [city.code, like],
        )
      ).rows;
      return rows;
    },
    async estimateTrip(from, to) {
      return estimateTrip(from, to);
    },
  };
}

type Fetch = typeof fetch;

/**
 * Ola Maps (api.olamaps.io): Places Autocomplete for search, Distance
 * Matrix for road distance and drive time.
 *
 * Written against Ola's public API documentation; check it against a live
 * key before relying on it (see the README's go-live checklist).
 */
export function olaMaps(apiKey: string, opts: { fetch?: Fetch; timeoutMs?: number } = {}): MapsProvider {
  const http = opts.fetch ?? fetch;
  const base = 'https://api.olamaps.io';
  const get = async (path: string, params: Record<string, string>) => {
    const url = new URL(path, base);
    for (const [k, v] of Object.entries(params)) url.searchParams.set(k, v);
    url.searchParams.set('api_key', apiKey);
    let res: Response;
    try {
      res = await http(url, { signal: AbortSignal.timeout(opts.timeoutMs ?? 8000), headers: { 'X-Request-Id': crypto.randomUUID() } });
    } catch {
      throw new ApiError(502, 'maps_unavailable', "Maps aren't responding. Try again in a moment.");
    }
    if (!res.ok) throw new ApiError(502, 'maps_unavailable', "Maps aren't responding. Try again in a moment.");
    return (await res.json()) as Record<string, unknown>;
  };

  return {
    name: 'ola',
    async searchPlaces(query, city) {
      if (query.trim().length < 2) return [];
      const body = await get('/places/v1/autocomplete', {
        input: query.trim(),
        location: `${city.lat},${city.lng}`,
        radius: String(city.radiusKm * 1000),
        strictbounds: 'true',
      });
      const predictions = (body.predictions as Array<Record<string, any>> | undefined) ?? [];
      const places: Place[] = [];
      for (const p of predictions) {
        const loc = p.geometry?.location;
        if (typeof loc?.lat !== 'number' || typeof loc?.lng !== 'number') continue;
        const main: string = p.structured_formatting?.main_text ?? String(p.description ?? '').split(',')[0] ?? '';
        const secondary: string = p.structured_formatting?.secondary_text ?? '';
        places.push({
          id: String(p.place_id ?? p.reference ?? `${loc.lat},${loc.lng}`),
          title: main.trim() || String(p.description),
          // Keep the area short, like the demo places: "Koramangala", not a full address.
          detail: secondary.split(',').slice(0, 2).join(',').trim(),
          lat: loc.lat,
          lng: loc.lng,
        });
      }
      return places.slice(0, 20);
    },
    async estimateTrip(from, to) {
      const body = await get('/routing/v1/distanceMatrix', {
        origins: `${from.lat},${from.lng}`,
        destinations: `${to.lat},${to.lng}`,
      });
      const el = (
        body.rows as Array<{ elements?: Array<{ distance?: number; duration?: number; status?: string; polyline?: string }> }> | undefined
      )?.[0]?.elements?.[0];
      if (!el || typeof el.distance !== 'number' || typeof el.duration !== 'number') {
        // No route (e.g. across water): fall back to the straight-line estimate.
        return estimateTrip(from, to);
      }
      return {
        distanceKm: Math.round(el.distance / 100) / 10,
        minutes: Math.max(5, Math.round(el.duration / 60)),
        ...(typeof el.polyline === 'string' && el.polyline ? { polyline: el.polyline } : {}),
      };
    },
  };
}
