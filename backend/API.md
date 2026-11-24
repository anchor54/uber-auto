# ride-api endpoints (R1 + D2 + D1)

JSON in and out, `snake_case`. Times are UTC RFC 3339 (`2026-11-19T04:45:00Z`); dates are
`YYYY-MM-DD` in the series' zone (Asia/Kolkata); local clock times are `HH:MM`. Weekdays are ISO:
1 = Monday … 7 = Sunday. Ids are UUIDs.

Signed-in calls send `Authorization: Bearer <token>`.

**Errors** are always

```json
{ "code": "offer_closed", "message": "This request has closed.", "state": { … } }
```

where `state` is the object as it is now (the offer, the series), or `null`, so the app can redraw
without another call.

| Status | Codes |
|---|---|
| 400 | `invalid_request`, `bad_request`, `invalid_phone`, `invalid_code` |
| 401 | `unauthorized`, `code_expired`, `invalid_code` |
| 403 | `riders_only`, `drivers_only`, `admins_only`, `forbidden` (not your series), `not_eligible`, `not_a_driver`, `not_your_path` |
| 404 | `not_found`, `not_registered` |
| 409 | `offer_closed`, `already_assigned`, `not_retryable`, `phone_in_use_as_rider`, `already_registered`, `locked`, `not_uploaded`, `plate_in_use`, `incomplete`, `wrong_status`, `not_approved`, `already_claimed`, `items_not_verified` |
| 422 | `ride_type_not_allowed`, `start_too_soon`, `invalid_time`, `invalid_name`, `city_not_served`, `terms_not_accepted`, `bad_kind`, `bad_content_type`, `bad_upi`, `bad_vehicle_type`, `bad_cab_class`, `bad_plate`, `bad_year`, `bad_weekday`, `bad_period`, `bad_decision`, `reason_required` |
| 429 | `resend_too_soon`, `too_many_codes`, `too_many_attempts` |

## Sign-in (unchanged flow: phone + code; dev builds use `123456`)

### `POST /v1/auth/otp` → 204
`{ "phone": "9000000101" }`. Sends a code. Riders and drivers both use it.

### `POST /v1/auth/verify` → 200
`{ "phone": "9000000101", "code": "123456" }` →
```json
{ "token": "…", "user": { "id": "…", "phone": "9000000101", "role": "rider", "name": "Priya Sharma" } }
```
A known number signs in with its role (rider, driver or admin); a new number becomes a rider.
The admin tool signs in here (users.role = `admin`).

### `POST /v1/driver/auth/otp` → 204, `POST /v1/driver/auth/verify` → 200 (D1 auth.start / auth.verify)
The driver app's sign-in. Any number gets a code. Verify returns
`{ "token", "user": { … }, "driver_exists": false }` and always a **driver** session: a new number
gets a `users` row (role driver) and no driver profile until `POST /v1/driver/signup`. A rider's
number also gets a driver session; signup then answers 409 `phone_in_use_as_rider`. A wrong code is
401 `wrong_code`. An admin number: 403 `not_a_driver`.

### `GET /v1/me` → 200
`{ "id", "phone", "role", "name" }`

### `PUT /v1/devices` → 204
`{ "fcm_token": "…", "platform": "android" }`. Registers the app for pushes.

## Rider (R1)

### Series request body

Used by `series.preview`, `series.create` and (every field optional) `series.retry`:

```json
{
  "pickup": { "lat": 12.9352, "lng": 77.6245, "label": "Koramangala" },
  "drop":   { "lat": 12.9698, "lng": 77.7178, "label": "Whitefield" },
  "vehicle_type": "auto",
  "preferences": ["ac", "quiet", "woman_driver"],
  "slots": [
    { "weekday": 1, "direction": "outbound", "timing": "pickup_at", "time": "08:30" },
    { "weekday": 5, "direction": "outbound", "timing": "arrive_by", "time": "09:30" },
    { "weekday": 1, "direction": "return",   "timing": "pickup_at", "time": "18:30" }
  ],
  "usual_payment": "upi",
  "start_date": "2026-11-23"
}
```

