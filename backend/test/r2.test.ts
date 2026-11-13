import { afterAll, afterEach, beforeEach, describe, expect, it } from 'vitest';
import { closeStaleRides, createRidesNightly, flagLateDrivers, queueLegIsOn, queuePaymentReminders } from '../src/modules/jobs.js';
import { sendDueNotifications, type PushMessage } from '../src/modules/notify.js';
import { simPlay } from '../src/modules/ride_sim.js';
import { createRides } from '../src/modules/rides.js';
import { bearer, closeDb, commuteRequest, DEMO_OTP, startTestApp, type TestApp } from './helpers.js';

/** Flow R2 against real Postgres: ride records, the driver's moves, live view, payment, ratings, worker jobs. */

const RAVI = '9000000010';
const MIN = 60_000;
/** Thursday 22 Oct 2026, the first ride day, at [hh:mm] IST. */
const thu = (hhmm: string) => {
  const [h, m] = hhmm.split(':').map(Number);
  return new Date(Date.UTC(2026, 9, 22, h!, m!) - 330 * MIN);
};

let t: TestApp;
beforeEach(async () => {
  t = await startTestApp({ autoAcceptCommutes: true });
});
afterEach(async () => {
  await t.close();
});
afterAll(async () => {
  await closeDb();
});

const post = (url: string, payload: unknown, token?: string) =>
  t.app.inject({ method: 'POST', url, payload: payload as object, headers: token ? bearer(token) : {} });
const put = (url: string, payload: unknown, token?: string) =>
  t.app.inject({ method: 'PUT', url, payload: payload as object, headers: token ? bearer(token) : {} });
const get = (url: string, token?: string) => t.app.inject({ method: 'GET', url, headers: token ? bearer(token) : {} });

async function driverToken(phone = RAVI) {
  await post('/v1/driver/auth/otp', { phone });
  return (await post('/v1/driver/auth/verify', { phone, code: DEMO_OTP })).json().token as string;
}

/** A rider with an active Mon–Fri commute with Ravi (auto-accept), so rides exist. */
async function commuter(phone = '9886054321', overrides: Record<string, unknown> = {}) {
  const rider = await t.signIn(phone);
  const proposals = (await post('/v1/commutes/proposals', { request: commuteRequest(overrides) }, rider)).json().proposals;
  const ravi = proposals.find((p: { driver: { id: string } }) => p.driver.id === 'd_ravi');
  const commute = (await post('/v1/commutes', { proposalId: ravi.id, usualPayment: 'upi' }, rider)).json();
  const rides = (await get('/v1/rides/upcoming?days=7', rider)).json().rides;
  return { rider, commute, rides, first: rides[0] as { id: string; pin: string | null; pickup: { lat: number; lng: number }; drop: { lat: number; lng: number } } };
}

const at = (date: Date) => date.toISOString();
const point = (lat: number, lng: number, when = t.clock.now) => ({ lat, lng, heading: 90, speed: 8, at: at(when) });

async function sendLocation(driver: string, lat: number, lng: number) {
  const res = await post('/v1/driver/location', { points: [point(lat, lng)] }, driver);
  expect(res.statusCode, res.body).toBe(204);
}

/** Departs [rideId] at 8:20 with the driver ~2 km away. */
async function departed(rideId: string, pickup: { lat: number; lng: number }) {
  t.clock.now = thu('08:20');
  const driver = await driverToken();
  expect((await put('/v1/driver/duty', { onDuty: true }, driver)).statusCode).toBe(204);
  await sendLocation(driver, pickup.lat + 0.018, pickup.lng);
  const res = await post(`/v1/driver/rides/${rideId}/depart`, {}, driver);
  expect(res.statusCode, res.body).toBe(200);
  return driver;
}

async function arrivedAt(rideId: string, pickup: { lat: number; lng: number }, when: string) {
  const driver = await departed(rideId, pickup);
  t.clock.now = thu(when);
  await sendLocation(driver, pickup.lat, pickup.lng);
  const res = await post(`/v1/driver/rides/${rideId}/arrive`, {}, driver);
  expect(res.statusCode, res.body).toBe(200);
  return driver;
}

