import { buildApp } from './app.js';
import { defaultConfig } from './context.js';
import { migrate } from './db/migrate.js';
import { createPool } from './db/pool.js';
import { consoleOtpSender } from './modules/auth.js';

const db = createPool();
await migrate(db);

const app = buildApp(
  {
    db,
    clock: () => new Date(),
    otpSender: consoleOtpSender,
    config: {
      ...defaultConfig,
      fixedOtp: process.env.FIXED_OTP || undefined,
      autoAcceptCommutes: process.env.AUTO_ACCEPT_COMMUTES !== 'false',
    },
  },
  { logger: true },
);

await app.listen({ host: '0.0.0.0', port: Number(process.env.PORT ?? 8080) });