* One slot per weekday and direction ("Same time every day" is the app sending the same time for
  every day). `direction` defaults to `outbound`, `timing` to `pickup_at`. A return slot's pickup is
  the drop-off and vice versa.
* `arrive_by`: pickup = time − travel − 10 min, rounded down to 5 min.
* `ride_type` is accepted as an alias of `vehicle_type`.
* `usual_payment`: `upi` (default) or `cash`.
* `start_date` is optional: the earliest start wanted. The start date is the first chosen weekday on or
  after it, and never before tomorrow; without it, the first chosen weekday from tomorrow on.
  A `start_date` of today or earlier is 422 `start_too_soon`.
* 422 `ride_type_not_allowed` when `ride_type_config` doesn't allow the type for regular rides
  (flow `recurring`) in the city, or there is no fare rule.

### `POST /v1/series/preview` → 200 (series.preview)
Nothing is saved.
```json
{
  "vehicle_type": "auto",
  "distance_km": 15.1,
  "travel_minutes": 51,
  "start_date": "2026-11-20",
  "suggested_fare": 197,
  "rides_per_month": 13,
  "time_zone": "Asia/Kolkata",
  "slots": [
    { "weekday": 1, "day": "MON", "direction": "outbound", "timing": "pickup_at",
      "arrive_by_time": null, "local_pickup_time": "08:30", "estimated_drop_time": "09:21" }
  ]
}
```
Road distance = straight line × 1.4; `travel_minutes` = that at the type's speed (auto 18 km/h),
rounded up; `suggested_fare` = fare rule base + per-km × road km (at least the minimum), to the
nearest rupee.

### `POST /v1/series` → 201 (series.create)
Same body. Returns the **series** (below), `finding_driver` with round 1 open for 15 minutes, or
`no_driver_found` straight away when no driver is eligible (no offer is opened).

### Series object
```json
{
  "id": "…", "rider_id": "…",
  "status": "finding_driver",
  "city": "blr", "vehicle_type": "auto", "ride_type": "auto", "preferences": [],
  "pickup": { "lat": 12.9352, "lng": 77.6245, "label": "Koramangala" },
  "drop":   { "lat": 12.9698, "lng": 77.7178, "label": "Whitefield" },
  "time_zone": "Asia/Kolkata", "start_date": "2026-11-20",
  "distance_km": 15.1, "travel_minutes": 51, "suggested_fare": 197, "usual_payment": "upi",
  "requested_at": "2026-11-19T04:30:00Z", "version": 1,
  "slots": [
    { "id": "…", "weekday": 1, "day": "MON", "direction": "outbound", "timing": "pickup_at",
      "arrive_by_time": null, "local_pickup_time": "08:30", "estimated_drop_time": "09:21",
      "driver_id": null, "driver": null, "active": true }
  ],
  "offer": { "id": "…", "kind": "series", "round": 1, "status": "open",
             "opened_at": "2026-11-19T04:30:00Z", "expires_at": "2026-11-19T04:45:00Z", "outcome": null },
  "round": 1,
  "expires_at": "2026-11-19T04:45:00Z"
}
```
* `status`: `finding_driver` | `active` | `paused` | `no_driver_found` | `ended`.
* `slots[].driver` once picked: `{ "id", "name", "vehicle_type", "vehicle_number", "vehicle_desc", "rating" }`.
* `offer` is the latest round (any status; `outcome` `assigned` | `no_fit` | `cancelled` once closed).
  `round` and `expires_at` repeat the open round's, and are `null` when no round is open. (The rider
  app shows no rounds; they are there for tests and support.)

### `GET /v1/series` → 200
The rider's series, newest first: `{ "series": [ <series>, … ] }`.

### `GET /v1/series/{id}` → 200 (series.get)
The series. 403 `forbidden` for another rider's series, 404 `not_found`.

### `POST /v1/series/{id}/cancel-request` → 204 (series.cancel_request)
Ends a `finding_driver` or `no_driver_found` series and closes its open round (`outcome: cancelled`);
interested drivers are told "The rider withdrew this request." Already `ended`: 204.
Once a driver is assigned: 409 `already_assigned` with the series (and its driver) as `state`.

