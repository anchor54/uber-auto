import { buildApp } from './app.js';
import { configFromEnv } from './context.js';
import { migrate } from './db/migrate.js';
import { createPool } from './db/pool.js';
import { startJobLoop } from './modules/jobs.js';
import { logPushSender } from './modules/notify.js';
import { providersFromEnv } from './providers.js';

const config = configFromEnv(); // Throws before touching the database if unsafe.
const db = createPool();
const { otpSender, maps, mapStyle } = providersFromEnv(process.env, db);
await migrate(db);

// The dev simulator can move this clock (POST /dev/sim/clock) to replay a
// ride at any time of day. Without the simulator it is real time.
let offsetMs = 0;
const ctx = {
  db,
  clock: () => new Date(Date.now() + offsetMs),
  setClock: config.enableDriverSim ? (now: Date) => void (offsetMs = now.getTime() - Date.now()) : undefined,
  otpSender,
  maps,
  mapStyle,
  config,
};
const app = buildApp(ctx, { logger: true });

// Production runs the worker as its own process (npm run worker). For local
// and end-to-end runs it can share this process, and so this clock.
if (process.env.RUN_JOBS_IN_API === 'true') {
  if (process.env.NODE_ENV === 'production') throw new Error('Run the worker as its own process in production');
  startJobLoop(ctx, logPushSender, Number(process.env.WORKER_EVERY_MS ?? 30_000), {
    info: (m) => app.log.info(m),
    error: (m) => app.log.error(m),
  });
}

await app.listen({ host: '0.0.0.0', port: Number(process.env.PORT ?? 8080) });
