import type { FastifyInstance } from 'fastify';
import { z } from 'zod';
import type { AppContext } from '../context.js';
import { ApiError } from '../domain/types.js';
import { expireUnanswered, pendingRequests, respondToCommute } from './driver_requests.js';
import { runJobs } from './jobs.js';
import { logPushSender } from './notify.js';
import { simMoves, simPlay, simStep } from './ride_sim.js';
import { riderRideJson, type RideRow } from './rides.js';

/**
 * Dev-only driver simulator, registered only when ENABLE_DRIVER_SIM=true
 * (never in production: configFromEnv refuses it). It acts as whichever
 * driver was asked, through the same service functions as the driver
 * endpoints, so what it exercises is what the driver app will do.
 *
 *   GET  /dev/sim/requests                 commutes waiting for a driver
 *   POST /dev/sim/commutes/:id/accept      the driver accepts
 *   POST /dev/sim/commutes/:id/decline     the driver declines
 *   POST /dev/sim/commutes/:id/expire      the driver never answers
 *
 *   GET  /dev/sim/rides?riderPhone=        open rides, soonest first
 *   POST /dev/sim/rides/:id/step {move}    depart | arrive | start | end | no_show | contact
 *   POST /dev/sim/rides/:id/play {speed}   plays the whole ride in the background
 *   POST /dev/sim/clock {now}              moves the server clock (API process only)
 *   POST /dev/sim/jobs                     runs the worker jobs once, now
 */
export function devSimRoutes(app: FastifyInstance, ctx: AppContext) {
  const idParam = z.object({ id: z.string().uuid() });

  app.get('/dev/sim/requests', async () => ({ requests: await pendingRequests(ctx, null) }));

  app.post('/dev/sim/commutes/:id/accept', async (req) => respondToCommute(ctx, null, idParam.parse(req.params).id, 'accept'));

  app.post('/dev/sim/commutes/:id/decline', async (req) => respondToCommute(ctx, null, idParam.parse(req.params).id, 'decline'));

  app.post('/dev/sim/commutes/:id/expire', async (req) => {
    const { id } = idParam.parse(req.params);
    await ctx.db.query(`update commutes set respond_by = $2 where id = $1 and status = 'awaiting_driver'`, [id, ctx.clock()]);
    await expireUnanswered(ctx);
    const row = (await ctx.db.query<{ status: string }>('select status from commutes where id = $1', [id])).rows[0];
    return { id, status: row?.status ?? null };
  });

  app.get('/dev/sim/rides', async (req) => {
    const { riderPhone } = z.object({ riderPhone: z.string().optional() }).parse(req.query);
    const rows = (
      await ctx.db.query<RideRow>(
        `select r.*, to_char(r.ride_date, 'YYYY-MM-DD') as ride_date from rides r join riders p on p.id = r.rider_id
         where r.state in ('scheduled', 'on_the_way', 'arrived', 'in_ride') and ($1::text is null or p.phone = $1)
         order by r.pickup_at limit 20`,
        [riderPhone ?? null],
      )
    ).rows;
    return { rides: await Promise.all(rows.map((r) => riderRideJson(ctx, r))) };
  });

  app.post('/dev/sim/rides/:id/step', async (req) => {
    const { move } = z.object({ move: z.enum(simMoves) }).parse(req.body);
    return riderRideJson(ctx, await simStep(ctx, idParam.parse(req.params).id, move));
  });

  app.post('/dev/sim/rides/:id/play', async (req, reply) => {
    const { speed } = z.object({ speed: z.number().min(1).max(600).default(30) }).parse(req.body ?? {});
    void simPlay(ctx, idParam.parse(req.params).id, { speed }, (m) => app.log.warn(m));
    return reply.code(202).send({ playing: true, speed });
  });

  app.post('/dev/sim/clock', async (req) => {
    if (!ctx.setClock) throw new ApiError(409, 'clock_fixed', 'This server cannot move its clock');
    const { now } = z.object({ now: z.string().datetime() }).parse(req.body);
    ctx.setClock(new Date(now));
    return { now: ctx.clock().toISOString() };
  });

  app.post('/dev/sim/jobs', async () => runJobs(ctx, logPushSender));
}