### `POST /v1/series/{id}/retry` → 200 (series.retry)
Only from `no_driver_found` (else 409 `not_retryable`, state = series). Optional body: any fields of
the series request (`slots` rewrites every slot; `vehicle_type` changes the type). The start date is
worked out again, the series goes back to `finding_driver`, and a fresh set of 3 rounds starts;
drivers who clashed or withdrew before are offered it again ("Not for me" stays). Returns the series.

### `GET /v1/ride-types?flow=recurring` → 200
R1.3's list: `{ "city": "blr", "flow": "recurring", "ride_types": [ { "ride_type": "auto", "label": "Auto", "description": "Up to 3 riders · open sides", "seats": 3 } ] }`.

### `GET /v1/places?q=kora` → 200
Place search (Ola Maps when configured, else the demo places): `{ "places": [ { "id", "title", "detail", "lat", "lng" } ] }`.

## Driver (D2)

### `GET /v1/offers` → 200 (offers.list)
Open offers this driver received (and hasn't dismissed), newest first, plus result cards for requests
they were interested in, kept 24 hours. 403 `not_eligible` unless the driver is live with an active
subscription.
```json
{
  "offers": [ <offer> ],
  "results": [ { "offer_id": "…", "series_id": "…", "result": "went_to_another_driver",
                 "message": "Went to another driver", "closed_at": "…" } ]
}
```
`results[].result`: `picked` | `went_to_another_driver` | `rider_withdrew`.

### Offer object (also `GET /v1/offers/{id}`, offer.get: 404 unless the driver received it)
```json
{
  "id": "…", "kind": "series", "series_id": "…", "round": 1,
  "status": "open", "outcome": null,
  "opened_at": "…", "expires_at": "…", "seconds_left": 840, "can_respond": true,
  "interested": false, "interested_at": null, "dismissed": false, "result": null,
  "rider": { "first_name": "Priya", "name": "Priya S.", "rating": 4.8 },
  "vehicle_type": "auto", "preferences": [],
  "pickup": { "lat": …, "lng": …, "label": "Koramangala" }, "drop": { … },
  "start_date": "2026-11-20", "distance_km": 15.1, "travel_minutes": 51,
  "fare_per_ride": 197, "rides_per_month": 13, "fare_per_month": 2561, "usual_payment": "upi",
  "slots": [ { "weekday": 1, "day": "MON", "direction": "outbound", "timing": "pickup_at",
               "arrive_by_time": null, "local_pickup_time": "08:30", "estimated_drop_time": "09:21" } ]
}
```
`status` is `closing` once `expires_at` has passed but the sweep hasn't closed it yet (the button reads
"Closed"); `can_respond` is false then. `result`: `picked` | `not_picked` | `clashed` | `withdrew` |
`ineligible` once decided.

### `POST /v1/offers/{id}/mark-interested` → 204 (offer.mark_interested)
### `POST /v1/offers/{id}/withdraw-interest` → 204 (offer.withdraw_interest)
Allowed until `expires_at`; after that 409 `offer_closed` with the offer as `state`. 404 if the driver
didn't receive the offer; 403 `not_eligible` (mark) if their plan has lapsed. Marking twice, or
withdrawing without interest, is a no-op.

### `POST /v1/offers/{id}/dismiss` → 204 (offer.dismiss)
"Not for me": the request stops showing for this driver, in this and later rounds (and withdraws their
interest while the round is open). 404 if they didn't receive it.

