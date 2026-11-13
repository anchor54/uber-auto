import { createHash, createHmac, randomBytes, randomInt } from 'node:crypto';
import type { FastifyInstance, FastifyRequest } from 'fastify';
import { z } from 'zod';
import type { AppContext } from '../context.js';
import { ApiError } from '../domain/types.js';

const phoneSchema = z.string().regex(/^[6-9]\d{9}$/, 'Enter a 10-digit mobile number');
const OTP_TTL_MS = 5 * 60_000;
const SESSION_TTL_MS = 30 * 24 * 60 * 60_000;
const MAX_ATTEMPTS = 5;

export type Role = 'rider' | 'driver';

const sha256 = (s: string) => createHash('sha256').update(s).digest('hex');

/** Codes are keyed with a server secret: a leaked table can't be reversed by trying all 10^6 codes. */
const codeHash = (ctx: AppContext, phone: string, code: string) =>
  createHmac('sha256', ctx.config.otpSecret).update(`${phone}:${code}`).digest('hex');

/** A bad number is `invalid_phone`, the same code the app's demo backend uses. */
export function parsePhone(body: unknown): { phone: string } {
  const r = z.object({ phone: phoneSchema }).safeParse(body);
  if (!r.success) throw new ApiError(400, 'invalid_phone', 'Enter a 10-digit mobile number');
  return r.data;
}

export interface OtpSender {
  send(phone: string, code: string): Promise<void>;
}

/** Logs codes instead of sending SMS. Replace with an SMS provider in production. */
export const consoleOtpSender: OtpSender = {
  async send(phone, code) {
    console.info(`[otp] ${phone}: ${code}`);
  },
};

const HOUR_MS = 3_600_000;

/**
 * Makes a code and sends it. Per phone: one send every
 * otpResendSeconds, at most otpMaxSendsPerHour an hour, and wrong guesses
 * count across resends until the hour is up.
 */
async function sendCode(ctx: AppContext, phone: string, role: Role) {
  const now = ctx.clock();
  const prev = (
    await ctx.db.query<{ sends: number; window_started_at: Date; last_sent_at: Date }>(
      'select sends, window_started_at, last_sent_at from otp_codes where phone = $1 and role = $2',
      [phone, role],
    )
  ).rows[0];
  const freshWindow = !prev || now.getTime() - prev.window_started_at.getTime() >= HOUR_MS;
  if (prev && !freshWindow) {
    const wait = Math.ceil((prev.last_sent_at.getTime() + ctx.config.otpResendSeconds * 1000 - now.getTime()) / 1000);
    if (wait > 0) throw new ApiError(429, 'resend_too_soon', `Wait ${wait} seconds before asking for another code.`);
    if (prev.sends >= ctx.config.otpMaxSendsPerHour) {
      throw new ApiError(429, 'too_many_codes', 'Too many codes asked for. Try again in an hour.');
    }
  }
  const code = ctx.config.fixedOtp ?? String(randomInt(0, 1_000_000)).padStart(6, '0');
  await ctx.db.query(
    `insert into otp_codes (phone, role, code_hash, expires_at, attempts, sends, window_started_at, last_sent_at)
     values ($1, $2, $3, $4, 0, 1, $5, $5)
     on conflict (phone, role) do update set
       code_hash = excluded.code_hash,
       expires_at = excluded.expires_at,
       last_sent_at = excluded.last_sent_at,
       sends = case when $6 then 1 else otp_codes.sends + 1 end,
       attempts = case when $6 then 0 else otp_codes.attempts end,
       window_started_at = case when $6 then excluded.window_started_at else otp_codes.window_started_at end`,
    [phone, role, codeHash(ctx, phone, code), new Date(now.getTime() + OTP_TTL_MS), now, freshWindow],
  );
  await ctx.otpSender.send(phone, code);
}

/** Checks the code and uses it up. Throws the same errors for riders and drivers. */
async function checkCode(ctx: AppContext, phone: string, role: Role, code: string) {
  const row = (
    await ctx.db.query<{ code_hash: string; expires_at: Date; attempts: number }>(
      'select code_hash, expires_at, attempts from otp_codes where phone = $1 and role = $2',
      [phone, role],
    )
  ).rows[0];
  if (!row || row.expires_at <= ctx.clock()) {
    throw new ApiError(401, 'code_expired', 'That code has expired. Ask for a new one.');
  }
  if (row.attempts >= MAX_ATTEMPTS) {
    throw new ApiError(429, 'too_many_attempts', 'Too many wrong codes. Try again in an hour.');
  }
  if (row.code_hash !== codeHash(ctx, phone, code)) {
    await ctx.db.query('update otp_codes set attempts = attempts + 1 where phone = $1 and role = $2', [phone, role]);
    throw new ApiError(401, 'invalid_code', "That code isn't right. Check the SMS and try again.");
  }
  await ctx.db.query('delete from otp_codes where phone = $1 and role = $2', [phone, role]);
}

