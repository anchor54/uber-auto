import { afterAll, afterEach, beforeEach, describe, expect, it } from 'vitest';
import { bearer, closeDb, commuteRequest, DEMO_OTP, place, startTestApp, type TestApp } from './helpers.js';

let t: TestApp;
beforeEach(async () => {
  t = await startTestApp();
});
afterEach(async () => {
  await t.close();
});
afterAll(async () => {
  await closeDb();
});

const post = (url: string, payload: unknown, token?: string) =>
  t.app.inject({ method: 'POST', url, payload: payload as object, headers: token ? bearer(token) : {} });
const get = (url: string, token?: string) => t.app.inject({ method: 'GET', url, headers: token ? bearer(token) : {} });

describe('R1.1 sign in with OTP', () => {
  it('sends a code and signs a new rider in', async () => {
    expect((await post('/v1/auth/otp', { phone: '9886054321' })).statusCode).toBe(204);
    expect(t.sentCodes).toEqual([{ phone: '9886054321', code: DEMO_OTP }]);
    const res = await post('/v1/auth/verify', { phone: '9886054321', code: DEMO_OTP });
    expect(res.statusCode).toBe(200);
    expect(res.json().rider).toMatchObject({ phone: '9886054321', name: null });
    expect(res.json().token).toMatch(/^[0-9a-f]{64}$/);
  });

  it('rejects numbers that are not Indian mobiles', async () => {
    const res = await post('/v1/auth/otp', { phone: '1234567890' });
    expect(res.statusCode).toBe(400);
    expect(res.json().message).toBe('Enter a 10-digit mobile number');
  });

  it('rejects a wrong code, then locks after five tries', async () => {
    await post('/v1/auth/otp', { phone: '9886054321' });
    for (let i = 0; i < 5; i++) {
      const res = await post('/v1/auth/verify', { phone: '9886054321', code: '000000' });
      expect(res.statusCode).toBe(401);
      expect(res.json().error).toBe('invalid_code');
    }
    const locked = await post('/v1/auth/verify', { phone: '9886054321', code: DEMO_OTP });
    expect(locked.statusCode).toBe(429);
  });

  it('codes expire after five minutes', async () => {
    await post('/v1/auth/otp', { phone: '9886054321' });
    t.clock.now = new Date(t.clock.now.getTime() + 6 * 60_000);
    const res = await post('/v1/auth/verify', { phone: '9886054321', code: DEMO_OTP });
    expect(res.json().error).toBe('code_expired');
  });

  it('signing in again keeps the same rider', async () => {
    const first = await t.signIn();
    const second = await t.signIn();
    const a = (await get('/v1/home', first)).json().rider.id;
    const b = (await get('/v1/home', second)).json().rider.id;
    expect(a).toBe(b);
  });

  it('protected routes need a session', async () => {
    expect((await get('/v1/home')).statusCode).toBe(401);
    expect((await get('/v1/home', 'not-a-token')).statusCode).toBe(401);
  });
});

describe('R1.2 places, trip and fares', () => {
  it('searches places by title or area', async () => {
    const token = await t.signIn();
    const res = await get('/v1/places?q=hsr', token);
    expect(res.json().places.map((p: { id: string }) => p.id)).toEqual(['home']);
    const all = await get('/v1/places', token);
    expect(all.json().places).toHaveLength(7);
  });

  it('estimates the trip', async () => {
    const token = await t.signIn();
    const res = await post('/v1/trips/estimate', { pickup: place('home'), drop: place('etv') }, token);
    expect(res.json()).toEqual({ distanceKm: 11.9, minutes: 38 });
  });
});

