import type pg from 'pg';
import type { AppContext } from '../context.js';

/**
 * Notifications are written to an outbox (the `notifications` table) and sent
 * by the worker. The table is unique on (kind, ride, recipient), so queueing
 * the same one twice does nothing and a restart can't double-send.
 */

export type NotificationKind =
  | 'leg_is_on'
  | 'driver_on_the_way'
  | 'driver_arrived'
  | 'driver_running_late'
  | 'driver_alert'
  | 'rider_running_late'
  | 'payment_reminder';

export interface Recipient {
  type: 'rider' | 'driver';
  id: string;
}

type Queryable = Pick<pg.Pool, 'query'> | pg.PoolClient;

export async function queueNotification(
  db: Queryable,
  n: { to: Recipient; kind: NotificationKind; rideId: string; sendAfter: Date; payload?: Record<string, unknown> },
): Promise<void> {
  await db.query(
    `insert into notifications (recipient_type, recipient_id, kind, ride_id, payload, send_after)
     values ($1, $2, $3, $4, $5, $6)
     on conflict (kind, ride_id, recipient_type, recipient_id) do nothing`,
    [n.to.type, n.to.id, n.kind, n.rideId, JSON.stringify(n.payload ?? {}), n.sendAfter],
  );
}

export interface PushMessage {
  title: string;
  body: string;
  /** Lets the app open the right screen from a tap. */
  data: { kind: string; rideId: string };
}

/** Sends one push to one device. FCM in production; logs in development. */
export interface PushSender {
  send(token: string, message: PushMessage): Promise<void>;
}

export const logPushSender: PushSender = {
  async send(token, m) {
    console.info(`[push] ${token.slice(0, 12)}…: ${m.title}: ${m.body}`);
  },
};

/** Text for each kind. [p] is the payload queued with it. */
export function messageFor(kind: string, p: Record<string, unknown>): { title: string; body: string } {
  const driver = String(p.driverFirstName ?? 'Your driver');
  const time = String(p.pickupTime ?? '');
  switch (kind) {
    case 'leg_is_on':
      return { title: 'Your ride is on', body: `${driver} picks you up at ${time}.` };
    case 'driver_on_the_way':
      return { title: `${driver} is on the way`, body: 'Track your ride live.' };
    case 'driver_arrived':
      return { title: `${driver} has arrived`, body: 'Your driver is waiting at the pickup point.' };
    case 'driver_running_late':
      return { title: `${driver} is running late`, body: 'Open the app to see the new arrival time.' };
    case 'driver_alert':
      return { title: `${driver} is waiting for you`, body: 'Your driver is at the pickup point.' };
    case 'rider_running_late':
      return { title: 'Your rider is 5 min late', body: 'Their wait now runs 10 minutes.' };
    case 'payment_reminder':
      return { title: 'How was this ride paid?', body: 'Mark the fare as paid so both of you have a record.' };
    default:
      return { title: 'Commute', body: 'Open the app for an update.' };
  }
}

export const MAX_TRIES = 5;

/**
 * Worker job: send due notifications to every device of the recipient.
 * Rows are claimed with FOR UPDATE SKIP LOCKED so two workers never send the
 * same one. A failure is retried with backoff (1, 2, 4, 8 minutes), up to 5 tries.
 */
export async function sendDueNotifications(ctx: AppContext, sender: PushSender, limit = 50): Promise<number> {
  const client = await ctx.db.connect();
  let sent = 0;
  try {
    await client.query('begin');
    const due = (
      await client.query<{ id: string; recipient_type: string; recipient_id: string; kind: string; ride_id: string; payload: Record<string, unknown>; tries: number }>(
        `select id, recipient_type, recipient_id, kind, ride_id, payload, tries from notifications
         where sent_at is null and send_after <= $1 and tries < $2
         order by send_after limit $3 for update skip locked`,
        [ctx.clock(), MAX_TRIES, limit],
      )
    ).rows;
    for (const n of due) {
      const tokens = (
        await client.query<{ push_token: string }>('select push_token from devices where owner_type = $1 and owner_id = $2', [
          n.recipient_type,
          n.recipient_id,
        ])
      ).rows.map((r) => r.push_token);
      const m = messageFor(n.kind, n.payload);
      try {
        for (const token of tokens) await sender.send(token, { ...m, data: { kind: n.kind, rideId: n.ride_id } });
        // No device is not an error: the rider sees the state in the app.
        await client.query('update notifications set sent_at = $2, tries = tries + 1 where id = $1', [n.id, ctx.clock()]);
        sent++;
      } catch (e) {
        const backoff = 2 ** n.tries * 60_000;
        await client.query('update notifications set tries = tries + 1, last_error = $2, send_after = $3 where id = $1', [
          n.id,
          (e as Error).message.slice(0, 500),
          new Date(ctx.clock().getTime() + backoff),
        ]);
      }
    }
    await client.query('commit');
  } catch (e) {
    await client.query('rollback');
    throw e;
  } finally {
    client.release();
  }
  return sent;
}