async function newSession(ctx: AppContext, owner: { riderId: string } | { driverId: string }) {
  const token = randomBytes(32).toString('hex');
  const role: Role = 'riderId' in owner ? 'rider' : 'driver';
  await ctx.db.query('insert into sessions (token_hash, role, rider_id, driver_id, expires_at) values ($1, $2, $3, $4, $5)', [
    sha256(token),
    role,
    'riderId' in owner ? owner.riderId : null,
    'driverId' in owner ? owner.driverId : null,
    new Date(ctx.clock().getTime() + SESSION_TTL_MS),
  ]);
  return token;
}

const verifySchema = z.object({ phone: phoneSchema, code: z.string().regex(/^\d{6}$/) });

export function authRoutes(app: FastifyInstance, ctx: AppContext) {
  app.post('/v1/auth/otp', async (req, reply) => {
    const { phone } = parsePhone(req.body);
    await sendCode(ctx, phone, 'rider');
    return reply.code(204).send();
  });

  app.post('/v1/auth/verify', async (req) => {
    const { phone, code } = verifySchema.parse(req.body);
    await checkCode(ctx, phone, 'rider', code);
    const rider = (
      await ctx.db.query<{ id: string; phone: string; name: string | null }>(
        `insert into riders (phone) values ($1)
         on conflict (phone) do update set phone = excluded.phone
         returning id, phone, name`,
        [phone],
      )
    ).rows[0]!;
    return { token: await newSession(ctx, { riderId: rider.id }), rider };
  });

  // Drivers are onboarded by ops; an unknown phone gets no code (and no SMS).
  app.post('/v1/driver/auth/otp', async (req, reply) => {
    const { phone } = parsePhone(req.body);
    const known = (await ctx.db.query('select 1 from drivers where phone = $1 and active', [phone])).rowCount;
    if (!known) throw new ApiError(403, 'not_a_driver', "This number isn't registered as a driver.");
    await sendCode(ctx, phone, 'driver');
    return reply.code(204).send();
  });

  app.post('/v1/driver/auth/verify', async (req) => {
    const { phone, code } = verifySchema.parse(req.body);
    await checkCode(ctx, phone, 'driver', code);
    const driver = (
      await ctx.db.query<{ id: string; name: string; phone: string }>('select id, name, phone from drivers where phone = $1 and active', [
        phone,
      ])
    ).rows[0];
    if (!driver) throw new ApiError(403, 'not_a_driver', "This number isn't registered as a driver.");
    return { token: await newSession(ctx, { driverId: driver.id }), driver };
  });
}

async function sessionFor(ctx: AppContext, req: FastifyRequest) {
  const header = req.headers.authorization ?? '';
  const token = header.startsWith('Bearer ') ? header.slice(7) : '';
  if (!token) throw new ApiError(401, 'unauthorized', 'Sign in first');
  const row = (
    await ctx.db.query<{ role: Role; rider_id: string | null; driver_id: string | null }>(
      'select role, rider_id, driver_id from sessions where token_hash = $1 and expires_at > $2',
      [sha256(token), ctx.clock()],
    )
  ).rows[0];
  if (!row) throw new ApiError(401, 'unauthorized', 'Your session has ended. Sign in again.');
  return row;
}

/** The signed-in rider or driver. */
export async function requireSession(ctx: AppContext, req: FastifyRequest): Promise<{ role: Role; id: string }> {
  const s = await sessionFor(ctx, req);
  return { role: s.role, id: (s.role === 'rider' ? s.rider_id : s.driver_id)! };
}

/** Resolves the signed-in rider from `Authorization: Bearer <token>`. */
export async function requireRider(ctx: AppContext, req: FastifyRequest): Promise<string> {
  const s = await sessionFor(ctx, req);
  if (s.role !== 'rider' || !s.rider_id) throw new ApiError(403, 'riders_only', 'This needs a rider account.');
  return s.rider_id;
}

/** Resolves the signed-in driver. */
export async function requireDriver(ctx: AppContext, req: FastifyRequest): Promise<string> {
  const s = await sessionFor(ctx, req);
  if (s.role !== 'driver' || !s.driver_id) throw new ApiError(403, 'drivers_only', 'This needs a driver account.');
  return s.driver_id;
}