describe('ride records', () => {
  it('confirming creates two rides per weekday, 14 days ahead; upcoming shows a week', async () => {
    const { rider, rides } = await commuter();
    const total = (await t.db.query('select count(*)::int as n from rides')).rows[0].n;
    expect(total).toBe(20); // Thu 22 Oct to Wed 4 Nov: 10 weekdays × 2
    expect(rides.map((r: { date: string; direction: string }) => `${r.date} ${r.direction}`)).toEqual([
      '2026-10-22 outbound', '2026-10-22 return', '2026-10-23 outbound', '2026-10-23 return',
      '2026-10-26 outbound', '2026-10-26 return', '2026-10-27 outbound', '2026-10-27 return',
    ]);
    expect(rides[0]).toMatchObject({ state: 'scheduled', fare: 250, pin: null, pickupAt: '2026-10-22T03:10:00.000Z' });
    expect(rides[1].pickup.id).toBe('etv'); // the return leg swaps pickup and drop
    const home = (await get('/v1/home', rider)).json();
    expect(home.nextRide.id).toBe(rides[0].id);
  });

  it('creating again does nothing; the nightly job runs once a night after 00:30', async () => {
    await commuter();
    expect(await createRides(t.ctx)).toBe(0);
    t.clock.now = new Date('2026-10-22T18:45:00Z'); // Fri 00:15 IST: too early
    expect(await createRidesNightly(t.ctx)).toBe(0);
    t.clock.now = new Date('2026-10-22T19:05:00Z'); // Fri 23 Oct 00:35 IST: Thu 5 and Fri 6 Nov come into range
    expect(await createRidesNightly(t.ctx)).toBe(4);
    expect(await createRidesNightly(t.ctx)).toBe(0); // already ran tonight
  });

  it('rides are created when the driver accepts (D2), not before', async () => {
    await t.close();
    t = await startTestApp({ autoAcceptCommutes: false });
    const { commute, rides } = await commuter();
    expect(rides).toEqual([]);
    await post(`/v1/driver/commutes/${commute.id}/accept`, {}, await driverToken());
    expect((await t.db.query('select count(*)::int as n from rides')).rows[0].n).toBe(20);
  });

  it("riders and drivers can't see or move each other's rides", async () => {
    const { first } = await commuter();
    const other = await t.signIn('9886054322');
    expect((await get(`/v1/rides/${first.id}`, other)).statusCode).toBe(403);
    expect((await get(`/v1/rides/${first.id}/live`, other)).json().error).toBe('not_your_ride');
    const suresh = await driverToken('9000000011');
    await put('/v1/driver/duty', { onDuty: true }, suresh);
    t.clock.now = thu('08:20');
    expect((await post(`/v1/driver/rides/${first.id}/depart`, {}, suresh)).statusCode).toBe(403);
  });
});