### `GET /v1/rides/week?start=2026-11-23` → 200 (rides.week)
The driver's rides for 7 days from `start` (default: this week's Monday), except cancelled ones.
```json
{
  "week_start": "2026-11-23", "week_end": "2026-11-29",
  "rides": [
    { "id": "…", "series_id": "…", "slot_id": "…", "local_date": "2026-11-23", "weekday": 1,
      "direction": "outbound", "pickup_at": "…", "drop_at": "…", "busy_until": "…",
      "local_pickup_time": "08:30", "status": "assigned", "fare": 197,
      "rider_id": "…", "rider_name": "Priya Sharma",
      "pickup": { … }, "drop": { … }, "new": true }
  ]
}
```
`new`: created in the last 24 hours (a new rider's rides after a win).

## Driver sign-up and verification (D1)

All with a driver session (403 `drivers_only` otherwise). Every error carries the driver as it is
now in `state` where there is one. Item kinds: the seven documents `profile_photo`, `id_proof`,
`licence`, `rc`, `insurance`, `permit`, `upi`; the vehicle `vehicle` and its photos
`vehicle_front`, `vehicle_back`, `vehicle_interior`.

**Locked** (409 `locked`): the status isn't `signing_up`, and, in `changes_needed`, the item isn't one
of the rejected ones (or one replaced since). The day off is editable only in `signing_up` (later
changes are D4).

### `POST /v1/driver/signup` → 201 (driver.register)
`{ "name": "Ravi Kumar", "city": "blr", "terms_accepted": true }` → the driver (below), `signing_up`,
with a fresh `reference` `DRV-nnnnn`. City: `blr` (or "Bengaluru"). 409 `phone_in_use_as_rider`,
409 `already_registered`; 422 `invalid_name`, `city_not_served`, `terms_not_accepted`.

### `GET /v1/driver` → 200 (driver.get)
```json
{
  "id": "…", "user_id": "…", "name": "Ravi Kumar", "phone": "9800000201", "city": "blr",
  "status": "changes_needed", "reject_reason": null, "reference": "DRV-20417",
  "upi_id": "ravi@okaxis", "day_off": 7, "day_off_name": "SUN", "day_off_set": true,
  "subscription_until": null, "eligible": false, "rating": 5, "version": 3,
  "submitted_at": "…", "approved_at": null, "created_at": "…",
  "items": [ { "kind": "licence", "label": "Driving licence", "state": "rejected",
               "reject_reason": "photo blurred", "storage_path": "driver-docs/<id>/licence",
               "uploaded_at": "…", "reviewed_at": "…" }, … ],
  "documents_added": 7, "documents_verified": 6, "missing": ["licence"],
  "vehicle": { "type": "auto", "cab_class": null, "ride_type": "auto", "make_model": "Bajaj RE",
               "year": 2021, "plate": "KA 05 AB 1234", "colour": "Green", "ac": false,
               "cng_or_electric": true, "review_state": "verified", "reject_reason": null },
  "plan": { "vehicle_type": "auto", "cab_class": "", "ride_type": "auto", "label": "Auto plan", "monthly_price": 1199 },
  "latest_payment": <payment> | null
}
```
`status`: `signing_up` | `submitted` | `changes_needed` | `approved` | `live` | `paused` (D5) | `rejected`.
Item `state`: `missing` | `uploaded` | `verified` | `rejected`. `items` lists all eleven kinds (the
UPI item has `value`). `missing` is what submit would refuse on (absent or rejected). 404
`not_registered` before signup.

### `POST /v1/driver/upload-url` → 200 (driver.upload_url)
`{ "kind": "licence", "content_type": "image/jpeg" }` →
`{ "kind", "upload_url", "method": "PUT", "storage_path": "driver-docs/<driver>/licence", "content_type", "max_bytes": 5242880, "expires_at" }`.
The app PUTs the file to `upload_url` (10 minutes). Only JPEG or PNG (422 `bad_content_type`); the
store refuses files over 5 MB (413 `file_too_large`) or whose bytes aren't the declared image type
(415 `bad_file_type`). Uploading again overwrites the same path. 409 `locked`.

### `PUT /v1/driver/documents/{kind}` → 200 (driver.item_uploaded)
`{ "storage_path": "driver-docs/<driver>/licence" }` → the item (`state: uploaded`). Paths outside
`driver-docs/<your id>/` are 403 `not_your_path` (nothing saved); a file that never arrived is 409
`not_uploaded`. Also for the three vehicle photos. 409 `locked`.

### `PUT /v1/driver/upi` → 200 (driver.set_upi)
`{ "upi_id": "ravi@okaxis" }` → the `upi` item. 422 `bad_upi` (pattern name@handle), 409 `locked`.

### `PUT /v1/driver/vehicle` → 200 (driver.set_vehicle)
`{ "vehicle_type": "auto" (or "type"; "cab_sedan" accepted), "cab_class": null | "hatchback" | "sedan" | "suv", "make_model", "year", "plate", "colour", "ac", "cng_or_electric" }`
→ the vehicle. The plate is upper-cased (spaces collapsed) and unique across drivers ignoring
spaces and dashes: 409 `plate_in_use`. The type (`cab_<class>` for cabs) decides which offers the
driver receives. 409 `locked`; 422 `bad_vehicle_type`, `bad_cab_class`, `bad_plate`, `bad_year`.

### `PUT /v1/driver/day-off` → 200 (driver.set_day_off)
`{ "day_off": 1..7 }` (ISO; "WED" accepted) or `{ "day_off": null }` for "No fixed day off" → the
driver. A regular slot on the day off is a clash in every pick. 409 `locked`.

### `POST /v1/driver/submit` → 200 (driver.submit)
→ the driver, `submitted`; everything is locked until the review ends. 409 `incomplete` with
`"missing": ["insurance", "permit"]` (also in `state.missing`); 409 `wrong_status`.
The admin gets "{name} is waiting for review".

### `GET /v1/driver/plan` → 200 (plans.get)
```json
{ "plan": { "vehicle_type": "auto", "cab_class": "", "label": "Auto plan", "monthly_price": 1199 },
  "monthly_price": 1199, "amount_due": 400, "period_start": "2026-11-21", "period_end": "2026-11-30",
  "days_left": 10, "days_in_month": 30, "reference": "DRV-20417", "payee_upi": "commuteride@icici",
  "status": "approved", "latest_payment": null }
```
The first period runs from today to the month's last day; `amount_due` = monthly price × days left
(today included) ÷ days in the month, rounded up to the rupee. (For a live driver it shows the next
period.) 409 `not_approved` before approval or after rejection.

### `POST /v1/driver/payments` → 201 (driver.claim_payment, "I've paid")
`{ "period_start": "2026-11-21" }` (optional) → the payment, `claimed`:
```json
{ "id": "…", "driver_id": "…", "driver_name": "Ravi Kumar", "period_start": "2026-11-21",
  "period_end": "2026-11-30", "amount": 400, "reference": "DRV-20417", "status": "claimed",
  "claimed_at": "…", "reviewed_at": null, "reject_reason": null }
```
One open claim at a time and one claimed-or-confirmed payment per period: 409 `already_claimed`
(state = that payment). 409 `not_approved` unless `approved` (or `paused`). The admin gets
"{name} says they've paid ₹{amount}".

## Admin (D1)

Admin session only (403 `admins_only`). `cmd/admin` wraps these (`pending`, `show`, `verify`,
`reject-item`, `approve`, `reject`, `confirm-payment`, `reject-payment`).

### `GET /v1/admin/pending` → 200
`{ "drivers": [ <driver> … ], "payments": [ <payment> … ] }`: drivers in `submitted` and payments in
`claimed`, oldest first.

### `GET /v1/admin/drivers/{id}` → 200 (admin.show_driver)
The driver, each file item with `url` (= `read_url`), a 10-minute signed link. 404.

### `POST /v1/admin/drivers/{id}/review-item` → 200 (admin.review_item)
`{ "item": "licence", "state": "verified" | "rejected", "reason": "photo blurred" }` → the item (with
`driver_status`). Only while `submitted` (409 `wrong_status`); a reason is required to reject. When
the last pending item is reviewed and any is rejected, the driver moves to `changes_needed` and gets
one push listing every rejected item: "A few things need fixing: {items}. Open the app to see why."

### `POST /v1/admin/drivers/{id}/approve` → 200 / `POST /v1/admin/drivers/{id}/reject` → 200
Approve: only when all eleven items are verified (409 `items_not_verified` with `unverified`), from
`submitted` (409 `wrong_status`); push "You're approved. Choose your plan to go live."
Reject `{ "reason" }`: from `submitted`, `changes_needed` or `approved`; status `rejected` for good;
push "We can't approve your application. Open the app for details."

### `POST /v1/admin/payments/{id}/confirm` → 200 / `POST /v1/admin/payments/{id}/reject` → 200
`{ "payment": <payment>, "driver": <driver> }`. Confirm, in one transaction: the payment becomes
`confirmed` only if still `claimed` (else 409 `wrong_status`, no second push), `subscription_until`
becomes the period's end, an `approved` (or `paused`) driver becomes `live`; push "You're live. New
requests for {vehicle type} drivers will show up in Requests." Reject `{ "reason" }`: `rejected`;
push "We couldn't find your payment. Open the app to try again."; the driver can claim again.

### Files: `PUT /storage/driver-docs/…?op=put&exp=&ct=&sig=`, `GET /storage/driver-docs/…?op=get&…`
The signed upload and read links (dev and tests: files kept by ride-api in memory or under
`STORAGE_DIR`; links on `PUBLIC_URL`, signed with `STORAGE_SECRET`). 403 `bad_signature` /
`link_expired`.

### Eligibility
A driver receives offers only when `status = 'live'` and `subscription_until` ≥ today (local date),
for their vehicle type and city. Every events row for a status change has
`data.old_status` / `data.new_status` (driver ids for driver changes, payment ids for payments).

## Internal and dev

### `POST /internal/sweep` → 200
Runs the sweeper once: `close_offers`, `generate_rides`, `send_outbox`; records a `sweeper_runs` row.
Needs `Authorization: Bearer $SWEEP_TOKEN` when `SWEEP_TOKEN` is set.
`{ "close_offers": { "closed": 1, "assigned": 1, "errors": 0 }, "generate_rides": 0, "send_outbox": 7 }`

### `POST /dev/clock/advance` → 200 (only with `ENABLE_DEV_ROUTES=true`; refused in production)
`{ "minutes": 15 }` moves the server clock forward, so `sim close-offers` can close rounds at once.

### `GET /health` → `{ "ok": true }`

### `GET /v1/map/style.json`, `/v1/map/sprite…`, `/v1/map/tiles/…`, `/v1/map/fonts/…`
The Ola Maps proxy for the app's map views (404 `no_map` without `OLA_MAPS_API_KEY`).

## Pushes

Written to `notification_outbox` in the same transaction as the change; sent by the sweep.
`data` always has `kind`, plus `series_id` / `offer_id`.

| kind | To | Title / body |
|---|---|---|
| `offer_new` | eligible drivers, round 1 | "New regular ride" / "Koramangala → Whitefield, Mon/Wed/Fri 08:30, ₹197 a ride" |
| `offer_still_open` | rounds 2–3, drivers not already interested | "Still open" / same |
| `offer_won` | picked driver | "You got it" / "You're Priya's regular driver: Mon, Wed, Fri at 08:30 from 20 Nov." |
| `offer_lost` | other interested drivers (not picked, clashed, ineligible) | "Request closed" / "This one went to another driver." |
| `offer_withdrawn` | interested drivers when the rider cancels | "Request withdrawn" / "The rider withdrew this request." |
| `series_active` | rider | "Driver confirmed" / "Ravi will drive you Mon, Wed and Fri from Fri 20 Nov" |
| `series_no_driver` | rider | "No driver yet" / "No driver yet. Try again or change the time." |
| `driver_changes_needed` | driver | "A few things need fixing" / "A few things need fixing: Driving licence. Open the app to see why." |
| `driver_approved` | driver | "You're approved" / "You're approved. Choose your plan to go live." |
| `driver_rejected` | driver | "Application closed" / "We can't approve your application. Open the app for details." |
| `driver_live` | driver | "You're live" / "You're live. New requests for auto drivers will show up in Requests." |
| `driver_payment_rejected` | driver | "Payment not found" / "We couldn't find your payment. Open the app to try again." |
| `admin_driver_submitted` | every admin | "Driver to review" / "Ravi Kumar is waiting for review" |
| `admin_payment_claimed` | every admin | "Payment to confirm" / "Ravi Kumar says they've paid ₹400" |
