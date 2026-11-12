# Commute ride apps

Riders book regular daily or weekly commutes (and one-time rides) by bike, auto or cab, with one regular driver at a fixed fare per ride. Drivers pay a flat monthly plan fee instead of commission. Payments are off-platform in v1: riders pay drivers directly by UPI or cash.

Designs, flows and rules live in the design canvas and the Rider and Driver App specs.

## Repository layout

| Path | What it is |
|---|---|
| `apps/rider/` | Rider app (Flutter, Android + web) |
| `backend/` | API (Node 22, TypeScript, Fastify, Postgres) |
| `.github/workflows/` | CI: tests, screenshots, APK and web builds, emulator tests |

The driver app (`apps/driver/`) arrives with the driver flows.

## Build order

Flows are built and tested one at a time, rider first:

| Flow | Status |
|---|---|
| R1 Sign up and book a first commute | Built: app (demo mode) + backend |
| R2 Daily commute ride | Next |
| R3 Skip or pause, R4 Driver can't make it, R5 Account, R6 One-time ride | To do |
| D1–D6 Driver app | To do |

## Rider app

```sh
cd apps/rider
flutter create . --platforms=android,web --org com.uberauto --project-name rider   # first time only
rm -f test/widget_test.dart                                                          # created by flutter create
flutter pub get
flutter run                     # on a connected phone or emulator
flutter run -d chrome           # in the browser; use device mode at 390 x 844
```

**Demo mode.** The app currently runs against a built-in fake backend (`lib/core/fake_api.dart`) so every flow can be tried without a server:

* Sign in with any Indian mobile number; the code is always `123456`.
* Pick "Home" (search "HSR") and "Embassy Tech Village" (search "Embassy") for the demo commute.
* Choosing the **Woman driver** preference finds no driver, to show the "No driver yet" branch.

### Tests

```sh
flutter test --exclude-tags screenshots          # unit, widget and whole-flow tests
flutter test --tags screenshots --update-goldens # renders every screen to test/screenshots/goldens/
flutter test integration_test -d <device-id>     # the same flows on a phone or emulator
```

Each flow has:

1. **Unit tests** for its rules (`test/core/`).
2. **Widget tests** per screen (`test/features/`).
3. **A flow test** that walks the whole flow at three phone sizes (`test/flows/`).
4. **Screenshots** of every screen and state, uploaded by CI for visual review.
5. **An integration test** that runs the same steps on an Android emulator in CI, and on a real phone over wireless debugging (`integration_test/`).

The widget tests and the integration test share one "robot" (`test/helpers/r1_robot.dart`), so what passes in CI is exactly what runs on the phone.

## Backend

```sh
cd backend
npm install
export DATABASE_URL=postgres://app:app@localhost:5432/commute_dev
npm run seed          # migrate and load the demo city (same places and drivers as the app's demo mode)
FIXED_OTP=123456 npm run dev
npm test              # needs a Postgres database; see test/helpers.ts
```

R1 endpoints (all JSON; everything except `/v1/auth/*` needs `Authorization: Bearer <token>`):

| Method and path | Purpose |
|---|---|
| `POST /v1/auth/otp` | Send a sign-in code |
| `POST /v1/auth/verify` | Check the code, return a session token |
| `GET /v1/places?q=` | Search places |
| `POST /v1/trips/estimate` | Distance and minutes between two places |
| `POST /v1/booking-options` | Ride types the config allows for a booking flow, with fares |
| `POST /v1/commutes/proposals` | Match drivers and hold them for 15 minutes |
| `POST /v1/commutes` | Confirm a held proposal (nothing is paid) |
| `POST /v1/waitlist` | No driver yet: join the waitlist |
| `GET /v1/home` | The rider's current commute for Home |

Until the driver app's accept step (flow D2) exists, `AUTO_ACCEPT_COMMUTES` (default on) accepts commutes on the driver's behalf.
