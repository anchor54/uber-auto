import { createHash, randomBytes, randomInt } from 'node:crypto';
import type { FastifyInstance, FastifyRequest } from 'fastify';
import { z } from 'zod';
import type { AppContext } from '../context.js';
import { ApiError } from '../domain/types.js';

const phoneSchema = z.string().regex(/^[6-9]\d{9}$/, 'Enter a 10-digit mobile number');
const OTP_TTL_MS = 5 * 60_000;
const SESSION_TTL_MS = 30 * 24 * 60 * 60_000;
const MAX_ATTEMPTS = 5;

const sha256 = (s: string) => createHash('sha256').update(s).digest('hex');

export interface OtpSender {
  send(phone: string, code: string): Promise<void>;
}

/** Logs codes instead of sending SMS. Replace with an SMS provider in production. */
export const consoleOtpSender: OtpSender = {
  async send(phone, code) {
    console.info(`[otp] ${phone}: ${code}`);
  },
};

export function authRoutes(app: FastifyInstance, ctx: AppContext) {
  app.post('/v1/auth/otp', async (req, reply) => {
    const { phone } = z.object({ phone: phoneSchema }).parse(req.body);
    const code = ctx.config.fixedOtp ?? String(randomInt(0, 1_000_000)).padStart(6, '0');
    await ctx.db.query(
      `insert into otp_codes (phone, code_hash, expires_at, attempts) values ($1, $2, $3, 0)
       on conflict (phone) do update set code_hash = excluded.code_hash, expires_at = excluded.expires_at, attempts = 0`,
      [phone, sha256(code), new Date(ctx.clock().getTime() + OTP_TTL_MS)],
    );
    await ctx.otpSender.send(phone, code);
    return reply.code(204).send();
  });

  app.post('/v1/auth/verify', async (req) => {
    const { phone, code } = z.object({ phone: phoneSchema, code: z.string().regex(/^\d{6}$/) }).parse(req.body);
    const row = (
      await ctx.db.query<{ code_hash: string; expires_at: Date; attempts: number }>(
        'select code_hash, expires_at, attempts from otp_codes where phone = $1',
        [phone],
      )
    ).rows[0];
    if (!row || row.expires_at <= ctx.clock()) {
      throw new ApiError(401, 'code_expired', 'That code has expired. Ask for a new one.');
    }
    if (row.attempts >= MAX_ATTEMPTS) {
      throw new ApiError(429, 'too_many_attempts', 'Too many wrong codes. Ask for a new one.');
    }
    if (row.code_hash !== sha256(code)) {
      await ctx.db.query('update otp_codes set attempts = attempts + 1 where phone = $1', [phone]);
      throw new ApiError(401, 'invalid_code', "That code isn't right. Check the SMS and try again.");
    }
    await ctx.db.query('delete from otp_codes where phone = $1', [phone]);
    const rider = (
      await ctx.db.query<{ id: string; phone: string; name: string | null }>(
        `insert into riders (phone) values ($1)
         on conflict (phone) do update set phone = excluded.phone
         returning id, phone, name`,
        [phone],
      )
    ).rows[0]!;
    const token = randomBytes(32).toString('hex');
    await ctx.db.query('insert into sessions (token_hash, rider_id, expires_at) values ($1, $2, $3)', [
      sha256(token),
      rider.id,
      new Date(ctx.clock().getTime() + SESSION_TTL_MS),
    ]);
    return { token, rider };
  });
}

/** Resolves the signed-in rider from `Authorization: Bearer <token>`. */
export async function requireRider(ctx: AppContext, req: FastifyRequest): Promise<string> {
  const header = req.headers.authorization ?? '';
  const token = header.startsWith('Bearer ') ? header.slice(7) : '';
  if (!token) throw new ApiError(401, 'unauthorized', 'Sign in first');
  const row = (
    await ctx.db.query<{ rider_id: string }>('select rider_id from sessions where token_hash = $1 and expires_at > $2', [
      sha256(token),
      ctx.clock(),
    ])
  ).rows[0];
  if (!row) throw new ApiError(401, 'unauthorized', 'Your session has ended. Sign in again.');
  return row.rider_id;
}
