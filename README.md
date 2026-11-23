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
| D1 Driver registration, verification and subscription | Backend built (driver and admin API, `cmd/admin`, live-and-paid eligibility in matching); driver app screens to do |
| D3–D6 Driver app screens | To do (D3's API is built) |

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

Go 1.24+, Postgres 16.

```sh
cd backend
export DATABASE_URL=postgres://app:app@localhost:5432/commute_dev
go run ./cmd/db seed   # wipe and load the demo city (same places and drivers as the app's demo mode)
FIXED_OTP=123456 ENABLE_DRIVER_SIM=true RUN_JOBS_IN_API=true go run ./cmd/server
go test ./...          # needs Postgres: DATABASE_URL, else postgres://app:app@localhost:5432/commute_test
./build.sh             # the production binaries in bin/: server, worker, db, sim, admin
```

Layout: `cmd/` holds the five programs; `internal/domain` the pure rules (dates, fares, the ride plan, the ride lifecycle, polylines); `internal/db` the pool, migrations (embedded in the binaries) and seeds; `internal/app` the HTTP API, its services and the worker jobs.

| Variable | Meaning |
|---|---|
| `DATABASE_URL` | Postgres connection string |
| `FIXED_OTP` | Dev only: every sign-in code is this |
| `ENABLE_DRIVER_SIM` | Dev only: the `/dev/sim` routes that play the driver |
| `RUN_JOBS_IN_API` | Dev only: run the worker jobs inside the API (they then share the simulator's clock) |
| `AUTO_ACCEPT_COMMUTES` | Demo only: accept commutes on the driver's behalf. Off by default |
| `OTP_SECRET` | Key for hashing sign-in codes; required in production |
| `MSG91_AUTH_KEY`, `MSG91_OTP_TEMPLATE_ID` | Send codes by SMS; required in production (else codes are logged) |
| `OLA_MAPS_API_KEY` | Place search, road distances and routes, and the app's street map, from Ola Maps (else the demo places, straight-line estimate and a drawn map) |
| `MAPS_PROVIDER` | `demo` keeps the demo places even with an Ola key (the contract and end-to-end tests need them) |
| `MAP_TILE_CACHE_MB` | Size of the backend's map tile cache (default 64; `0` turns it off) |
| `CORS_ORIGINS` | Comma-separated web origins allowed to call the API |
| `FCM_SERVICE_ACCOUNT_JSON` | Worker: a Firebase service account's JSON key, to send push notifications through FCM (else they are logged) |
| `ADMIN_PHONES` | Comma-separated phones that may sign in as admin (D1 review and payments) |
| `PLATFORM_UPI_ID` | Where drivers pay their subscription (default `commute.plans@okaxis`) |
| `SUPABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY` | Driver documents in Supabase Storage's private `driver-docs` bucket, through 10-minute signed links; required in production (else an in-memory store served by the API at `/storage/…`, lost on restart) |

With `APP_ENV=production` (or `NODE_ENV=production`) the server refuses to start with any of the demo switches on, or without `OTP_SECRET`, SMS and Supabase Storage.

**Processes.** `cmd/server` is the API. `cmd/worker` is the worker: every 30 seconds it creates rides 14 days ahead (nightly), queues "Leg is on", flags late drivers, queues payment reminders, closes stale rides and sends notifications; once a day it deletes ride tracking points older than 30 days. Each job is safe to run twice.

**Playing the driver** (backend running with `ENABLE_DRIVER_SIM=true`):

```sh
go run ./cmd/sim requests              # commutes waiting for a driver (D2)
go run ./cmd/sim accept                # accept the newest (or: decline, expire)
go run ./cmd/sim rides                 # open rides, soonest first
go run ./cmd/sim clock pickup-20       # move the server clock to 20 min before the next pickup
go run ./cmd/sim play next 30          # play the next ride at 30× speed
go run ./cmd/sim ride next depart      # or one move: arrive, start, end, no_show, contact
```

**Reviewing drivers (D1)** with the admin tool, against `API_URL` (default `http://localhost:8080`) as a phone listed in `ADMIN_PHONES`:

```sh
go run ./cmd/admin login 9000000099            # asks for the code; saves the token to ~/.commute-admin-token (or set ADMIN_TOKEN)
go run ./cmd/admin pending                     # drivers waiting for review and payments claimed, oldest first
go run ./cmd/admin show drv_…                  # every item, with 10-minute links to the files
go run ./cmd/admin verify drv_… licence        # or: reject-item drv_… licence "photo blurred" (items: profile_photo, id_proof,
                                               #   licence, rc, insurance, permit, upi_id, vehicle_front, vehicle_back, vehicle_interior, vehicle)
go run ./cmd/admin approve drv_…               # or: reject drv_… "<reason>"
go run ./cmd/admin confirm-payment <payment>   # or: reject-payment <payment> "<reason>"; confirming makes the driver live
```

Only drivers whose status is `live` and whose `subscription_until` is today or later are proposed to riders, and never for a commute that runs on their weekly day off. Drivers seeded by ops are live and paid until 2099-12-31.

### API

All JSON; everything except the `auth` routes and `GET /v1/driver/signup-config` needs `Authorization: Bearer <token>`. Rider routes need a rider session, driver routes a driver session, admin routes an admin session. Errors are `{error, message}`, and a 409 also carries `state` (the object as it is now). The D1 contract is in `docs/flows/d1-driver-registration.md`.

| Area | Routes |
|---|---|
| Sign-in | `POST /v1/auth/otp`, `/v1/auth/verify`; drivers (any phone; verify says `hasProfile`): `POST /v1/driver/auth/otp`, `/verify`; admins (`ADMIN_PHONES` only): `POST /v1/admin/auth/otp`, `/verify` |
| Driver sign-up (D1) | `GET /v1/driver/signup-config`; `POST /v1/driver/register`; `GET /v1/driver/me`; `POST /v1/driver/items/:kind/upload-url`, `/v1/driver/items/:kind/uploaded`; `PUT /v1/driver/upi`, `/v1/driver/vehicle`, `/v1/driver/day-off`; `POST /v1/driver/submit`; `GET /v1/driver/plan`; `POST /v1/driver/payments` |
| Admin (D1) | `GET /v1/admin/pending`, `/v1/admin/drivers/:id`; `POST /v1/admin/drivers/:id/items/:item/review`, `/approve`, `/reject`; `POST /v1/admin/payments/:id/confirm`, `/reject` |
| Booking (R1) | `GET /v1/places?q=`, `POST /v1/trips/estimate`, `/v1/booking-options`, `/v1/commutes/proposals`, `/v1/commutes`, `/v1/waitlist`, `GET /v1/home` |
| Driver answer (D2) | `GET /v1/driver/requests`, `POST /v1/driver/commutes/:id/accept`, `/decline` |
| Rides, rider (R2) | `GET /v1/rides/upcoming?days=`, `/v1/rides/unsettled`, `/v1/rides/:id`, `/v1/rides/:id/live`; `POST /v1/rides/:id/running-late`, `/payment`, `/rating`; `PUT /v1/devices` (riders, registered drivers and admins) |
| Rides, driver (D3 API) | `GET /v1/driver/run?date=`, `PUT /v1/driver/duty`, `POST /v1/driver/location`, `POST /v1/driver/rides/:id/depart`, `/arrive`, `/start`, `/end`, `/contact`, `/no-show`, `/payment` |

The ride lifecycle is one pure function (`Transition` in `backend/internal/domain/rides.go`); every move writes a `ride_events` row.

## Going live

1. **Render.** Create an account, then *New → Blueprint* and pick this repository: `render.yaml` creates the API, the worker and Postgres in Singapore. Enter the keys it asks for (below). Then, once, open the API's *Shell* and run `./bin/db seed-config` to load fare rules, ride types and places (add `--demo-drivers` for the demo drivers). It never deletes data. The blueprint starts as **staging** (`APP_ENV=staging`): there is no SMS yet, so each sign-in code is written to the API's log in Render, where you read it to sign in.
2. **SMS (MSG91), before real riders.** Register your sender and an OTP template on DLT through MSG91; the template text must contain `##OTP##`. In `render.yaml`, add `MSG91_AUTH_KEY` and `MSG91_OTP_TEMPLATE_ID` to `commute-api` (as `sync: false`) and set `APP_ENV` to `production` on both services; Render then asks for the two keys. Production refuses to start without them. It also needs driver document storage: in Supabase create a **private** bucket `driver-docs` (file size limit 5 MB, allowed types `image/jpeg, image/png`) and set `SUPABASE_URL` and `SUPABASE_SERVICE_ROLE_KEY` (a secret) on `commute-api`; and `ADMIN_PHONES` for whoever reviews drivers. Codes are limited to one every 30 seconds and five an hour per phone, and wrong guesses count across resends.
3. **Maps (Ola Maps).** Create a project in the Ola Maps console and put its key in `OLA_MAPS_API_KEY`. The backend uses it for place search, road distances and routes, and serves the app's map (`/v1/map/…`: Ola's style trimmed to the base map, with tiles, fonts and sprite passed through and cached), so the key never reaches phones. Check Ola's terms on caching tiles; `MAP_TILE_CACHE_MB=0` turns the cache off.
4. **App.** In GitHub, set the repository variable `API_BASE_URL` to the API's URL (e.g. `https://commute-api.onrender.com`). CI then builds the APK and the web app in real mode.
5. **Web app.** In GitHub *Settings → Pages*, choose *Deploy from a branch* → `gh-pages`. CI publishes the web build there on every push to `main`.
6. **Push notifications** through the Firebase project `recurring-ride-share` (Android app `com.uberauto.rider`).
   - **App:** nothing to do. The project's client settings are built in (`lib/core/fcm_push.dart`); they aren't secrets, and no `google-services.json` is needed. To use another project, set the GitHub repository variables `FIREBASE_API_KEY`, `FIREBASE_APP_ID`, `FIREBASE_SENDER_ID` and `FIREBASE_PROJECT_ID` (or pass them as `--dart-define`s locally).
   - **Worker:** in the Firebase console, *Project settings → Service accounts → Generate new private key*, and paste the whole JSON file into the worker's `FCM_SERVICE_ACCOUNT_JSON` on Render. This one is a secret: never commit it.

   Riders are asked to allow notifications once they have a commute. Tapping one opens that ride. Without Firebase, notifications are only logged and riders see changes in the app within 5 seconds.
7. **Driver app.** Real riders shouldn't get "Driver confirmed" from a stand-in: drivers need the driver app (or ops acting through `/v1/driver/*`) before launch.
