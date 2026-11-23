# D1 Driver registration, verification and subscription — backend contract

Source spec: *D1: Driver Registration, Verification and Subscription* (Nov 23, 2026).
This file is how that spec maps onto **this** repository. Where the two differ,
this file wins for the code; each difference is listed under *Deviations*.

Scope here: build-order steps 1–4 (migration, driver endpoints, admin endpoints
and `cmd/admin`, eligibility in matching). The driver app screens (steps 5–6) come next.

## Deviations from the spec (and why)

| Spec | Here | Why |
|---|---|---|
| `ride-api` on Cloud Run + `ride-sweeper` | `cmd/server` + `cmd/worker` (Render) | What the repo runs today. The worker's `SendDueNotifications` is the "sweeper sends outbox rows". |
| `users` table with role | `riders` and `drivers` tables (as today); sessions carry the role (`rider`, `driver`, `admin`) and the phone | Keeps R1/R2/D2 untouched |
| `events` table | `driver_events` | `ride_events` already exists for rides |
| `notification_outbox` | the existing `notifications` outbox (`ride_id` null for D1 kinds) | Same pattern, already sent by the worker |
| Error `{code, message, state}` | `{error, message, state}` (+ `missing` / `items` where noted) | The apps already read `error` |
| `401 wrong_code` | `401 invalid_code` (and `code_expired`, `too_many_attempts`) | Existing sign-in codes the rider app uses |
| Offers fanned out to drivers | Matching proposals (`findCandidates`) | The repo has no offer fan-out yet; "recipient of an offer" = "proposed as a candidate" |
| Tests in `test/integration`, goose | `internal/app/*_test.go`, embedded migrations | Repo convention (real Postgres, in-process handler, test clock) |
| Supabase Storage only | `Storage` interface: Supabase in staging/prod, in-memory store served by the API in dev/tests | No Supabase project wired yet; prod refuses the in-memory store |

## Data (migration `007_driver_registration.sql`)

`drivers` gains: `status` (`signing_up, submitted, changes_needed, approved, live, paused, rejected`),
`reject_reason`, `reference` (unique, `DRV-` + 5 digits), `day_off` (ISO weekday 1=Mon … 7=Sun, or null),
`day_off_set` (bool: a choice was made; `day_off` null + `day_off_set` = "No fixed day off"),
`subscription_until` (date), `version` (int, +1 on every driver update), `terms_accepted_at`, `created_at`.
Existing (ops-seeded) drivers become `live` with `subscription_until = 2099-12-31`; the demo seed does the same.
Registered drivers get `base` = city centre (12.9716, 77.5946), `service_radius_km` 50, work hours 0–1440
(the design dropped areas and working hours), `rating` 5.0, `ride_type`/`vehicle`/`plate`/`upi_id` filled when set.

New tables: `vehicles` (one per driver; `plate` display form, `plate_key` unique = upper-case with spaces and
hyphens removed; `review_state` uploaded/verified/rejected), `driver_items` (key driver_id+kind; `storage_path`
null for `upi_id`), `plans` (`ride_type` pk, `vehicle_type`, `cab_class`, `monthly_price`), `subscription_payments`
(partial unique index on `(driver_id, period_start) where status in ('claimed','confirmed')`), `driver_events`.
`sessions.role`, `otp_codes.role`, `devices.owner_type`, `notifications.recipient_type` all gain `admin`.

Default plan prices (₹/month, seeded config, editable in the table): bike 499, auto 799, cab_hatchback 1499,
cab_sedan 1499, cab_suv 1999.

### Items

Order everywhere: `profile_photo, id_proof, licence, rc, insurance, permit, upi_id` (the seven documents),
then `vehicle_front, vehicle_back, vehicle_interior` (vehicle photos). File kinds = all but `upi_id`.
Reviewable items = those ten plus `vehicle` (the vehicle details).
Labels: Profile photo, ID proof, Driving licence, Vehicle RC, Insurance, Permit, UPI ID, Front photo, Back photo,
Interior photo, Vehicle details.

Submit needs: every one of the ten items present, the vehicle set, and a day-off choice. Missing list order:
the ten kinds in order, then `vehicle`, then `day_off`.

**Locked**: status is not `signing_up`; except in `changes_needed`, where an item (or `vehicle`) whose state is
`rejected` can be replaced. Day off is locked outside `signing_up`.

## Driver JSON (`driverState`)

