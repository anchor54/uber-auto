# ride-api endpoints (R1 + D2)

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
| 403 | `riders_only`, `drivers_only`, `forbidden` (not your series), `not_eligible`, `not_a_driver` |
| 404 | `not_found` |
| 409 | `offer_closed`, `already_assigned`, `not_retryable` |
| 422 | `ride_type_not_allowed`, `start_too_soon`, `invalid_time` |
| 429 | `resend_too_soon`, `too_many_codes`, `too_many_attempts` |

## Sign-in (unchanged flow: phone + code; dev builds use `123456`)

### `POST /v1/auth/otp` → 204
`{ "phone": "9000000101" }`. Sends a code. Riders and drivers both use it.

### `POST /v1/auth/verify` → 200
`{ "phone": "9000000101", "code": "123456" }` →
```json
{ "token": "…", "user": { "id": "…", "phone": "9000000101", "role": "rider", "name": "Priya Sharma" } }
```
A known number signs in with its role (rider or driver); a new number becomes a rider.

### `POST /v1/driver/auth/otp`, `POST /v1/driver/auth/verify`
The same, for drivers only (403 `not_a_driver` otherwise). Drivers are onboarded by ops (D1).

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
