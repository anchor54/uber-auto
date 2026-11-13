import type { Db } from './db/pool.js';
import { consoleOtpSender, type OtpSender } from './modules/auth.js';
import { olaMapStyle, type MapStyle } from './modules/map_style.js';
import { demoMaps, olaMaps, type MapsProvider } from './modules/maps.js';
import { msg91OtpSender } from './modules/sms.js';

/**
 * The outside services, chosen from the environment:
 *
 *   SMS   MSG91_AUTH_KEY + MSG91_OTP_TEMPLATE_ID → MSG91; else codes are logged
 *   Maps  OLA_MAPS_API_KEY → Ola Maps; else the seeded demo city.
 *         MAPS_PROVIDER=demo forces the demo city (tests that need its places).
 *         MAP_TILE_CACHE_MB sizes the map tile cache (default 64; 0 turns it off).
 *
 * In production, logged codes would let anyone with log access sign in as
 * anyone, so the server refuses to start without SMS.
 */
export function providersFromEnv(env: NodeJS.ProcessEnv, db: Db, log: (m: string) => void = console.warn) {
  const production = env.NODE_ENV === 'production';

  let otpSender: OtpSender = consoleOtpSender;
  if (env.MSG91_AUTH_KEY && env.MSG91_OTP_TEMPLATE_ID) {
    otpSender = msg91OtpSender({ authKey: env.MSG91_AUTH_KEY, templateId: env.MSG91_OTP_TEMPLATE_ID });
  } else if (production) {
    throw new Error('Refusing to start in production without MSG91_AUTH_KEY and MSG91_OTP_TEMPLATE_ID');
  }

  let maps: MapsProvider = demoMaps(db);
  let mapStyle: MapStyle | undefined;
  if (env.OLA_MAPS_API_KEY && env.MAPS_PROVIDER !== 'demo') {
    maps = olaMaps(env.OLA_MAPS_API_KEY);
    mapStyle = olaMapStyle(env.OLA_MAPS_API_KEY, { cacheMb: Number(env.MAP_TILE_CACHE_MB ?? 64) });
  } else if (production) {
    log('OLA_MAPS_API_KEY is not set: using the seeded demo places and straight-line distances');
  }
  return { otpSender, maps, mapStyle };
}
