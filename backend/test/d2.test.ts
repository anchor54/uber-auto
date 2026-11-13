import { afterAll, afterEach, beforeEach, describe, expect, it } from 'vitest';
import { configFromEnv } from '../src/context.js';
import { bearer, closeDb, commuteRequest, DEMO_OTP, startTestApp, type TestApp } from './helpers.js';

/** Flow D2: the driver accepts or declines a commute the rider confirmed. */

const RAVI_PHONE = '9000000010'; // d_ravi in the seed
const SURESH_PHONE = '9000000011'; // d_suresh

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

async function driverSignIn(phone: string) {
  expect((await post('/v1/driver/auth/otp', { phone })).statusCode).toBe(204);
  const res = await post('/v1/driver/auth/verify', { phone, code: DEMO_OTP });
  expect(res.statusCode).toBe(200);
  return res.json().token as string;
}

/** A rider confirms Ravi for the standard weekday commute. Returns the commute. */
async function confirmRavi(riderPhone = '9886054321', overrides: Record<string, unknown> = {}) {
  const token = await t.signIn(riderPhone);
  const proposals = (await post('/v1/commutes/proposals', { request: commuteRequest(overrides) }, token)).json().proposals;
  const ravi = proposals.find((p: { driver: { id: string } }) => p.driver.id === 'd_ravi');
  expect(ravi, 'Ravi is offered').toBeDefined();
  const res = await post('/v1/commutes', { proposalId: ravi.id, usualPayment: 'upi' }, token);
  expect(res.statusCode).toBe(201);
  return { token, commute: res.json() };
}

describe('driver sign-in', () => {
  it('a registered driver signs in with the same OTP flow', async () => {
    const res = await post('/v1/driver/auth/otp', { phone: RAVI_PHONE });
    expect(res.statusCode).toBe(204);
    const v = await post('/v1/driver/auth/verify', { phone: RAVI_PHONE, code: DEMO_OTP });
    expect(v.json().driver).toMatchObject({ id: 'd_ravi', name: 'Ravi Kumar' });
  });

  it('an unknown number gets no code', async () => {
    const res = await post('/v1/driver/auth/otp', { phone: '9886054321' });
    expect(res.statusCode).toBe(403);
    expect(res.json().error).toBe('not_a_driver');
    expect(t.sentCodes).toEqual([]);
  });

  it('rider and driver sessions only open their own routes', async () => {
    const rider = await t.signIn();
    const driver = await driverSignIn(RAVI_PHONE);
    expect((await get('/v1/driver/requests', rider)).statusCode).toBe(403);
    expect((await get('/v1/home', driver)).statusCode).toBe(403);
  });

  it('a driver can also be a rider with the same phone', async () => {
    await driverSignIn(RAVI_PHONE);
    const rider = await t.signIn(RAVI_PHONE);
    expect((await get('/v1/home', rider)).json().rider.phone).toBe(RAVI_PHONE);
  });
});

describe('waiting for the driver', () => {
  it('a confirmed commute waits for the driver until respondBy', async () => {
    const { commute } = await confirmRavi();
    expect(commute.status).toBe('awaiting_driver');
    // Now is Wed 9:00 IST; first pickup Thu 8:40. 12 hours (Wed 21:00 IST)
    // comes before "an hour before pickup" (Thu 7:40), so 21:00 wins.
    expect(commute.respondBy).toBe('2026-10-21T15:30:00.000Z');
    expect(commute.driver.phone, 'phone shared only after accepting').toBeNull();
  });

  it('respondBy is an hour before the first pickup when that comes sooner', async () => {
    t.clock.now = new Date('2026-10-21T15:30:00Z'); // Wed 21:00 IST
    const { commute } = await confirmRavi();
    expect(commute.respondBy).toBe('2026-10-22T02:10:00.000Z'); // Thu 7:40 IST
  });

  it('the driver sees the request; other drivers do not', async () => {
    const { commute } = await confirmRavi();
    const ravi = await driverSignIn(RAVI_PHONE);
    const suresh = await driverSignIn(SURESH_PHONE);
    const mine = (await get('/v1/driver/requests', ravi)).json().requests;
    expect(mine).toHaveLength(1);
    expect(mine[0]).toMatchObject({ id: commute.id, riderName: 'New rider', fare: 250, pickupTime: 520, startDate: '2026-10-22' });
    expect((await get('/v1/driver/requests', suresh)).json().requests).toEqual([]);
  });

  it('a commute awaiting the driver holds the slot against other riders', async () => {
    await confirmRavi('9886054321');
    const other = await t.signIn('9886054322');
    const proposals = (await post('/v1/commutes/proposals', { request: commuteRequest() }, other)).json().proposals;
    expect(proposals.map((p: { driver: { id: string } }) => p.driver.id)).not.toContain('d_ravi');
  });
});

