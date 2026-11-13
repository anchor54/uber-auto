import type { FastifyInstance } from 'fastify';
import type { AppContext } from '../context.js';
import { ApiError } from '../domain/types.js';

type Fetch = typeof fetch;

/**
 * The map the app draws: Ola Maps' vector style, trimmed and served through
 * this backend so the Ola key never reaches the phone.
 *
 *   - Only the base map is kept: Ola's own data source (EV chargers and 3D
 *     models) and its unused street overlay are dropped, so a screen loads
 *     one set of tiles instead of three and shows no advertising.
 *   - Tiles, fonts and the sprite are fetched here with the key and passed
 *     on. Tiles and fonts are cached (most recent first, up to a size
 *     limit): riders in one city view much the same tiles, so Ola sees far
 *     fewer requests. Phones cache them for a day too.
 *
 * Check Ola Maps' terms on caching before relying on the server cache at
 * scale; it can be turned off with MAP_TILE_CACHE_MB=0.
 */
export interface MapStyle {
  /** The style; [publicBase] is this API's URL as the phone sees it. */
  style(publicBase: string): Promise<Record<string, unknown>>;
  sprite(suffix: SpriteSuffix): Promise<Asset>;
  tile(source: string, z: number, x: number, y: number): Promise<Asset>;
  font(fontstack: string, range: string): Promise<Asset>;
}

export interface Asset {
  body: Buffer;
  contentType: string;
}

export const spriteSuffixes = ['.json', '.png', '@2x.json', '@2x.png'] as const;
export type SpriteSuffix = (typeof spriteSuffixes)[number];

/** Ola style sources the app doesn't use (chargers, 3D models, a hidden overlay). */
const DROPPED_SOURCES = new Set(['vectordata', 'street']);
/** Layers the map never reaches (it stays at zoom 15 or below). */
const DROPPED_LAYER_TYPES = new Set(['fill-extrusion']);
/** The app's map doesn't zoom past this, so neither does the tile proxy. */
export const MAX_ZOOM = 15;
const STYLE_CACHE_MS = 6 * 3_600_000;

/** Least recently used cache, bounded by total bytes. */
class ByteLru {
  private items = new Map<string, Asset>();
  private bytes = 0;
  constructor(private readonly maxBytes: number) {}
  get(key: string): Asset | undefined {
    const v = this.items.get(key);
    if (v) {
      this.items.delete(key);
      this.items.set(key, v);
    }
    return v;
  }
  set(key: string, v: Asset) {
    if (v.body.length > this.maxBytes) return;
    this.items.set(key, v);
    this.bytes += v.body.length;
    for (const [k, old] of this.items) {
      if (this.bytes <= this.maxBytes) break;
      this.items.delete(k);
      this.bytes -= old.body.length;
    }
  }
  get size() {
    return this.items.size;
  }
}

