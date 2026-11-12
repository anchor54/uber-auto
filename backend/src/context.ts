import type { Db } from './db/pool.js';
import type { OtpSender } from './modules/auth.js';

export interface AppConfig {
  /** Every rider is in this city until multi-city sign-up exists. */
  city: string;
  /** Rider local time zone offset, for "tomorrow" and start dates. IST = 330. */
  utcOffsetMinutes: number;
  /** Fixed OTP for demo and tests. Never set in production. */
  fixedOtp?: string;
  /**
   * Accept commutes on the driver's behalf. Stands in for the driver app's
   * accept step (flow D2) until it exists.
   */
  autoAcceptCommutes: boolean;
  /** How long a proposed driver is held while the rider confirms. */
  proposalHoldMinutes: number;
}

export interface AppContext {
  db: Db;
  config: AppConfig;
  clock: () => Date;
  otpSender: OtpSender;
}

export const defaultConfig: AppConfig = {
  city: 'blr',
  utcOffsetMinutes: 330,
  autoAcceptCommutes: true,
  proposalHoldMinutes: 15,
};