describe('a normal leg', () => {
  it('depart → arrive → start with the PIN → end, with the live view following', async () => {
    const { rider, first } = await commuter();
    const driver = await departed(first.id, first.pickup);

    // The PIN shows from the moment the driver departs.
    const ride = (await get(`/v1/rides/${first.id}`, rider)).json();
    expect(ride.state).toBe('on_the_way');
    expect(ride.pin).toMatch(/^\d{4}$/);
    expect(ride.driver).toMatchObject({ name: 'Ravi Kumar', phone: RAVI, upiId: 'ravi.kumar@okbank' });

    const far = (await get(`/v1/rides/${first.id}/live`, rider)).json();
    expect(far).toMatchObject({ state: 'on_the_way', late: false, serverTime: at(thu('08:20')) });
    expect(far.etaMinutes).toBe(10); // 2 km × 1.85 at 22 km/h
    t.clock.now = thu('08:26');
    await sendLocation(driver, first.pickup.lat + 0.006, first.pickup.lng);
    const near = (await get(`/v1/rides/${first.id}/live`, rider)).json();
    expect(near.etaMinutes).toBeLessThan(far.etaMinutes);

    // Too far to arrive; then at the pickup.
    expect((await post(`/v1/driver/rides/${first.id}/arrive`, {}, driver)).json().error).toBe('too_far');
    t.clock.now = thu('08:38');
    await sendLocation(driver, first.pickup.lat, first.pickup.lng);
    const arrived = (await post(`/v1/driver/rides/${first.id}/arrive`, {}, driver)).json();
    expect(arrived.waitEndsAt).toBe(at(thu('08:43')));
    expect((await get(`/v1/rides/${first.id}/live`, rider)).json()).toMatchObject({ state: 'arrived', etaMinutes: 0 });

    const start = await post(`/v1/driver/rides/${first.id}/start`, { pin: ride.pin }, driver);
    expect(start.json().state).toBe('in_ride');
    expect((await get(`/v1/rides/${first.id}`, rider)).json().pin, 'PIN hidden once riding').toBeNull();

    t.clock.now = thu('09:20');
    const end = await post(`/v1/driver/rides/${first.id}/end`, { lat: first.drop.lat, lng: first.drop.lng }, driver);
    expect(end.json()).toMatchObject({ state: 'completed', paymentStatus: 'unmarked' });

    const events = (await t.db.query('select from_state, to_state, actor_type from ride_events where ride_id = $1 order by id', [first.id])).rows;
    expect(events.map((e) => `${e.from_state}→${e.to_state}`)).toEqual([
      'scheduled→on_the_way', 'on_the_way→arrived', 'arrived→in_ride', 'in_ride→completed',
    ]);
    const queued = (await t.db.query('select kind from notifications where ride_id = $1 order by id', [first.id])).rows.map((r) => r.kind);
    expect(queued).toEqual(['driver_on_the_way', 'driver_arrived']);
  });

  it('three wrong PINs lock the start', async () => {
    const { first } = await commuter();
    const driver = await arrivedAt(first.id, first.pickup, '08:38');
    const pin = (await t.db.query('select pin from rides where id = $1', [first.id])).rows[0].pin as string;
    const wrong = pin === '0000' ? '1111' : '0000';
    const a = await post(`/v1/driver/rides/${first.id}/start`, { pin: wrong }, driver);
    expect([a.statusCode, a.json().error, a.json().message]).toEqual([422, 'wrong_pin', "That PIN isn't right. 2 tries left."]);
    expect((await post(`/v1/driver/rides/${first.id}/start`, { pin: wrong }, driver)).json().message).toMatch(/1 try left/);
    expect((await post(`/v1/driver/rides/${first.id}/start`, { pin: wrong }, driver)).statusCode).toBe(423);
    const locked = await post(`/v1/driver/rides/${first.id}/start`, { pin }, driver);
    expect([locked.statusCode, locked.json().error]).toEqual([423, 'pin_locked']);
  });

  it('ending away from the drop-off needs a reason', async () => {
    const { first } = await commuter();
    const driver = await arrivedAt(first.id, first.pickup, '08:38');
    const pin = (await t.db.query('select pin from rides where id = $1', [first.id])).rows[0].pin;
    await post(`/v1/driver/rides/${first.id}/start`, { pin }, driver);
    const far = { lat: first.pickup.lat, lng: first.pickup.lng };
    expect((await post(`/v1/driver/rides/${first.id}/end`, far, driver)).statusCode).toBe(422);
    const ok = await post(`/v1/driver/rides/${first.id}/end`, { ...far, reason: 'Rider asked to stop' }, driver);
    expect(ok.json().state).toBe('completed');
  });

  it('a driver with a ride under way can neither depart another nor go off duty', async () => {
    const a = await commuter('9886054321');
    // 10:00 and 20:00: clear of Ravi's 8:40 and 18:30 legs plus the 20-minute gap.
    const b = await commuter('9886054322', { time: 600, returnTime: 1200 });
    const driver = await departed(a.first.id, a.first.pickup);
    const second = await post(`/v1/driver/rides/${b.first.id}/depart`, {}, driver);
    expect([second.statusCode, second.json().error]).toEqual([409, 'driver_busy']);
    expect((await put('/v1/driver/duty', { onDuty: false }, driver)).json().error).toBe('rides_in_progress');
  });

  it('an off-duty driver cannot depart, nor more than 90 minutes early', async () => {
    const { first } = await commuter();
    const driver = await driverToken();
    t.clock.now = thu('08:20');
    expect((await post(`/v1/driver/rides/${first.id}/depart`, {}, driver)).json().error).toBe('off_duty');
    await put('/v1/driver/duty', { onDuty: true }, driver);
    t.clock.now = thu('07:05');
    expect((await post(`/v1/driver/rides/${first.id}/depart`, {}, driver)).json().error).toBe('too_early');
  });

  it('a move that is not allowed says the current state', async () => {
    const { first } = await commuter();
    const driver = await driverToken();
    const res = await post(`/v1/driver/rides/${first.id}/arrive`, {}, driver);
    expect([res.statusCode, res.json().error]).toEqual([409, 'invalid_transition']);
    expect(res.json().message).toMatch(/scheduled/);
  });
});