describe('R1.3 booking options follow the ride-type config', () => {
  const body = { flow: 'recurring_weekday', pickup: place('home'), drop: place('etv'), pickupTime: 520 };

  it('offers every type with fares and drivers on the route', async () => {
    const token = await t.signIn();
    const res = (await post('/v1/booking-options', body, token)).json();
    expect(res.defaultType).toBe('cab_sedan');
    expect(res.options.map((o: { type: string; fare: number }) => [o.type, o.fare])).toEqual([
      ['bike', 90],
      ['auto', 160],
      ['cab_hatchback', 215],
      ['cab_sedan', 250],
      ['cab_suv', 330],
    ]);
    expect(res.options.every((o: { driversOnRoute: number }) => o.driversOnRoute === 3)).toBe(true);
  });

  it('changing the config switches ride types off for one flow only', async () => {
    await t.db.query(`update ride_type_config set allowed = '{auto,cab_sedan}', default_type = 'auto' where flow = 'recurring_weekly'`);
    const token = await t.signIn();
    const weekly = (await post('/v1/booking-options', { ...body, flow: 'recurring_weekly' }, token)).json();
    expect(weekly.options.map((o: { type: string }) => o.type)).toEqual(['auto', 'cab_sedan']);
    expect(weekly.defaultType).toBe('auto');
    const weekday = (await post('/v1/booking-options', body, token)).json();
    expect(weekday.options).toHaveLength(5);
  });

  it('no drivers counted outside working hours', async () => {
    const token = await t.signIn();
    const res = (await post('/v1/booking-options', { ...body, pickupTime: 300 }, token)).json();
    expect(res.options.every((o: { driversOnRoute: number }) => o.driversOnRoute === 0)).toBe(true);
  });
});

describe('R1.4 driver match', () => {
  it('proposes three sedan drivers, nearest first, held for 15 minutes', async () => {
    const token = await t.signIn();
    const res = await post('/v1/commutes/proposals', { request: commuteRequest() }, token);
    expect(res.statusCode).toBe(200);
    const body = res.json();
    expect(body.startDate).toBe('2026-10-22');
    expect(body.pickupTime).toBe(520);
    expect(body.proposals.map((p: { driver: { name: string } }) => p.driver.name)).toEqual(['Ravi Kumar', 'Suresh M', 'Imran K']);
    expect(body.proposals[0].fare).toBe(250);
    expect(body.proposals[0].reasons[1]).toBe('Already drives near HSR Layout at this time');
    expect(body.proposals[0].expiresAt).toBe('2026-10-21T03:45:00.000Z');
  });

  it('arrive-by works out the pickup time on the server', async () => {
    const token = await t.signIn();
    const body = (await post('/v1/commutes/proposals', { request: commuteRequest({ timing: 'arrive_by', time: 570 }) }, token)).json();
    expect(body.pickupTime).toBe(520);
  });

  it('woman-driver preference finds nobody in the demo city', async () => {
    const token = await t.signIn();
    const body = (await post('/v1/commutes/proposals', { request: commuteRequest({ preferences: ['woman_driver'] }) }, token)).json();
    expect(body.proposals).toEqual([]);
  });

  it('a woman driver on the route is matched when she exists', async () => {
    await t.db.query(`update drivers set gender = 'female' where id = 'd_imran'`);
    const token = await t.signIn();
    const body = (await post('/v1/commutes/proposals', { request: commuteRequest({ preferences: ['woman_driver'] }) }, token)).json();
    expect(body.proposals.map((p: { driver: { id: string } }) => p.driver.id)).toEqual(['d_imran']);
  });

  it('pickups too far from a driver base are not offered', async () => {
    const token = await t.signIn();
    const body = (
      await post('/v1/commutes/proposals', { request: commuteRequest({ pickup: place('airport'), drop: place('manyata') }) }, token)
    ).json();
    expect(body.proposals).toEqual([]);
  });

  it('a ride type the config switched off is refused', async () => {
    await t.db.query(`update ride_type_config set allowed = '{auto}' where flow = 'recurring_weekday'`);
    const token = await t.signIn();
    const res = await post('/v1/commutes/proposals', { request: commuteRequest() }, token);
    expect(res.statusCode).toBe(422);
    expect(res.json().error).toBe('ride_type_not_allowed');
  });

  it('invalid requests get a readable message', async () => {
    const token = await t.signIn();
    const res = await post('/v1/commutes/proposals', { request: commuteRequest({ drop: place('home') }) }, token);
    expect(res.statusCode).toBe(400);
    expect(res.json().message).toBe("Pickup and drop-off can't be the same place");
  });
});

