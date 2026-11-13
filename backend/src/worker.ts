/**
 * The worker: everything that runs on a clock, one loop every 30 seconds.
 * In production it is its own process (`npm run worker`); the API never
 * runs these jobs there.
 */
import { configFromEnv } from './context.js';
import { migrate } from './db/migrate.js';
import { createPool } from './db/pool.js';
import { consoleOtpSender } from './modules/auth.js';
import { startJobLoop } from './modules/jobs.js';
import { logPushSender } from './modules/notify.js';

const EVERY_MS = Number(process.env.WORKER_EVERY_MS ?? 30_000);

const config = configFromEnv();
const db = createPool();
await migrate(db);
// Real time only. When the dev simulator moves the API's clock, run the jobs
// inside the API instead (RUN_JOBS_IN_API=true) so both share that clock.
const ctx = { db, config, clock: () => new Date(), otpSender: consoleOtpSender };

console.info(`[worker] running jobs every ${EVERY_MS / 1000}s`);
const stop = startJobLoop(ctx, logPushSender, EVERY_MS);
const shutdown = async () => {
  stop();
  await db.end();
  process.exit(0);
};
process.on('SIGTERM', shutdown);
process.on('SIGINT', shutdown);
