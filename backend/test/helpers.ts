import type { FastifyInstance } from 'fastify';
import { buildApp } from '../src/app.js';
import { defaultConfig, type AppConfig, type AppContext } from '../src/context.js';
import { createPool, type Db } from '../src/db/pool.js';
import { demoPlaces, seedDemoData } from '../src/db/seed.js';

export const DEMO_OTP = '123456';

/** Wednesday 21 Oct 2026, 9:00 AM IST: the same moment the app's tests use. */
export const testNow = new Date('2026-10-21T03:30:00Z');

export const place = (id: string) => {
  const p = demoPlaces.find((x) => x.id === id);
  if (!p) throw new Error(`no demo place ${id}`);
  return p;
};

export function commuteRequest(overrides: Record<string, unknown> = {}) {
  return {
    pickup: place('home'),
    drop: place('etv'),
    repeat: 'every_weekday',
    weekdays: [1, 2, 3, 4, 5],
    timing: 'pickup_at',
    time: 520,
    returnTrip: true,
    returnTime: 1110,
    rideType: 'cab_sedan',
    preferences: ['ac_on'],
    ...overrides,
  };
}

export interface TestApp {
  app: FastifyInstance;
  db: Db;
  ctx: AppContext;
  clock: { now: Date };
  sentCodes: Array<{ phone: string; code: string }>;
  signIn(phone?: string): Promise<string>;
  close(): Promise<void>;
}

let shared: Db | undefined;

export async function startTestApp(config: Partial<AppConfig> = {}): Promise<TestApp> {
  shared ??= createPool(process.env.DATABASE_URL ?? 'postgres://app:app@localhost:5432/commute_test');
  const db = shared;
  await seedDemoData(db);
  const clock = { now: testNow };
  const sentCodes: Array<{ phone: string; code: string }> = [];
  const ctx: AppContext = {
    db,
    clock: () => clock.now,
    setClock: (now) => void (clock.now = now),
    otpSender: { send: async (phone, code) => void sentCodes.push({ phone, code }) },
    // Tests sign the same phone in many times; the send limits have their own tests.
    config: { ...defaultConfig, fixedOtp: DEMO_OTP, otpResendSeconds: 0, otpMaxSendsPerHour: 1000, ...config },
  };
  const app = buildApp(ctx);
  await app.ready();
  return {
    app,
    db,
    ctx,
    clock,
    sentCodes,
    async signIn(phone = '9886054321') {
      await app.inject({ method: 'POST', url: '/v1/auth/otp', payload: { phone } });
      const res = await app.inject({ method: 'POST', url: '/v1/auth/verify', payload: { phone, code: DEMO_OTP } });
      return res.json().token as string;
    },
    async close() {
      await app.close();
    },
  };
}

export async function closeDb() {
  await shared?.end();
  shared = undefined;
}

export const bearer = (token: string) => ({ authorization: `Bearer ${token}` });
