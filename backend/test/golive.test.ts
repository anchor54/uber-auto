import { createHash } from 'node:crypto';
import { afterAll, afterEach, beforeEach, describe, expect, it } from 'vitest';
import { olaMaps, cityAreas } from '../src/modules/maps.js';
import { msg91OtpSender } from '../src/modules/sms.js';
import { providersFromEnv } from '../src/providers.js';
import { bearer, closeDb, DEMO_OTP, startTestApp, type TestApp } from './helpers.js';

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
