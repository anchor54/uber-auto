import type { Db } from './db/pool.js';
import type { OtpSender } from './modules/auth.js';
import { cityAreas, demoMaps, type CityArea, type MapsProvider } from './modules/maps.js';

export interface AppConfig {
  /** Every rider is in this city until multi-city sign-up exists. */
  city: string;
  /** Rider local time zone offset, for "tomorrow" and start dates. IST = 330. */
  utcOffsetMinutes: number;
  /** Fixed OTP for demo and tests. Never set in production. */
  fixedOtp?: string;
  /**
   * Accept commutes on the driver's behalf the moment the rider confirms.
   * For demos without a driver; off by default and refused in production.
   */
  autoAcceptCommutes: boolean;
  /** How long a driver has to accept a new commute (D2). */
  driverResponseHours: number;
  /** Registers the /dev/sim routes that act as drivers. Never in production. */
  enableDriverSim: boolean;
  /** Key for hashing sign-in codes. Must be set (and secret) in production. */
  otpSecret: string;
  /** Seconds between two codes for the same phone. */
  otpResendSeconds: number;
  /** Codes one phone can ask for in an hour. */
  otpMaxSendsPerHour: number;
  /** Web origins allowed to call the API from a browser (the web build). */
  corsOrigins: string[];
  /** How long a proposed driver is held while the rider confirms. */
  proposalHoldMinutes: number;
}

export interface AppContext {
  db: Db;
  config: AppConfig;
  clock: () => Date;
  otpSender: OtpSender;
  /** Dev simulator only: moves [clock] to [now]. */
  setClock?: (now: Date) => void;
  /** Place search and trip estimates; the demo city when not given. */
  maps?: MapsProvider;
}

export function mapsOf(ctx: AppContext): MapsProvider {
  return (ctx.maps ??= demoMaps(ctx.db));
}

export function cityArea(ctx: AppContext): CityArea {
  const area = cityAreas[ctx.config.city];
  if (!area) throw new Error(`No map area for city ${ctx.config.city}`);
  return area;
}

export const defaultConfig: AppConfig = {
  city: 'blr',
  utcOffsetMinutes: 330,
  autoAcceptCommutes: false,
  proposalHoldMinutes: 15,
  driverResponseHours: 12,
  enableDriverSim: false,
  otpSecret: 'dev-only-otp-secret',
  otpResendSeconds: 30,
  otpMaxSendsPerHour: 5,
  corsOrigins: [],
};

/**
 * Reads the config from the environment. In production (NODE_ENV=production)
 * the demo switches are refused: a fixed code would let anyone sign in, and
 * auto-accept or the simulator would confirm drivers who never agreed.
 */
export function configFromEnv(env: NodeJS.ProcessEnv = process.env): AppConfig {
  const config: AppConfig = {
    ...defaultConfig,
    fixedOtp: env.FIXED_OTP || undefined,
    autoAcceptCommutes: env.AUTO_ACCEPT_COMMUTES === 'true',
    enableDriverSim: env.ENABLE_DRIVER_SIM === 'true',
    driverResponseHours: Number(env.DRIVER_RESPONSE_HOURS ?? defaultConfig.driverResponseHours),
    otpSecret: env.OTP_SECRET || defaultConfig.otpSecret,
    corsOrigins: (env.CORS_ORIGINS ?? '').split(',').map((s) => s.trim()).filter(Boolean),
  };
  if (env.NODE_ENV === 'production') {
    const unsafe = [
      config.fixedOtp && 'FIXED_OTP',
      config.autoAcceptCommutes && 'AUTO_ACCEPT_COMMUTES',
      config.enableDriverSim && 'ENABLE_DRIVER_SIM',
    ].filter(Boolean);
    if (unsafe.length) throw new Error(`Refusing to start in production with ${unsafe.join(', ')} set`);
    if (!env.OTP_SECRET || env.OTP_SECRET.length < 32) {
      throw new Error('Refusing to start in production without OTP_SECRET (32+ random characters)');
    }
  }
  return config;
}