describe('in-ride ETA', () => {
  it('scales the booked trip time by the distance left, so it never exceeds the trip', async () => {
    const { rider, first } = await commuter();
    const driver = await arrivedAt(first.id, first.pickup, '08:38');
    const pin = (await t.db.query('select pin from rides where id = $1', [first.id])).rows[0].pin;
    await post(`/v1/driver/rides/${first.id}/start`, { pin }, driver);
    const live = async () => (await get(`/v1/rides/${first.id}/live`, rider)).json().etaMinutes as number;
    // At the pickup: the whole 38-minute trip is ahead.
    expect(await live()).toBe(38);
    // Halfway there in straight-line terms: half the trip time.
    t.clock.now = thu('08:58');
    await sendLocation(driver, (first.pickup.lat + first.drop.lat) / 2, (first.pickup.lng + first.drop.lng) / 2);
    expect(await live()).toBe(19);
    // Almost at the drop-off.
    t.clock.now = thu('09:15');
    await sendLocation(driver, first.drop.lat, first.drop.lng);
    expect(await live()).toBe(1);
  });
});

describe('location', () => {
  it('points older than 2 minutes or in the future are refused', async () => {
    const driver = await driverToken();
    const old = await post('/v1/driver/location', { points: [point(12.9, 77.6, new Date(t.clock.now.getTime() - 3 * MIN))] }, driver);
    expect([old.statusCode, old.json().error]).toEqual([400, 'stale_location']);
    const ahead = await post('/v1/driver/location', { points: [point(12.9, 77.6, new Date(t.clock.now.getTime() + 5 * MIN))] }, driver);
    expect(ahead.statusCode).toBe(400);
  });

  it('keeps one track point every 30 seconds during a ride', async () => {
    const { first } = await commuter();
    const driver = await departed(first.id, first.pickup);
    for (let s = 5; s <= 65; s += 5) {
      t.clock.now = new Date(thu('08:20').getTime() + s * 1000);
      await sendLocation(driver, first.pickup.lat + 0.01, first.pickup.lng);
    }
    const n = (await t.db.query('select count(*)::int as n from ride_track where ride_id = $1', [first.id])).rows[0].n;
    expect(n).toBe(3); // 8:20:05 (first), 8:20:35, 8:21:05
  });
});

describe('waiting and no-shows', () => {
  it('no-show: not before the wait ends, not without contact; the return leg stays booked', async () => {
    const { rider, rides, first } = await commuter();
    const driver = await arrivedAt(first.id, first.pickup, '08:38');
    t.clock.now = thu('08:42');
    expect((await post(`/v1/driver/rides/${first.id}/no-show`, {}, driver)).statusCode).toBe(425);
    t.clock.now = thu('08:43');
    expect((await post(`/v1/driver/rides/${first.id}/no-show`, {}, driver)).json().error).toBe('no_contact_logged');
    expect((await post(`/v1/driver/rides/${first.id}/contact`, { kind: 'call' }, driver)).statusCode).toBe(204);
    expect((await post(`/v1/driver/rides/${first.id}/no-show`, {}, driver)).json().state).toBe('no_show');
    expect((await get(`/v1/rides/${rides[1].id}`, rider)).json().state).toBe('scheduled');
  });

  it('"I\'m 5 min late" extends the wait to 10 minutes, once', async () => {
    const { rider, first } = await commuter();
    const driver = await arrivedAt(first.id, first.pickup, '08:38');
    const late = await post(`/v1/rides/${first.id}/running-late`, {}, rider);
    expect(late.json()).toMatchObject({ riderLateUsed: true, waitEndsAt: at(thu('08:48')) });
    expect((await post(`/v1/rides/${first.id}/running-late`, {}, rider)).json().error).toBe('already_used');
    await post(`/v1/driver/rides/${first.id}/contact`, { kind: 'alert' }, driver);
    t.clock.now = thu('08:44');
    expect((await post(`/v1/driver/rides/${first.id}/no-show`, {}, driver)).statusCode).toBe(425);
    const kinds = (await t.db.query('select kind, recipient_type from notifications where ride_id = $1', [first.id])).rows;
    expect(kinds).toContainEqual({ kind: 'rider_running_late', recipient_type: 'driver' });
    expect(kinds).toContainEqual({ kind: 'driver_alert', recipient_type: 'rider' });
  });

  it('said before the driver arrives, it applies on arrival', async () => {
    const { rider, first } = await commuter();
    await post(`/v1/rides/${first.id}/running-late`, {}, rider);
    await arrivedAt(first.id, first.pickup, '08:38');
    expect((await get(`/v1/rides/${first.id}`, rider)).json().waitEndsAt).toBe(at(thu('08:48')));
  });
});

