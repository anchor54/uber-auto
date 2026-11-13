import Fastify, { type FastifyInstance } from 'fastify';
import { ZodError } from 'zod';
import type { AppContext } from './context.js';
import { ApiError } from './domain/types.js';
import { authRoutes } from './modules/auth.js';
import { bookingRoutes } from './modules/booking.js';
import { commuteRoutes } from './modules/commutes.js';
import { devSimRoutes } from './modules/dev_sim.js';
import { driverRequestRoutes } from './modules/driver_requests.js';
import { rideRoutes } from './modules/rides.js';

export function buildApp(ctx: AppContext, opts: { logger?: boolean } = {}): FastifyInstance {
  const app = Fastify({ logger: opts.logger ?? false });

  app.setErrorHandler((err, _req, reply) => {
    if (err instanceof ZodError) {
      return reply.code(400).send({
        error: 'invalid_request',
        message: err.issues[0]?.message ?? 'Invalid request',
        issues: err.issues,
      });
    }
    if (err instanceof ApiError) {
      return reply.code(err.status).send({ error: err.code, message: err.message });
    }
    const status = (err as { statusCode?: number }).statusCode;
    if (status && status < 500) {
      return reply.code(status).send({ error: 'bad_request', message: (err as Error).message });
    }
    app.log.error(err);
    return reply.code(500).send({ error: 'internal', message: 'Something went wrong' });
  });

  // The web build calls the API from another origin (GitHub Pages).
  const allowed = new Set(ctx.config.corsOrigins);
  app.addHook('onRequest', async (req, reply) => {
    const origin = req.headers.origin;
    if (!origin || !allowed.has(origin)) return;
    reply.header('access-control-allow-origin', origin);
    reply.header('vary', 'origin');
    if (req.method === 'OPTIONS') {
      reply.header('access-control-allow-methods', 'GET, POST, PUT, OPTIONS');
      reply.header('access-control-allow-headers', 'authorization, content-type');
      reply.header('access-control-max-age', '600');
      return reply.code(204).send();
    }
  });

  app.get('/health', async () => ({ ok: true }));
  authRoutes(app, ctx);
  bookingRoutes(app, ctx);
  commuteRoutes(app, ctx);
  driverRequestRoutes(app, ctx);
  rideRoutes(app, ctx);
  if (ctx.config.enableDriverSim) devSimRoutes(app, ctx);
  return app;
}
