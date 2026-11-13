import { createHash } from 'node:crypto';
import { afterAll, afterEach, beforeEach, describe, expect, it } from 'vitest';
import { olaMapStyle } from '../src/modules/map_style.js';
import { olaMaps, cityAreas } from '../src/modules/maps.js';
import { msg91OtpSender } from '../src/modules/sms.js';
import { providersFromEnv } from '../src/providers.js';
import { bearer, closeDb, commuteRequest, DEMO_OTP, startTestApp, type TestApp } from './helpers.js';

/** Going live: SMS limits and delivery, the maps provider, CORS. */

const PHONE = '9886054321';
const SEC = 1000;

describe('sign-in code limits', () => {
  let t: TestApp;
  beforeEach(async () => {
    t = await startTestApp({ otpResendSeconds: 30, otpMaxSendsPerHour: 5 });
  });
  afterEach(async () => {
    await t.close();
  });

  const send = () => t.app.inject({ method: 'POST', url: '/v1/auth/otp', payload: { phone: PHONE } });
  const verify = (code: string) => t.app.inject({ method: 'POST', url: '/v1/auth/verify', payload: { phone: PHONE, code } });
  const later = (ms: number) => void (t.clock.now = new Date(t.clock.now.getTime() + ms));

  it('one code every 30 seconds per phone', async () => {
    expect((await send()).statusCode).toBe(204);
    const again = await send();
    expect([again.statusCode, again.json().error, again.json().message]).toEqual([429, 'resend_too_soon', 'Wait 30 seconds before asking for another code.']);
    later(29 * SEC);
    expect((await send()).json().message).toBe('Wait 1 seconds before asking for another code.');
    later(SEC);
    expect((await send()).statusCode).toBe(204);
  });

  it('at most 5 codes an hour; the hour then starts again', async () => {
    for (let i = 0; i < 5; i++) {
      expect((await send()).statusCode).toBe(204);
      later(31 * SEC);
    }
    expect((await send()).json().error).toBe('too_many_codes');
    t.clock.now = new Date(t.clock.now.getTime() + 60 * 60 * SEC);
    expect((await send()).statusCode).toBe(204);
    expect(t.sentCodes).toHaveLength(6);
  });

  it('wrong guesses count across resends, so a new code is not five new tries', async () => {
    await send();
    for (let i = 0; i < 3; i++) expect((await verify('000000')).json().error).toBe('invalid_code');
    later(31 * SEC);
    await send();
    for (let i = 0; i < 2; i++) expect((await verify('000000')).json().error).toBe('invalid_code');
    const locked = await verify(DEMO_OTP);
    expect([locked.statusCode, locked.json().error]).toEqual([429, 'too_many_attempts']);
  });

  it('codes are stored keyed with the server secret, not as a bare hash', async () => {
    await send();
    const row = (await t.db.query('select code_hash from otp_codes where phone = $1', [PHONE])).rows[0];
    expect(row.code_hash).not.toBe(createHash('sha256').update(DEMO_OTP).digest('hex'));
    expect((await verify(DEMO_OTP)).statusCode).toBe(200);
  });
});

/** A stand-in for fetch that records requests and answers from [reply]. */
function fakeFetch(reply: (url: URL) => { status?: number; body: unknown } | Error) {
  const calls: Array<{ url: URL; init: RequestInit | undefined }> = [];
  const fn = (async (input: URL | string, init?: RequestInit) => {
    const url = new URL(input.toString());
    calls.push({ url, init });
    const r = reply(url);
    if (r instanceof Error) throw r;
    return new Response(JSON.stringify(r.body), { status: r.status ?? 200, headers: { 'content-type': 'application/json' } });
  }) as typeof fetch;
  return { fn, calls };
}

describe('MSG91', () => {
  it('sends our code with the OTP template', async () => {
    const f = fakeFetch(() => ({ body: { type: 'success', request_id: 'abc' } }));
    await msg91OtpSender({ authKey: 'key-1', templateId: 'tmpl-1', fetch: f.fn }).send(PHONE, '482913');
    const { url, init } = f.calls[0]!;
    expect(`${url.origin}${url.pathname}`).toBe('https://control.msg91.com/api/v5/otp');
    expect(Object.fromEntries(url.searchParams)).toEqual({ template_id: 'tmpl-1', mobile: `91${PHONE}`, otp: '482913', otp_expiry: '5' });
    expect(init?.method).toBe('POST');
    expect((init?.headers as Record<string, string>).authkey).toBe('key-1');
  });

  it('a refusal or an outage is a 502 the app can show', async () => {
    for (const reply of [{ body: { type: 'error', message: 'Invalid template' } }, { status: 500, body: {} }, new Error('ECONNRESET')]) {
      const f = fakeFetch(() => reply);
      await expect(msg91OtpSender({ authKey: 'k', templateId: 't', fetch: f.fn }).send(PHONE, '1')).rejects.toMatchObject({
        status: 502,
        code: 'sms_failed',
      });
    }
  });
});