/** Completes the first ride and returns its id and the tokens. */
async function completedRide() {
  const c = await commuter();
  const driver = await arrivedAt(c.first.id, c.first.pickup, '08:38');
  const pin = (await t.db.query('select pin from rides where id = $1', [c.first.id])).rows[0].pin;
  await post(`/v1/driver/rides/${c.first.id}/start`, { pin }, driver);
  t.clock.now = thu('09:20');
  await post(`/v1/driver/rides/${c.first.id}/end`, { lat: c.first.drop.lat, lng: c.first.drop.lng }, driver);
  return { ...c, driver, id: c.first.id };
}

describe('payment marks and ratings', () => {
  it('each side marks on its own; the status combines them', async () => {
    const { rider, driver, id } = await completedRide();
    expect((await post(`/v1/rides/${id}/payment`, { method: 'upi' }, rider)).json()).toMatchObject({ paymentStatus: 'paid_by_rider' });
    expect((await post(`/v1/driver/rides/${id}/payment`, { mark: 'not_paid' }, driver)).json()).toMatchObject({ paymentStatus: 'disputed' });
    expect((await post(`/v1/driver/rides/${id}/payment`, { mark: 'cash' }, driver)).json()).toMatchObject({ paymentStatus: 'paid' });
  });

  it("can't mark or rate a ride that hasn't ended", async () => {
    const { rider, first } = await commuter();
    expect((await post(`/v1/rides/${first.id}/payment`, { method: 'cash' }, rider)).statusCode).toBe(409);
    expect((await post(`/v1/rides/${first.id}/rating`, { stars: 5 }, rider)).statusCode).toBe(409);
  });

  it('unsettled lists completed rides the rider has neither marked nor rated', async () => {
    const { rider, id } = await completedRide();
    expect((await get('/v1/rides/unsettled', rider)).json().rides.map((r: { id: string }) => r.id)).toEqual([id]);
    expect((await post(`/v1/rides/${id}/rating`, { stars: 4, tags: ['Clean car'] }, rider)).statusCode).toBe(204);
    expect((await get('/v1/rides/unsettled', rider)).json().rides).toEqual([]);
  });

  it('a rating can change for 7 days; "don\'t match me" keeps that driver out of future matches', async () => {
    const { rider, id } = await completedRide();
    await post(`/v1/rides/${id}/rating`, { stars: 5 }, rider);
    await post(`/v1/rides/${id}/rating`, { stars: 2, tags: ['Late'], note: 'Twenty minutes late', dontMatch: true }, rider);
    expect((await get(`/v1/rides/${id}`, rider)).json().rating).toEqual({ stars: 2, tags: ['Late'], note: 'Twenty minutes late', dontMatch: true });
    const again = (await post('/v1/commutes/proposals', { request: commuteRequest({ time: 600, returnTime: 1200 }) }, rider)).json().proposals;
    expect(again.map((p: { driver: { id: string } }) => p.driver.id)).not.toContain('d_ravi');
    t.clock.now = new Date(thu('09:20').getTime() + 8 * 24 * 60 * MIN);
    expect((await post(`/v1/rides/${id}/rating`, { stars: 3 }, rider)).json().error).toBe('rating_closed');
  });
});