describe('accept', () => {
  it('the driver accepts; Home shows the commute active with their number', async () => {
    const { token, commute } = await confirmRavi();
    const ravi = await driverSignIn(RAVI_PHONE);
    const res = await post(`/v1/driver/commutes/${commute.id}/accept`, {}, ravi);
    expect(res.json()).toEqual({ id: commute.id, status: 'active' });
    const home = (await get('/v1/home', token)).json();
    expect(home.commute.status).toBe('active');
    expect(home.commute.driver.phone).toBe(RAVI_PHONE);
    expect((await get('/v1/driver/requests', ravi)).json().requests).toEqual([]);
  });

  it('answering twice is a conflict', async () => {
    const { commute } = await confirmRavi();
    const ravi = await driverSignIn(RAVI_PHONE);
    await post(`/v1/driver/commutes/${commute.id}/accept`, {}, ravi);
    const again = await post(`/v1/driver/commutes/${commute.id}/decline`, {}, ravi);
    expect(again.statusCode).toBe(409);
    expect(again.json().error).toBe('already_answered');
  });

  it("a driver can't answer another driver's request", async () => {
    const { commute } = await confirmRavi();
    const suresh = await driverSignIn(SURESH_PHONE);
    const res = await post(`/v1/driver/commutes/${commute.id}/accept`, {}, suresh);
    expect(res.statusCode).toBe(404);
  });
});

describe('decline', () => {
  it('Home shows the decline; the rider searches again without that driver', async () => {
    const { token, commute } = await confirmRavi();
    const ravi = await driverSignIn(RAVI_PHONE);
    expect((await post(`/v1/driver/commutes/${commute.id}/decline`, {}, ravi)).json().status).toBe('declined');

    const home = (await get('/v1/home', token)).json();
    expect(home.commute).toMatchObject({ status: 'declined', declineReason: 'declined' });

    const again = (await post('/v1/commutes/proposals', { request: commuteRequest() }, token)).json().proposals;
    expect(again.length).toBeGreaterThan(0);
    expect(again.map((p: { driver: { id: string } }) => p.driver.id)).not.toContain('d_ravi');
  });

  it("a decline frees the driver's slot for other riders", async () => {
    const { commute } = await confirmRavi('9886054321');
    const ravi = await driverSignIn(RAVI_PHONE);
    await post(`/v1/driver/commutes/${commute.id}/decline`, {}, ravi);
    const other = await t.signIn('9886054322');
    const proposals = (await post('/v1/commutes/proposals', { request: commuteRequest() }, other)).json().proposals;
    expect(proposals[0].driver.id).toBe('d_ravi');
  });

  it('booking a new driver after a decline makes that the commute on Home', async () => {
    const { token, commute } = await confirmRavi();
    await post(`/v1/driver/commutes/${commute.id}/decline`, {}, await driverSignIn(RAVI_PHONE));
    const [next] = (await post('/v1/commutes/proposals', { request: commuteRequest() }, token)).json().proposals;
    const res = await post('/v1/commutes', { proposalId: next.id, usualPayment: 'upi' }, token);
    expect(res.statusCode).toBe(201);
    const home = (await get('/v1/home', token)).json();
    expect(home.commute.id).toBe(res.json().id);
    expect(home.commute.status).toBe('awaiting_driver');
  });
});