describe('Ola Maps', () => {
  const blr = cityAreas.blr!;

  it('place search maps autocomplete results to places in the city', async () => {
    const f = fakeFetch(() => ({
      body: {
        predictions: [
          {
            place_id: 'ola:123',
            description: 'Forum Mall, Hosur Road, Koramangala, Bengaluru, Karnataka',
            structured_formatting: { main_text: 'Forum Mall', secondary_text: 'Hosur Road, Koramangala, Bengaluru, Karnataka' },
            geometry: { location: { lat: 12.9346, lng: 77.6113 } },
          },
          { place_id: 'ola:no-geometry', description: 'Somewhere' },
        ],
        status: 'ok',
      },
    }));
    const places = await olaMaps('ola-key', { fetch: f.fn }).searchPlaces('forum', blr);
    expect(places).toEqual([{ id: 'ola:123', title: 'Forum Mall', detail: 'Hosur Road, Koramangala', lat: 12.9346, lng: 77.6113 }]);
    const q = Object.fromEntries(f.calls[0]!.url.searchParams);
    expect(f.calls[0]!.url.pathname).toBe('/places/v1/autocomplete');
    expect(q).toMatchObject({ input: 'forum', location: '12.9716,77.5946', radius: '45000', strictbounds: 'true', api_key: 'ola-key' });
  });

  it('a one-letter search asks nothing', async () => {
    const f = fakeFetch(() => ({ body: {} }));
    expect(await olaMaps('k', { fetch: f.fn }).searchPlaces('f', blr)).toEqual([]);
    expect(f.calls).toEqual([]);
  });

  it('trip estimate from the distance matrix, straight line when there is no route', async () => {
    const f = fakeFetch(() => ({ body: { rows: [{ elements: [{ distance: 14_230, duration: 2_710, status: 'OK' }] }] } }));
    const a = { lat: 12.9121, lng: 77.6446 };
    const b = { lat: 12.9406, lng: 77.6964 };
    expect(await olaMaps('k', { fetch: f.fn }).estimateTrip(a, b)).toEqual({ distanceKm: 14.2, minutes: 45 });
    expect(f.calls[0]!.url.pathname).toBe('/routing/v1/distanceMatrix');

    const none = fakeFetch(() => ({ body: { rows: [{ elements: [{ status: 'NO_ROUTE' }] }] } }));
    expect(await olaMaps('k', { fetch: none.fn }).estimateTrip(a, b)).toEqual({ distanceKm: 11.9, minutes: 38 });
  });

  it('an outage is a 502 the app can show', async () => {
    const f = fakeFetch(() => ({ status: 503, body: {} }));
    await expect(olaMaps('k', { fetch: f.fn }).searchPlaces('forum', blr)).rejects.toMatchObject({ code: 'maps_unavailable' });
  });
});

describe('providers from the environment', () => {
  const db = {} as never;
  it('production refuses to start without SMS', () => {
    expect(() => providersFromEnv({ NODE_ENV: 'production' }, db, () => {})).toThrow(/MSG91/);
  });
  it('picks MSG91 and Ola Maps when their keys are set', () => {
    const p = providersFromEnv({ NODE_ENV: 'production', MSG91_AUTH_KEY: 'a', MSG91_OTP_TEMPLATE_ID: 'b', OLA_MAPS_API_KEY: 'c' }, db);
    expect(p.maps.name).toBe('ola');
    expect(p.otpSender.send).toBeTypeOf('function');
  });
  it('MAPS_PROVIDER=demo keeps the demo city even with an Ola key', () => {
    expect(providersFromEnv({ OLA_MAPS_API_KEY: 'c', MAPS_PROVIDER: 'demo' }, db).maps.name).toBe('demo');
  });
  it('development uses logged codes and the demo city', () => {
    expect(providersFromEnv({}, db).maps.name).toBe('demo');
  });
});

describe('CORS for the web build', () => {
  let t: TestApp;
  beforeEach(async () => {
    t = await startTestApp({ corsOrigins: ['https://example.github.io'] });
  });
  afterEach(async () => {
    await t.close();
  });

  it('answers preflight for an allowed origin only', async () => {
    const ok = await t.app.inject({ method: 'OPTIONS', url: '/v1/home', headers: { origin: 'https://example.github.io' } });
    expect(ok.statusCode).toBe(204);
    expect(ok.headers['access-control-allow-origin']).toBe('https://example.github.io');
    expect(ok.headers['access-control-allow-headers']).toContain('authorization');
    const token = await t.signIn();
    const get = await t.app.inject({ method: 'GET', url: '/v1/home', headers: { origin: 'https://evil.example', ...bearer(token) } });
    expect(get.headers['access-control-allow-origin']).toBeUndefined();
  });
});