```json
{
  "id": "drv_…", "name": "Ravi Kumar", "phone": "9876500001", "city": "blr",
  "status": "signing_up", "rejectReason": null, "reference": "DRV-20417",
  "dayOff": 7, "dayOffSet": true, "subscriptionUntil": null,
  "items": [ {"kind": "profile_photo", "label": "Profile photo", "state": "missing|uploaded|verified|rejected",
              "rejectReason": null, "value": null} ],          // value = the UPI ID for upi_id
  "vehicle": {"type": "auto", "cabClass": null, "makeModel": "Bajaj RE", "year": 2022, "plate": "KA 05 AB 1234",
              "colour": "Green", "ac": false, "cngOrElectric": true, "reviewState": "uploaded", "rejectReason": null} | null,
  "plan": {"rideType": "auto", "vehicleType": "auto", "cabClass": null, "name": "Auto plan", "monthlyPrice": 799} | null,
  "latestPayment": {"id": "uuid", "periodStart": "2026-11-21", "periodEnd": "2026-11-30", "amount": 267,
                    "status": "claimed", "claimedAt": "…Z", "rejectReason": null} | null,
  "missing": ["insurance", "day_off"]
}
```
Admin's view (`GET /v1/admin/drivers/{id}`) adds `url` (signed read link, 10 min) to each file item and to nothing else.

## Endpoints

All JSON. Driver routes need a driver session, admin routes an admin session (else `403 drivers_only` /
`403 admins_only`; no token `401 unauthorized`). 409s carry `state` (the driver, or the payment where noted).

| Route | Body | 2xx | Errors |
|---|---|---|---|
| `POST /v1/driver/auth/otp` | phone | 204 (any valid phone now) | 400 invalid_phone, 429 resend_too_soon / too_many_codes |
| `POST /v1/driver/auth/verify` | phone, code | 200 `{token, hasProfile, driver: driverState\|null}` | 401 invalid_code… |
| `GET /v1/driver/signup-config` | — (no auth) | 200 `{cities:[{id:"blr",name:"Bengaluru"}], items:[{kind,label,hint}]}` | — |
| `POST /v1/driver/register` | name, city, termsAccepted | 201 driverState (`signing_up`) | 400 invalid_request, 409 phone_in_use_as_rider, 409 already_registered |
| `GET /v1/driver/me` | — | 200 driverState | 404 not_registered (signed in, no profile yet) |
| `POST /v1/driver/items/{kind}/upload-url` | contentType (`image/jpeg`\|`image/png`) | 200 `{uploadUrl, method:"PUT", headers:{content-type}, path, expiresAt}` | 400, 409 locked |
| `POST /v1/driver/items/{kind}/uploaded` | path | 200 `{item, driver}` | 403 not_your_path, 409 locked, 422 not_uploaded (no object at path) |
| `PUT /v1/driver/upi` | upiId | 200 `{item, driver}` | 422 bad_upi, 409 locked |
| `PUT /v1/driver/vehicle` | type, cabClass, makeModel, year, plate, colour, ac, cngOrElectric | 200 `{vehicle, driver}` | 400, 409 plate_in_use, 409 locked |
| `PUT /v1/driver/day-off` | dayOff (1–7 or null) | 200 driverState | 400, 409 locked |
| `POST /v1/driver/submit` | — | 200 driverState (`submitted`) | 409 incomplete (+`missing`), 409 wrong_status |
| `GET /v1/driver/plan` | — | 200 `{plan, amountDueNow, periodStart, periodEnd, reference, payTo:{upiId}, latestPayment}` | 409 not_approved |
| `POST /v1/driver/payments` | periodStart | 201 `{payment, driver}` (`claimed`) | 409 already_claimed (state = existing payment), 409 not_approved, 422 wrong_period |
| `POST /v1/admin/auth/otp`, `/verify` | phone[, code] | 204 / 200 `{token}` | 403 not_an_admin (phone not in `ADMIN_PHONES`) |
| `GET /v1/admin/pending` | — | 200 `{drivers:[{id,name,phone,city,status,submittedAt}], payments:[{id,driverId,driverName,reference,periodStart,periodEnd,amount,claimedAt}]}` oldest first | 403 |
| `GET /v1/admin/drivers/{id}` | — | 200 driverState with `url`s | 403, 404 not_found |
| `POST /v1/admin/drivers/{id}/items/{item}/review` | decision (`verified`\|`rejected`), reason (required if rejected) | 200 `{item, driver}` | 400, 404, 409 wrong_status (driver not `submitted`) |
| `POST /v1/admin/drivers/{id}/approve` | — | 200 driverState (`approved`) | 409 items_not_verified (+`items`), 409 wrong_status |
| `POST /v1/admin/drivers/{id}/reject` | reason | 200 driverState (`rejected`) | 400, 409 wrong_status |
| `POST /v1/admin/payments/{id}/confirm` | — | 200 `{payment, driver}` | 404, 409 wrong_status (state = payment) |
| `POST /v1/admin/payments/{id}/reject` | reason | 200 `{payment, driver}` | 400, 404, 409 wrong_status |