describe('no answer in time', () => {
  it('after respondBy the request lapses: Home says so and the driver can no longer accept', async () => {
    const { token, commute } = await confirmRavi();
    t.clock.now = new Date('2026-10-21T15:30:01Z'); // a second after respondBy
    const home = (await get('/v1/home', token)).json();
    expect(home.commute).toMatchObject({ status: 'declined', declineReason: 'no_answer' });

    const ravi = await driverSignIn(RAVI_PHONE);
    const res = await post(`/v1/driver/commutes/${commute.id}/accept`, {}, ravi);
    expect(res.statusCode).toBe(410);
    expect(res.json().error).toBe('request_expired');
  });

  it('just before respondBy the driver can still accept', async () => {
    const { commute } = await confirmRavi();
    t.clock.now = new Date('2026-10-21T15:29:59Z');
    const ravi = await driverSignIn(RAVI_PHONE);
    expect((await post(`/v1/driver/commutes/${commute.id}/accept`, {}, ravi)).json().status).toBe('active');
  });
});

describe('dev simulator', () => {
  it('is not there unless switched on', async () => {
    expect((await get('/dev/sim/requests')).statusCode).toBe(404);
  });

  it('acts as the asked driver: list, accept, decline, expire', async () => {
    await t.close();
    t = await startTestApp({ enableDriverSim: true });
    const a = await confirmRavi('9886054321');
    const b = await confirmRavi('9886054322', { time: 600, returnTime: 1170 });
    const c = await confirmRavi('9886054323', { time: 680, returnTime: 1230 });

    const listed = (await get('/dev/sim/requests')).json().requests.map((r: { id: string }) => r.id);
    expect(listed).toEqual([a.commute.id, b.commute.id, c.commute.id]);

    expect((await post(`/dev/sim/commutes/${a.commute.id}/accept`, {})).json().status).toBe('active');
    expect((await post(`/dev/sim/commutes/${b.commute.id}/decline`, {})).json().status).toBe('declined');
    expect((await post(`/dev/sim/commutes/${c.commute.id}/expire`, {})).json().status).toBe('declined');

    expect((await get('/v1/home', a.token)).json().commute.status).toBe('active');
    expect((await get('/v1/home', b.token)).json().commute.declineReason).toBe('declined');
    expect((await get('/v1/home', c.token)).json().commute.declineReason).toBe('no_answer');
  });
});

describe('production guard', () => {
  const prod = { NODE_ENV: 'production', OTP_SECRET: 'x'.repeat(32) };

  it('refuses demo switches in production', () => {
    expect(() => configFromEnv({ ...prod, FIXED_OTP: '123456' })).toThrow(/FIXED_OTP/);
    expect(() => configFromEnv({ ...prod, AUTO_ACCEPT_COMMUTES: 'true' })).toThrow(/AUTO_ACCEPT/);
    expect(() => configFromEnv({ ...prod, ENABLE_DRIVER_SIM: 'true' })).toThrow(/DRIVER_SIM/);
    expect(configFromEnv(prod)).toMatchObject({ autoAcceptCommutes: false, enableDriverSim: false });
  });

  it('refuses production without a strong OTP secret', () => {
    expect(() => configFromEnv({ NODE_ENV: 'production' })).toThrow(/OTP_SECRET/);
    expect(() => configFromEnv({ NODE_ENV: 'production', OTP_SECRET: 'short' })).toThrow(/OTP_SECRET/);
  });

  it('auto-accept is off unless asked for', () => {
    expect(configFromEnv({}).autoAcceptCommutes).toBe(false);
    expect(configFromEnv({ AUTO_ACCEPT_COMMUTES: 'true' }).autoAcceptCommutes).toBe(true);
  });
});