afterAll(async () => {
  await closeDb();
});

describe('seed:config on a live database', () => {
  it('adds what is missing, never deletes, and keeps ops changes', async () => {
    const { seedConfig } = await import('../src/db/seed_config.js');
    const t = await startTestApp();
    try {
      const rider = await t.signIn();
      await t.db.query(`update fare_rules set base = 99 where ride_type = 'bike'`);
      await t.db.query(`delete from places where id = 'airport'`);
      expect(await seedConfig(t.db, { demoDrivers: true })).toEqual({ fareRules: 0, rideTypeConfig: 0, places: 1, drivers: 0 });
      expect((await t.db.query(`select base from fare_rules where ride_type = 'bike'`)).rows[0].base).toBe(99);
      expect((await t.app.inject({ method: 'GET', url: '/v1/home', headers: bearer(rider) })).statusCode).toBe(200);
    } finally {
      await t.close();
    }
  });
});

describe('the map style for the app', () => {
  const olaStyle = {
    version: 8,
    sprite: 'https://api.olamaps.io/tiles/vector/v1/styles/default-light-standard/sprite',
    glyphs: 'https://api.olamaps.io/tiles/vector/v1/fonts/{fontstack}/{range}.pbf',
    sources: {
      openmaptiles: { type: 'vector', url: 'https://api.olamaps.io/tiles/vector/v1/data/planet.json' },
      vectordata: { type: 'vector', url: 'https://api.olamaps.io/tiles/vector/v1/data/vectordata.json' },
      street: { type: 'vector', url: 'https://api.olamaps.io/tiles/vector/v1/data/street.json' },
    },
    layers: [
      { id: 'bg', type: 'background' },
      { id: 'road', type: 'line', source: 'openmaptiles', 'source-layer': 'transportation' },
      { id: 'building-3d', type: 'fill-extrusion', source: 'openmaptiles', 'source-layer': 'building' },
      { id: 'ola-pois', type: 'symbol', source: 'vectordata', 'source-layer': 'hyperchargers' },
      { id: 'sli', type: 'line', source: 'street', 'source-layer': 'street' },
    ],
  };
  const tilejson = { tiles: ['https://api.olamaps.io/tiles/vector/v1/data/planet/{z}/{x}/{y}.pbf'], minzoom: 0, maxzoom: 14 };
  const ola = () =>
    fakeFetch((url) =>
      url.pathname.endsWith('style.json') ? { body: olaStyle } : url.pathname.endsWith('.json') ? { body: tilejson } : { body: { tile: url.pathname } },
    );

  it('keeps only the base map, serves everything through here, and never contains the key', async () => {
    const f = ola();
    const style = (await olaMapStyle('SECRET-KEY', { fetch: f.fn }).style('https://api.example.com')) as any;
    expect(JSON.stringify(style)).not.toContain('SECRET-KEY');
    expect(Object.keys(style.sources)).toEqual(['openmaptiles']);
    expect(style.sources.openmaptiles).toEqual({
      type: 'vector',
      minzoom: 0,
      maxzoom: 14,
      tiles: ['https://api.example.com/v1/map/tiles/openmaptiles/{z}/{x}/{y}.pbf'],
    });
    expect(style.layers.map((l: { id: string }) => l.id)).toEqual(['bg', 'road']);
    expect(style.glyphs).toBe('https://api.example.com/v1/map/fonts/{fontstack}/{range}.pbf');
    expect(style.sprite).toBe('https://api.example.com/v1/map/sprite');
    // Ola is asked with the key, and the dropped sources are never fetched.
    expect(f.calls.every((c) => c.url.searchParams.get('api_key') === 'SECRET-KEY')).toBe(true);
    expect(f.calls.map((c) => c.url.pathname)).toEqual([
      '/tiles/vector/v1/styles/default-light-standard/style.json',
      '/tiles/vector/v1/data/planet.json',
    ]);
  });

  it('the style is fetched from Ola once per 6 hours', async () => {
    let clock = 0;
    const f = ola();
    const ms = olaMapStyle('k', { fetch: f.fn, now: () => clock });
    await ms.style('https://a');
    await ms.style('https://a');
    expect(f.calls).toHaveLength(2);
    clock += 7 * 3_600_000;
    await ms.style('https://a');
    expect(f.calls).toHaveLength(4);
  });

  it('tiles come from Ola once, then from the cache; impossible tiles are refused', async () => {
    const f = ola();
    const ms = olaMapStyle('k', { fetch: f.fn });
    const a = await ms.tile('openmaptiles', 14, 11726, 7605);
    expect(a.contentType).toBe('application/x-protobuf');
    expect(JSON.parse(a.body.toString())).toEqual({ tile: '/tiles/vector/v1/data/planet/14/11726/7605.pbf' });
    const before = f.calls.length;
    await ms.tile('openmaptiles', 14, 11726, 7605);
    expect(f.calls.length).toBe(before);
    for (const [src, z, x, y] of [
      ['vectordata', 14, 1, 1],
      ['openmaptiles', 15, 1, 1], // past the source's max zoom
      ['openmaptiles', 2, 4, 0], // x out of range at z2
      ['openmaptiles', -1, 0, 0],
    ] as const) {
      await expect(ms.tile(src, z, x, y)).rejects.toMatchObject({ code: 'no_tile' });
    }
  });

  it('with the cache off, every tile goes to Ola', async () => {
    const f = ola();
    const ms = olaMapStyle('k', { fetch: f.fn, cacheMb: 0 });
    await ms.tile('openmaptiles', 3, 1, 1);
    await ms.tile('openmaptiles', 3, 1, 1);
    expect(f.calls.filter((c) => c.url.pathname.endsWith('.pbf'))).toHaveLength(2);
  });

  it('fonts are passed through; odd font names are refused', async () => {
    const f = ola();
    const ms = olaMapStyle('k', { fetch: f.fn });
    await ms.font('Gentona Book', '0-255');
    expect(f.calls.at(-1)!.url.pathname).toBe('/tiles/vector/v1/fonts/Gentona%20Book/0-255.pbf');
    await expect(ms.font('../../etc', '0-255')).rejects.toMatchObject({ code: 'no_font' });
    await expect(ms.font('Gentona Book', 'all')).rejects.toMatchObject({ code: 'no_font' });
  });

  it('is served under /v1/map without a session; 404 with no maps provider', async () => {
    const t = await startTestApp();
    try {
      expect((await t.app.inject({ method: 'GET', url: '/v1/map/style.json' })).statusCode).toBe(404);
      t.ctx.mapStyle = olaMapStyle('k1', { fetch: ola().fn });
      const res = await t.app.inject({ method: 'GET', url: '/v1/map/style.json', headers: { host: 'api.example.com', 'x-forwarded-proto': 'https' } });
      expect(res.statusCode).toBe(200);
      expect(res.json().sprite).toBe('https://api.example.com/v1/map/sprite');
      const tile = await t.app.inject({ method: 'GET', url: '/v1/map/tiles/openmaptiles/3/1/2.pbf' });
      expect([tile.statusCode, tile.headers['content-type'], tile.headers['cache-control']]).toEqual([200, 'application/x-protobuf', 'public, max-age=86400']);
      expect((await t.app.inject({ method: 'GET', url: '/v1/map/tiles/openmaptiles/3/1/2.png' })).statusCode).toBe(404);
      const font = await t.app.inject({ method: 'GET', url: '/v1/map/fonts/Gentona%20Book/0-255.pbf' });
      expect(font.statusCode).toBe(200);
    } finally {
      await t.close();
    }
  });
});