describe('worker jobs', () => {
  it('"Leg is on" is queued 30 minutes before pickup, once', async () => {
    const { first } = await commuter();
    t.clock.now = thu('08:05');
    expect(await queueLegIsOn(t.ctx)).toBe(0);
    t.clock.now = thu('08:11');
    expect(await queueLegIsOn(t.ctx)).toBe(1);
    expect(await queueLegIsOn(t.ctx)).toBe(0);
    const n = (await t.db.query(`select payload from notifications where kind = 'leg_is_on' and ride_id = $1`, [first.id])).rows[0];
    expect(n.payload).toEqual({ driverFirstName: 'Ravi', pickupTime: '8:40 AM' });
  });

  it('flags a late driver once from the ETA, and the live view says so', async () => {
    const { rider, first } = await commuter();
    const driver = await departed(first.id, first.pickup);
    t.clock.now = thu('08:36');
    await sendLocation(driver, first.pickup.lat + 0.03, first.pickup.lng); // ~3.3 km: 16 min ETA → 8:52
    expect(await flagLateDrivers(t.ctx)).toBe(1);
    expect((await t.db.query('select late_flagged_at from rides where id = $1', [first.id])).rows[0].late_flagged_at).toEqual(thu('08:36'));
    expect((await get(`/v1/rides/${first.id}/live`, rider)).json().late).toBe(true);
    const late = (await t.db.query(`select count(*)::int as n from notifications where kind = 'driver_running_late'`)).rows[0].n;
    expect(late).toBe(1);
  });

  it('a driver who never departs is late once the window has been over 5 minutes', async () => {
    await commuter();
    t.clock.now = thu('08:50');
    expect(await flagLateDrivers(t.ctx)).toBe(0);
    t.clock.now = thu('08:51');
    expect(await flagLateDrivers(t.ctx)).toBe(1);
  });

  it('payment reminders two hours after drop, to whoever has not marked', async () => {
    const { rider, id } = await completedRide();
    await post(`/v1/rides/${id}/payment`, { method: 'cash' }, rider);
    t.clock.now = thu('11:19');
    expect(await queuePaymentReminders(t.ctx)).toBe(0);
    t.clock.now = thu('11:21');
    expect(await queuePaymentReminders(t.ctx)).toBe(1);
    const n = (await t.db.query(`select recipient_type from notifications where kind = 'payment_reminder'`)).rows;
    expect(n).toEqual([{ recipient_type: 'driver' }]);
  });

  it('closes rides stale for 3 hours', async () => {
    const { first } = await commuter();
    await departed(first.id, first.pickup);
    t.clock.now = new Date(thu('08:20').getTime() + 3 * 60 * MIN + MIN);
    expect(await closeStaleRides(t.ctx)).toBe(1);
    expect((await t.db.query('select state from rides where id = $1', [first.id])).rows[0].state).toBe('cancelled');
  });

  it('sends each notification once, to every device of the recipient, retrying failures', async () => {
    const { rider, first } = await commuter();
    expect((await put('/v1/devices', { pushToken: 'token-phone-0001', platform: 'android' }, rider)).statusCode).toBe(204);
    await departed(first.id, first.pickup);
    const sent: Array<[string, PushMessage]> = [];
    let fail = true;
    const flaky = {
      async send(token: string, m: PushMessage) {
        if (fail) throw new Error('FCM unavailable');
        sent.push([token, m]);
      },
    };
    expect(await sendDueNotifications(t.ctx, flaky)).toBe(0);
    fail = false;
    expect(await sendDueNotifications(t.ctx, flaky)).toBe(0); // backing off for a minute
    t.clock.now = new Date(t.clock.now.getTime() + MIN);
    expect(await sendDueNotifications(t.ctx, flaky)).toBe(1);
    expect(await sendDueNotifications(t.ctx, flaky)).toBe(0);
    expect(sent).toEqual([
      ['token-phone-0001', { title: 'Ravi is on the way', body: 'Track your ride live.', data: { kind: 'driver_on_the_way', rideId: first.id } }],
    ]);
  });
});

describe('simulator', () => {
  it('plays a whole ride through the driver rules', async () => {
    const { rider, first } = await commuter();
    t.clock.now = thu('08:25');
    // Each simulated tick moves the test clock 5 s instead of sleeping.
    const sleep = async (ms: number) => void (t.clock.now = new Date(t.clock.now.getTime() + ms * 600));
    await simPlay(t.ctx, first.id, { speed: 600, sleep });
    const ride = (await get(`/v1/rides/${first.id}`, rider)).json();
    expect(ride.state).toBe('completed');
    const track = (await t.db.query('select count(*)::int as n from ride_track where ride_id = $1', [first.id])).rows[0].n;
    expect(track).toBeGreaterThan(10);
  });

  it('dev routes step a ride and move the clock', async () => {
    await t.close();
    t = await startTestApp({ autoAcceptCommutes: true, enableDriverSim: true });
    const { rider, first } = await commuter();
    expect((await post('/dev/sim/clock', { now: at(thu('08:20')) })).json().now).toBe(at(thu('08:20')));
    const listed = (await get('/dev/sim/rides?riderPhone=9886054321')).json().rides;
    expect(listed[0].id).toBe(first.id);
    for (const move of ['depart', 'arrive', 'start', 'end']) {
      const res = await post(`/dev/sim/rides/${first.id}/step`, { move });
      expect(res.statusCode, `${move}: ${res.body}`).toBe(200);
    }
    expect((await get(`/v1/rides/${first.id}`, rider)).json().state).toBe('completed');
  });
});
