# Commute ride apps

Riders book regular daily or weekly commutes (and one-time rides) by bike, auto or cab, with one regular driver at a fixed fare per ride. Drivers pay a flat monthly plan fee instead of commission. Payments are off-platform in v1: riders pay drivers directly by UPI or cash.

Designs, flows and rules live in the design canvas and the Rider and Driver App specs.

## Repository layout

| Path | What it is |
|---|---|
| `apps/rider/` | Rider app (Flutter, Android + web) |
| `backend/` | API and worker (Node 22, TypeScript, Fastify, Postgres) |
| `render.yaml` | Hosting: API, worker and Postgres on Render |
| `.github/workflows/` | CI: tests, screenshots, APK and web builds, emulator tests, end-to-end tests |

The driver app (`apps/driver/`) arrives with the driver flows; its API exists already.

## Build order

| Flow | Status |
|---|---|
| R1 Sign up and book a first commute | Built: app + backend, demo and real mode |
| D2 Driver accepts or declines a commute | Built: backend API + simulator; rider sees waiting / confirmed / declined |
| R2 Daily commute ride | Built: rides, lifecycle, live location, payment marks, ratings, worker; push is log-only until a Firebase project exists |
| R3 Skip or pause, R4 Driver can't make it, R5 Account, R6 One-time ride | To do |
| D1, D3–D6 Driver app screens | To do (D3's API is built) |

## Rider app

```sh
cd apps/rider
flutter pub get
flutter run                                                    # demo mode
flutter run --dart-define=API_BASE_URL=http://10.0.2.2:8080    # real backend (emulator → this machine)
```

**Demo mode** (no `API_BASE_URL`) runs on a built-in fake backend (`lib/core/fake_api.dart`), so every flow works without a server:

* Sign in with any Indian mobile number; the code is always `123456`.
* Pick "Home" (search "HSR") and "Embassy Tech Village" (search "Embassy") for the demo commute.
* The demo driver takes 8 seconds to accept (you'll see "Waiting for Ravi"). 20 seconds later the first ride plays itself out at 20× speed: driver on the way, arrived, in the ride, pay and rate.
* Choosing the **Woman driver** preference finds no driver, to show the "No driver yet" branch.

**Real mode** (`--dart-define=API_BASE_URL=…`) talks to the backend over HTTP (`lib/core/http_api.dart`) and keeps the rider signed in across restarts (session token in Android Keystore / iOS Keychain). Debug builds may use plain HTTP to a dev machine; release builds need HTTPS. On a phone over USB or wireless debugging, `adb reverse tcp:8080 tcp:8080` lets the app use `http://127.0.0.1:8080`.

### Tests

```sh
flutter test --exclude-tags screenshots          # unit, widget and whole-flow tests
flutter test --tags screenshots --update-goldens # renders every screen to test/screenshots/goldens/
RIDER_API_URL=http://localhost:8080 flutter test --tags contract   # same API script, demo vs real backend
flutter test integration_test -d <device>        # flows on a phone or emulator (demo mode)
flutter test integration_test/r1_e2e_test.dart integration_test/r2_e2e_test.dart \
  -d <device> --dart-define=API_BASE_URL=http://10.0.2.2:8080   # flows against the real backend
```

Each flow has unit tests (`test/core/`, `test/features/ride/ride_tracker_test.dart`), widget tests per screen (`test/features/`), a flow test at three phone sizes (`test/flows/`), screenshots, and integration tests. The widget tests and the on-device runs share "robots" (`test/helpers/r1_robot.dart`, `r2_robot.dart`).

The **contract test** (`test/contract/`) runs one script of `RiderApi` calls against the demo backend and the real one, so the two can't drift apart unnoticed.

## Backend

```sh
cd backend
npm install
export DATABASE_URL=postgres://app:app@localhost:5432/commute_dev
npm run seed      # wipe and load the demo city (same places and drivers as the app's demo mode)
FIXED_OTP=123456 ENABLE_DRIVER_SIM=true RUN_JOBS_IN_API=true npm run dev
npm test          # needs Postgres; see test/helpers.ts
```

| Variable | Meaning |
|---|---|
| `DATABASE_URL` | Postgres connection string |
| `FIXED_OTP` | Dev only: every sign-in code is this |
| `ENABLE_DRIVER_SIM` | Dev only: the `/dev/sim` routes that play the driver |
| `RUN_JOBS_IN_API` | Dev only: run the worker jobs inside the API (they then share the simulator's clock) |
| `AUTO_ACCEPT_COMMUTES` | Demo only: accept commutes on the driver's behalf. Off by default |
| `OTP_SECRET` | Key for hashing sign-in codes; required in production |
| `MSG91_AUTH_KEY`, `MSG91_OTP_TEMPLATE_ID` | Send codes by SMS; required in production (else codes are logged) |
| `OLA_MAPS_API_KEY` | Place search and road distances from Ola Maps (else the demo places and straight-line estimate) |
| `CORS_ORIGINS` | Comma-separated web origins allowed to call the API |

With `NODE_ENV=production` the server refuses to start with any of the demo switches on, or without `OTP_SECRET` and SMS.

**Processes.** `npm run dev` / `npm start` is the API. `npm run worker` is the worker: every 30 seconds it creates rides 14 days ahead (nightly), queues "Leg is on", flags late drivers, queues payment reminders, closes stale rides and sends notifications. Each job is safe to run twice.

**Playing the driver** (backend running with `ENABLE_DRIVER_SIM=true`):

```sh
npm run sim -- requests                 # commutes waiting for a driver (D2)
npm run sim -- accept                   # accept the newest (or: decline, expire)
npm run sim -- rides                    # open rides, soonest first
npm run sim -- clock pickup-20          # move the server clock to 20 min before the next pickup
npm run sim -- play next 30             # play the next ride at 30× speed
npm run sim -- ride next depart         # or one move: arrive, start, end, no_show, contact
```

### API

All JSON; everything except the `auth` routes needs `Authorization: Bearer <token>`. Rider routes need a rider session, driver routes a driver session.

| Area | Routes |
|---|---|
| Sign-in | `POST /v1/auth/otp`, `/v1/auth/verify`; drivers: `POST /v1/driver/auth/otp`, `/verify` |
| Booking (R1) | `GET /v1/places?q=`, `POST /v1/trips/estimate`, `/v1/booking-options`, `/v1/commutes/proposals`, `/v1/commutes`, `/v1/waitlist`, `GET /v1/home` |
| Driver answer (D2) | `GET /v1/driver/requests`, `POST /v1/driver/commutes/:id/accept`, `/decline` |
| Rides, rider (R2) | `GET /v1/rides/upcoming?days=`, `/v1/rides/unsettled`, `/v1/rides/:id`, `/v1/rides/:id/live`; `POST /v1/rides/:id/running-late`, `/payment`, `/rating`; `PUT /v1/devices` |
| Rides, driver (D3 API) | `GET /v1/driver/run?date=`, `PUT /v1/driver/duty`, `POST /v1/driver/location`, `POST /v1/driver/rides/:id/depart`, `/arrive`, `/start`, `/end`, `/contact`, `/no-show`, `/payment` |

The ride lifecycle is one pure function (`src/domain/rides.ts`); every move writes a `ride_events` row.

## Going live

1. **Render.** Create an account, then *New → Blueprint* and pick this repository: `render.yaml` creates the API, the worker and Postgres in Singapore. Enter the keys it asks for (below). Then, once, open the API's *Shell* and run `npm run seed:config:prod` to load fare rules, ride types and places (add `-- --demo-drivers` for the demo drivers). It never deletes data.
2. **SMS (MSG91).** Register your sender and an OTP template on DLT through MSG91; the template text must contain `##OTP##`. Put `MSG91_AUTH_KEY` and the template id (`MSG91_OTP_TEMPLATE_ID`) into Render. Codes are limited to one every 30 seconds and five an hour per phone, and wrong guesses count across resends.
3. **Maps (Ola Maps).** Create a project in the Ola Maps console and put its key in `OLA_MAPS_API_KEY`. The adapter (`src/modules/maps.ts`) was written from Ola's API documentation and unit-tested against recorded shapes; try a search and a booking on staging before launch.
4. **App.** In GitHub, set the repository variable `API_BASE_URL` to the API's URL (e.g. `https://commute-api.onrender.com`). CI then builds the APK and the web app in real mode.
5. **Web app.** In GitHub *Settings → Pages*, choose *Deploy from a branch* → `gh-pages`. CI publishes the web build there on every push to `main`.
6. **Push notifications** (later): create a Firebase project, add the Android app, and implement `PushService` (`lib/core/push.dart`) and an FCM `PushSender` (`src/modules/notify.ts`). Until then notifications are logged and riders see changes in the app within 5 seconds.
7. **Driver app.** Real riders shouldn't get "Driver confirmed" from a stand-in: drivers need the driver app (or ops acting through `/v1/driver/*`) before launch.
