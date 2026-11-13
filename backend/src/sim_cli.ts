/**
 * Plays the driver against a running backend started with ENABLE_DRIVER_SIM=true.
 *
 *   npm run sim -- requests                 list commutes waiting for a driver
 *   npm run sim -- accept  [commuteId]      accept (default: the newest request)
 *   npm run sim -- decline [commuteId]      decline
 *   npm run sim -- expire  [commuteId]      act as if the driver never answered
 *
 *   npm run sim -- rides [riderPhone]       open rides, soonest first
 *   npm run sim -- ride <rideId|next> <depart|arrive|start|end|no_show|contact>
 *   npm run sim -- play <rideId|next> [speed]   play a whole ride (default 30× real time)
 *   npm run sim -- clock <ISO time | pickup-<minutes> >   move the server clock
 *   npm run sim -- jobs                     run the worker jobs once
 *
 * SIM_URL picks the backend (default http://localhost:8080).
 */
const base = process.env.SIM_URL ?? 'http://localhost:8080';

interface Request {
  id: string;
  driverId: string;
  pickup: { title: string };
  drop: { title: string };
  pickupTime: number;
  startDate: string;
  fare: number;
}

async function call(method: string, path: string, body: unknown = {}): Promise<unknown> {
  const res = await fetch(`${base}${path}`, {
    method,
    headers: method === 'POST' ? { 'content-type': 'application/json' } : {},
    body: method === 'POST' ? JSON.stringify(body) : undefined,
  });
  const json = await res.json().catch(() => ({}));
  if (!res.ok) {
    const hint = res.status === 404 && path.startsWith('/dev/sim') ? ' (is the backend running with ENABLE_DRIVER_SIM=true?)' : '';
    throw new Error(`${method} ${path} → ${res.status} ${JSON.stringify(json)}${hint}`);
  }
  return json;
}

const hhmm = (m: number) => `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;

async function requests(): Promise<Request[]> {
  return ((await call('GET', '/dev/sim/requests')) as { requests: Request[] }).requests;
}

interface Ride {
  id: string;
  date: string;
  direction: string;
  state: string;
  pickupAt: string;
  pickup: { title: string };
  drop: { title: string };
  driver: { name: string };
}

async function rides(riderPhone?: string): Promise<Ride[]> {
  const q = riderPhone ? `?riderPhone=${riderPhone}` : '';
  return ((await call('GET', `/dev/sim/rides${q}`)) as { rides: Ride[] }).rides;
}

async function rideId(arg: string | undefined): Promise<Ride> {
  const list = await rides();
  const r = !arg || arg === 'next' ? list[0] : list.find((x) => x.id === arg);
  if (!r) throw new Error(arg && arg !== 'next' ? `No open ride ${arg}` : 'No open rides.');
  return r;
}

async function rideCommand(command: string, args: string[]): Promise<boolean> {
  switch (command) {
    case 'rides': {
      const list = await rides(args[0]);
      if (!list.length) console.log('No open rides.');
      for (const r of list) {
        console.log(`${r.id}  ${r.date} ${r.direction.padEnd(8)} ${r.state.padEnd(10)} ${new Date(r.pickupAt).toISOString()}  ${r.pickup.title} → ${r.drop.title}  ${r.driver.name}`);
      }
      return true;
    }
    case 'ride': {
      const r = await rideId(args[0]);
      const move = args[1];
      if (!move) throw new Error('Say which move: depart, arrive, start, end, no_show or contact');
      const out = (await call('POST', `/dev/sim/rides/${r.id}/step`, { move })) as Ride;
      console.log(`${out.id}: ${out.state}`);
      return true;
    }
    case 'play': {
      const r = await rideId(args[0]);
      console.log(JSON.stringify(await call('POST', `/dev/sim/rides/${r.id}/play`, { speed: Number(args[1] ?? 30) })));
      return true;
    }
    case 'clock': {
      const arg = args[0];
      if (!arg) throw new Error('Give an ISO time, or pickup-<minutes> to jump to before the next pickup');
      const m = /^pickup-(\d+)$/.exec(arg);
      const now = m ? new Date(Date.parse((await rideId('next')).pickupAt) - Number(m[1]) * 60_000).toISOString() : arg;
      console.log(JSON.stringify(await call('POST', '/dev/sim/clock', { now })));
      return true;
    }
    case 'jobs':
      console.log(JSON.stringify(await call('POST', '/dev/sim/jobs')));
      return true;
  }
  return false;
}

async function main() {
  const [command = 'requests', id, ...rest] = process.argv.slice(2);
  if (await rideCommand(command, [id, ...rest].filter((x): x is string => x !== undefined))) return;
  if (command === 'requests') {
    const list = await requests();
    if (!list.length) console.log('No commutes waiting for a driver.');
    for (const r of list) {
      console.log(`${r.id}  ${r.driverId}  ${r.pickup.title} → ${r.drop.title}  ${hhmm(r.pickupTime)} from ${r.startDate}  ₹${r.fare}`);
    }
    return;
  }
  if (!['accept', 'decline', 'expire'].includes(command)) {
    throw new Error(`Unknown command "${command}". Use requests, accept, decline, expire, rides, ride, play, clock or jobs.`);
  }
  const target = id ?? (await requests()).at(-1)?.id;
  if (!target) throw new Error('No commutes waiting for a driver.');
  console.log(JSON.stringify(await call('POST', `/dev/sim/commutes/${target}/${command}`)));
}

main().catch((e: Error) => {
  console.error(e.message);
  process.exit(1);
});