export function olaMapStyle(
  apiKey: string,
  opts: { styleName?: string; fetch?: Fetch; now?: () => number; cacheMb?: number } = {},
): MapStyle {
  const http = opts.fetch ?? fetch;
  const now = opts.now ?? Date.now;
  const styleName = opts.styleName ?? 'default-light-standard';
  const base = 'https://api.olamaps.io/tiles/vector/v1';
  const withKey = (url: string) => `${url}${url.includes('?') ? '&' : '?'}api_key=${encodeURIComponent(apiKey)}`;
  const cache = new ByteLru((opts.cacheMb ?? 64) * 1024 * 1024);
  let cached: { at: number; style: Record<string, any>; tiles: Map<string, { url: string; maxzoom: number }> } | undefined;

  const fail = () => new ApiError(502, 'map_unavailable', "The map isn't loading right now.");

  const get = async (url: string): Promise<Response> => {
    let res: Response;
    try {
      res = await http(withKey(url), { signal: AbortSignal.timeout(10_000) });
    } catch {
      throw fail();
    }
    if (!res.ok) throw fail();
    return res;
  };

  /** Ola's style, trimmed, with the tile URL template of each kept source. Cached for 6 hours. */
  const load = async () => {
    if (cached && now() - cached.at < STYLE_CACHE_MS) return cached;
    const style = (await (await get(`${base}/styles/${styleName}/style.json`)).json()) as Record<string, any>;
    const tiles = new Map<string, { url: string; maxzoom: number }>();
    const sources: Record<string, unknown> = {};
    for (const [name, src] of Object.entries((style.sources ?? {}) as Record<string, any>)) {
      if (DROPPED_SOURCES.has(name)) continue;
      if (src?.type === 'vector' && typeof src.url === 'string') {
        const tilejson = (await (await get(src.url)).json()) as Record<string, any>;
        tiles.set(name, { url: (tilejson.tiles as string[])[0]!, maxzoom: Math.min(tilejson.maxzoom ?? 14, MAX_ZOOM) });
        sources[name] = {
          type: 'vector',
          minzoom: tilejson.minzoom ?? 0,
          maxzoom: Math.min(tilejson.maxzoom ?? 14, MAX_ZOOM),
          ...(tilejson.attribution ? { attribution: tilejson.attribution } : {}),
        };
      } else {
        sources[name] = src;
      }
    }
    const layers = ((style.layers ?? []) as Array<Record<string, any>>).filter(
      (l) => !DROPPED_SOURCES.has(l.source) && !DROPPED_LAYER_TYPES.has(l.type),
    );
    cached = { at: now(), style: { ...style, sources, layers }, tiles };
    return cached;
  };

  const cachedAsset = async (key: string, url: string, contentType: string): Promise<Asset> => {
    const hit = cache.get(key);
    if (hit) return hit;
    const asset = { body: Buffer.from(await (await get(url)).arrayBuffer()), contentType };
    cache.set(key, asset);
    return asset;
  };

  return {
    async style(publicBase) {
      const { style } = await load();
      const sources: Record<string, unknown> = {};
      for (const [name, src] of Object.entries(style.sources as Record<string, any>)) {
        sources[name] = src.type === 'vector' ? { ...src, tiles: [`${publicBase}/v1/map/tiles/${name}/{z}/{x}/{y}.pbf`] } : src;
      }
      return {
        ...style,
        sources,
        sprite: `${publicBase}/v1/map/sprite`,
        glyphs: `${publicBase}/v1/map/fonts/{fontstack}/{range}.pbf`,
      };
    },
    async sprite(suffix) {
      return cachedAsset(`sprite${suffix}`, `${base}/styles/${styleName}/sprite${suffix}`, suffix.endsWith('.png') ? 'image/png' : 'application/json');
    },
    async tile(source, z, x, y) {
      const { tiles } = await load();
      const t = tiles.get(source);
      const n = 2 ** z;
      if (!t || !Number.isInteger(z) || z < 0 || z > t.maxzoom || !Number.isInteger(x) || !Number.isInteger(y) || x < 0 || y < 0 || x >= n || y >= n) {
        throw new ApiError(404, 'no_tile', 'No such tile');
      }
      const url = t.url.replace('{z}', String(z)).replace('{x}', String(x)).replace('{y}', String(y));
      return cachedAsset(`t/${source}/${z}/${x}/${y}`, url, 'application/x-protobuf');
    },
    async font(fontstack, range) {
      if (!/^[\w ,.-]{1,200}$/.test(fontstack) || !/^\d{1,5}-\d{1,5}$/.test(range)) throw new ApiError(404, 'no_font', 'No such font');
      return cachedAsset(`f/${fontstack}/${range}`, `${base}/fonts/${encodeURIComponent(fontstack)}/${range}.pbf`, 'application/x-protobuf');
    },
  };
}

/**
 * GET /v1/map/style.json, /v1/map/sprite{.json,.png,@2x.json,@2x.png},
 * /v1/map/tiles/:source/:z/:x/:y.pbf and /v1/map/fonts/:fontstack/:range.pbf.
 * Open (no session): map views fetch these without our auth header, and they
 * hold nothing about any rider. 404 when no maps provider is configured: the
 * app then draws its simple map.
 */
export function mapStyleRoutes(app: FastifyInstance, ctx: AppContext) {
  const style = () => {
    if (!ctx.mapStyle) throw new ApiError(404, 'no_map', 'No map provider is configured');
    return ctx.mapStyle;
  };
  const send = (reply: import('fastify').FastifyReply, a: Asset, maxAge: number) =>
    reply.header('content-type', a.contentType).header('cache-control', `public, max-age=${maxAge}`).send(a.body);

  app.get('/v1/map/style.json', async (req, reply) => {
    const proto = (req.headers['x-forwarded-proto'] as string | undefined)?.split(',')[0] ?? req.protocol;
    // Short: map views cache the style by URL, so a changed style must not linger.
    return reply.header('cache-control', 'public, max-age=600').send(await style().style(`${proto}://${req.headers.host}`));
  });

  for (const suffix of spriteSuffixes) {
    app.get(`/v1/map/sprite${suffix}`, async (_req, reply) => send(reply, await style().sprite(suffix), 86_400));
  }

  app.get<{ Params: { source: string; z: string; x: string; file: string } }>('/v1/map/tiles/:source/:z/:x/:file', async (req, reply) => {
    const m = /^(\d+)\.pbf$/.exec(req.params.file);
    if (!m) throw new ApiError(404, 'no_tile', 'No such tile');
    return send(reply, await style().tile(req.params.source, Number(req.params.z), Number(req.params.x), Number(m[1])), 86_400);
  });

  app.get<{ Params: { fontstack: string; file: string } }>('/v1/map/fonts/:fontstack/:file', async (req, reply) => {
    const m = /^(\d+-\d+)\.pbf$/.exec(req.params.file);
    if (!m) throw new ApiError(404, 'no_font', 'No such font');
    return send(reply, await style().font(req.params.fontstack, m[1]!), 7 * 86_400);
  });
}