describe('road routes for drawing', () => {
  it('each ride keeps the route of its direction; the way back is asked for once', async () => {
    const t = await startTestApp({ autoAcceptCommutes: true });
    try {
      const asked: string[] = [];
      t.ctx.maps = {
        name: 'fake',
        async searchPlaces() {
          return [];
        },
        async estimateTrip(from) {
          const outbound = from.lat < 12.92; // home is south of the office
          asked.push(outbound ? 'out' : 'back');
          return { distanceKm: 9.5, minutes: 20, polyline: outbound ? 'OUT_ROUTE' : 'BACK_ROUTE' };
        },
      };
      const rider = await t.signIn();
      const proposals = (await t.app.inject({ method: 'POST', url: '/v1/commutes/proposals', payload: { request: commuteRequest() }, headers: bearer(rider) })).json();
      expect(proposals.proposals[0].trip.polyline).toBe('OUT_ROUTE');
      await t.app.inject({ method: 'POST', url: '/v1/commutes', payload: { proposalId: proposals.proposals[0].id, usualPayment: 'upi' }, headers: bearer(rider) });
      const rides = (await t.app.inject({ method: 'GET', url: '/v1/rides/upcoming', headers: bearer(rider) })).json().rides;
      expect(rides[0]).toMatchObject({ direction: 'outbound', route: 'OUT_ROUTE' });
      expect(rides[1]).toMatchObject({ direction: 'return', route: 'BACK_ROUTE' });
      expect(asked.filter((a) => a === 'back')).toHaveLength(1);
    } finally {
      await t.close();
    }
  });
});