Rules:
* `register`: name trimmed ≥ 2 chars; city in the configured cities; `termsAccepted` must be true. A rider with the
  same phone → `phone_in_use_as_rider`. A session that already has a driver, or a driver row with that phone → `already_registered`.
* `upload-url` path is always `driver-docs/{driverId}/{kind}`; uploading again overwrites it. `uploaded` accepts only
  a path starting with `driver-docs/{driverId}/` (else 403, nothing saved) and sets the item to `uploaded`, clearing any reason.
* UPI pattern: `^[A-Za-z0-9._-]{2,256}@[A-Za-z][A-Za-z0-9]{1,63}$`.
* Vehicle: `type` bike|auto|cab; `cabClass` hatchback|sedan|suv required iff cab, else null; `ac` forced false unless cab;
  `year` 1990…(this year + 1); plate normalised to upper case with single spaces; unique by `plate_key` across
  `vehicles` and other drivers' `drivers.plate`. Message for `plate_in_use`: "This number plate is registered to another
  driver. If it's your vehicle, contact support." Saving sets `review_state = uploaded`, and the driver's `ride_type`
  (`bike`, `auto`, `cab_<class>`), `vehicle` ("{colour} {makeModel}[ · AC]"), `plate`, `has_ac`.
* Review: when no reviewable item of a `submitted` driver is left `uploaded` and at least one is `rejected`, the driver
  moves to `changes_needed` in the same transaction and one `items_rejected` push lists every rejected item's label.
* `approve`: driver `submitted` and every reviewable item `verified`.
* Amount due now (Asia/Kolkata local date): `ceil(monthly_price × days_left_including_today ÷ days_in_month)`;
  period = today … last day of this month. `payments` accepts only that period start.
* Confirm (one transaction, `update … where status = 'claimed'`): payment `confirmed`; driver `subscription_until =
  greatest(existing, period_end)`; status `approved|paused → live`; one `driver_events` row; one `driver_live` push.
  A second confirm finds nothing to update → 409, no push, nothing written.
* Every status change and every payment change writes one `driver_events` row
  (`kind`, `from_status`, `to_status`, `actor_type` driver|admin|system, `actor_id`, `at`, `data`).

## Eligibility (used by matching)

A driver is a candidate only when `status = 'live'` and `subscription_until >= today` (local date), and a weekly day
off on any of the commute's weekdays is a clash (not a candidate).

## Notifications (outbox kinds → text)

| kind | to | title / body |
|---|---|---|
| `items_rejected` | driver | "A few things need fixing" / "A few things need fixing: {items}. Open the app to see why." |
| `driver_approved` | driver | "You're approved" / "You're approved. Choose your plan to go live." |
| `driver_rejected` | driver | "Application update" / "We can't approve your application. Open the app for details." |
| `driver_live` | driver | "You're live" / "You're live. New requests for {vehicle type} drivers will show up in Requests." |
| `payment_rejected` | driver | "Payment not found" / "We couldn't find your payment. Open the app to try again." |
| `admin_driver_submitted` | each admin phone | "Driver waiting" / "{name} is waiting for review" |
| `admin_payment_claimed` | each admin phone | "Payment claimed" / "{name} says they've paid ₹{amount}" |

Admins register devices with `PUT /v1/devices` like anyone else (owner = their phone).

## Config

| Env | Meaning |
|---|---|
| `ADMIN_PHONES` | Comma-separated phones allowed to sign in as admin |
| `PLATFORM_UPI_ID` | Where drivers pay the subscription (default `commute.plans@okaxis`) |
| `SUPABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY` | Driver documents in the private `driver-docs` bucket (else an in-memory store served by the API; refused in production) |

## Go identifiers shared by code and tests

* `domain.AmountDueNow(monthlyPrice int, today string) (amount int, periodStart, periodEnd string)` — dates `YYYY-MM-DD`.
* `domain.NewReference() string` — `DRV-` + 5 digits.
* `domain.ValidUPI(s string) bool`, `domain.NormalizePlate(s string) string`, `domain.PlateKey(s string) string`.
* `domain.MissingForSubmit(items map[string]string, vehicleSet, dayOffSet bool) []string` — `items` maps kind → state
  for the ten item kinds that have a row; returns missing kinds (then `vehicle`, `day_off`) in the order above.
* `domain.DriverItemKinds` (the ten, in order), `domain.FileItemKinds` (the nine file kinds).
* `app.Storage` interface; `app.NewMemStorage(baseURL, secret string) *app.MemStorage`;
  `(*MemStorage).Put(path string, data []byte)`; `App.Storage` field (nil ⇒ an in-memory store).
* `Config.AdminPhones []string`, `Config.PlatformUPIID string`.
* `App.SendDueNotifications(ctx, sender)` sends D1 kinds like the others.
