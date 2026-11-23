# Commute ride apps

Riders book regular daily or weekly commutes (and one-time rides) by bike, auto or cab, with one regular driver at a fixed fare per ride. Drivers pay a flat monthly plan fee instead of commission. Payments are off-platform in v1: riders pay drivers directly by UPI or cash.

Designs, flows and rules live in the design canvas and the Rider and Driver App specs.

## Repository layout

| Path | What it is |
|---|---|
| `apps/rider/` | Rider app (Flutter, Android + web) |
| `backend/` | API and worker (Go, Postgres) |
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

Go 1.24+, Postgres 16 (with the `btree_gist` extension). Being rewritten to the R1–R6 / D1–D6 specs on
branch `spec-rewrite`; R1+D2 (series request, driver interest, the pick) is done, the daily ride
(R2+D3) comes next.

```sh
cd backend
export DATABASE_URL=postgres://app:app@localhost:5432/commute_dev
go run ./cmd/db seed   # wipe and load the spec world: rider Priya, auto drivers Ravi, Meena, Anil, Suresh
FIXED_OTP=123456 ENABLE_DEV_ROUTES=true RUN_SWEEPER_IN_API=true go run ./cmd/server
go test ./...          # needs Postgres: DATABASE_URL, else postgres://app:app@localhost:5432/commute_test
./build.sh             # the production binaries in bin/: server, worker, db, sim
```

Layout: `cmd/` holds the four programs. `internal/travel` (distance, travel time, fares) and
`internal/scheduling` (weekdays, start dates, arrive-by, the clash check) are pure rules;
`internal/offers` is the series request and the pick; `internal/sweeper` the timed work;
`internal/notify` the outbox and FCM; `internal/httpapi` the HTTP API; `internal/store` the pool and
migrations (embedded); `internal/seed` config and the spec seed; `internal/testkit` runs the API
in-process for `test/integration`. Every endpoint is in [`backend/API.md`](backend/API.md).

| Variable | Meaning |
|---|---|
| `DATABASE_URL` | Postgres connection string (`DB_SIMPLE_PROTOCOL=true` behind a transaction pooler) |
| `FIXED_OTP` | Dev only: every sign-in code is this |
| `ENABLE_DEV_ROUTES` | Dev only: `POST /dev/clock/advance`, so the simulator can close offers at once |
| `RUN_SWEEPER_IN_API` | Run the sweeper inside the API every `SWEEP_EVERY_MS` (default a minute) |
| `SWEEP_TOKEN` | When set, `POST /internal/sweep` needs `Authorization: Bearer <token>` |
| `HEARTBEAT_URL` | Pinged after every sweep (Healthchecks.io) |
| `OTP_SECRET` | Key for hashing sign-in codes; required in production |
| `MSG91_AUTH_KEY`, `MSG91_OTP_TEMPLATE_ID` | Send codes by SMS (else codes are logged) |
| `OLA_MAPS_API_KEY` | Place search and the app's street map from Ola Maps (else the demo places) |
| `MAP_TILE_CACHE_MB` | Size of the map tile cache (default 64; `0` turns it off) |
| `CORS_ORIGINS` | Comma-separated web origins allowed to call the API |
| `FCM_SERVICE_ACCOUNT_JSON` | A Firebase service account's JSON key, to send pushes through FCM (else they are logged) |

With `APP_ENV=production` the server refuses to start with `FIXED_OTP` or `ENABLE_DEV_ROUTES`, or
without `OTP_SECRET`.

**Processes.** `cmd/server` is ride-api. Timed work is the sweeper's `RunOnce`: close offers whose
window has passed and run the pick, keep rides created 14 days ahead, send queued pushes; each run is
a `sweeper_runs` row. In production Cloud Scheduler calls `POST /internal/sweep` every minute; on
Render, `cmd/worker` runs the same loop.

**Playing the drivers** (until the driver app exists):

```sh
go run ./cmd/sim offers --drivers ravi
go run ./cmd/sim interested --series <id or prefix> --drivers ravi,meena,anil
go run ./cmd/sim close-offers          # move the server clock 15 min (dev routes) and sweep now
```

## Going live

1. **Render.** Create an account, then *New → Blueprint* and pick this repository: `render.yaml` creates the API, the worker and Postgres in Singapore. Enter the keys it asks for (below). Then, once, open the API's *Shell* and run `./bin/db seed-config` to load fare rules and ride types (the server also does this on start). It never deletes data. The blueprint starts as **staging** (`APP_ENV=staging`): there is no SMS yet, so each sign-in code is written to the API's log in Render, where you read it to sign in.
2. **SMS (MSG91), before real riders.** Register your sender and an OTP template on DLT through MSG91; the template text must contain `##OTP##`. In `render.yaml`, add `MSG91_AUTH_KEY` and `MSG91_OTP_TEMPLATE_ID` to `commute-api` (as `sync: false`) and set `APP_ENV` to `production` on both services; Render then asks for the two keys. Production refuses to start without them. Codes are limited to one every 30 seconds and five an hour per phone, and wrong guesses count across resends.
3. **Maps (Ola Maps).** Create a project in the Ola Maps console and put its key in `OLA_MAPS_API_KEY`. The backend uses it for place search, road distances and routes, and serves the app's map (`/v1/map/…`: Ola's style trimmed to the base map, with tiles, fonts and sprite passed through and cached), so the key never reaches phones. Check Ola's terms on caching tiles; `MAP_TILE_CACHE_MB=0` turns the cache off.
4. **App.** In GitHub, set the repository variable `API_BASE_URL` to the API's URL (e.g. `https://commute-api.onrender.com`). CI then builds the APK and the web app in real mode.
5. **Web app.** In GitHub *Settings → Pages*, choose *Deploy from a branch* → `gh-pages`. CI publishes the web build there on every push to `main`.
6. **Push notifications** through the Firebase project `recurring-ride-share` (Android app `com.uberauto.rider`).
   - **App:** nothing to do. The project's client settings are built in (`lib/core/fcm_push.dart`); they aren't secrets, and no `google-services.json` is needed. To use another project, set the GitHub repository variables `FIREBASE_API_KEY`, `FIREBASE_APP_ID`, `FIREBASE_SENDER_ID` and `FIREBASE_PROJECT_ID` (or pass them as `--dart-define`s locally).
   - **Worker:** in the Firebase console, *Project settings → Service accounts → Generate new private key*, and paste the whole JSON file into the worker's `FCM_SERVICE_ACCOUNT_JSON` on Render. This one is a secret: never commit it.

   Riders are asked to allow notifications once they have a commute. Tapping one opens that ride. Without Firebase, notifications are only logged and riders see changes in the app within 5 seconds.
7. **Driver app.** Real riders shouldn't get "Driver confirmed" from a stand-in: drivers need the driver app (or ops acting through `/v1/driver/*`) before launch.
