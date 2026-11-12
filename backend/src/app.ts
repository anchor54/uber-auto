import Fastify, { type FastifyInstance } from 'fastify';
import { ZodError } from 'zod';
import type { AppContext } from './context.js';
import { ApiError } from './domain/types.js';
import { authRoutes } from './modules/auth.js';
import { bookingRoutes } from './modules/booking.js';
import { commuteRoutes } from './modules/commutes.js';

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

  app.get('/health', async () => ({ ok: true }));
  authRoutes(app, ctx);
  bookingRoutes(app, ctx);
  commuteRoutes(app, ctx);
  return app;
}