describe('R1.5 confirm and R1.6 Home', () => {
  async function propose(token: string, overrides: Record<string, unknown> = {}) {
    const body = (await post('/v1/commutes/proposals', { request: commuteRequest(overrides) }, token)).json();
    return body.proposals as Array<{ id: string; driver: { id: string; name: string } }>;
  }

  it('Home is empty for a new rider', async () => {
    const token = await t.signIn();
    const home = (await get('/v1/home', token)).json();
    expect(home.commute).toBeNull();
    expect(home.recentRides).toEqual([]);
  });

  it('confirming creates the commute; Home shows it with the driver', async () => {
    const token = await t.signIn();
    const [ravi] = await propose(token);
    const res = await post('/v1/commutes', { proposalId: ravi!.id, usualPayment: 'cash' }, token);
    expect(res.statusCode).toBe(201);
    expect(res.json()).toMatchObject({
      status: 'active',
      startDate: '2026-10-22',
      pickupTime: 520,
      fare: 250,
      usualPayment: 'cash',
      rideType: 'cab_sedan',
      weekdays: [1, 2, 3, 4, 5],
      driver: { name: 'Ravi Kumar', plate: 'KA 01 AB 1234', upiId: 'ravi.kumar@okbank' },
    });
    const home = (await get('/v1/home', token)).json();
    expect(home.commute.driver.name).toBe('Ravi Kumar');
    expect(home.rider.usualPayment).toBe('cash');
  });

  it('without auto-accept the commute waits for the driver', async () => {
    await t.close();
    t = await startTestApp({ autoAcceptCommutes: false });
    const token = await t.signIn();
    const [ravi] = await propose(token);
    const res = await post('/v1/commutes', { proposalId: ravi!.id, usualPayment: 'upi' }, token);
    expect(res.json().status).toBe('awaiting_driver');
  });

  it('a hold that ran out must be searched again', async () => {
    const token = await t.signIn();
    const [ravi] = await propose(token);
    t.clock.now = new Date(t.clock.now.getTime() + 16 * 60_000);
    const res = await post('/v1/commutes', { proposalId: ravi!.id, usualPayment: 'upi' }, token);
    expect(res.statusCode).toBe(410);
  });

  it("riders can't confirm someone else's proposal", async () => {
    const mine = await t.signIn('9886054321');
    const theirs = await t.signIn('9000000001');
    const [ravi] = await propose(mine);
    const res = await post('/v1/commutes', { proposalId: ravi!.id, usualPayment: 'upi' }, theirs);
    expect(res.statusCode).toBe(404);
  });

  it("a driver's slot can't be taken twice: the second rider is told and Ravi drops out of matches", async () => {
    const a = await t.signIn('9886054321');
    const b = await t.signIn('9000000001');
    const [raviForA] = await propose(a);
    const [raviForB] = await propose(b);
    expect((await post('/v1/commutes', { proposalId: raviForA!.id, usualPayment: 'upi' }, a)).statusCode).toBe(201);
    const second = await post('/v1/commutes', { proposalId: raviForB!.id, usualPayment: 'upi' }, b);
    expect(second.statusCode).toBe(409);
    expect(second.json().error).toBe('driver_unavailable');
    const again = await propose(b);
    expect(again.map((p) => p.driver.name)).toEqual(['Suresh M', 'Imran K']);
  });

  it('a commute at a different time can share the driver', async () => {
    const a = await t.signIn('9886054321');
    const b = await t.signIn('9000000001');
    const [raviForA] = await propose(a);
    await post('/v1/commutes', { proposalId: raviForA!.id, usualPayment: 'upi' }, a);
    // 7:30 AM pickup, 38 min trip, ends 8:08: 32 minutes before Ravi's 8:40 pickup.
    const forB = await propose(b, { time: 450, returnTrip: false });
    expect(forB[0]!.driver.name).toBe('Ravi Kumar');
  });

  it('Saturday-only commutes start this Saturday', async () => {
    const token = await t.signIn();
    const body = (await post('/v1/commutes/proposals', { request: commuteRequest({ repeat: 'chosen_days', weekdays: [6] }) }, token)).json();
    expect(body.startDate).toBe('2026-10-24');
  });

  it('the waitlist keeps the request', async () => {
    const token = await t.signIn();
    const res = await post('/v1/waitlist', { request: commuteRequest({ preferences: ['woman_driver'] }) }, token);
    expect(res.statusCode).toBe(201);
    const rows = (await t.db.query('select count(*)::int as n from waitlist')).rows[0];
    expect(rows.n).toBe(1);
  });
});
