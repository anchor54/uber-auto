/// JSON shapes of the backend API (backend/API.md, "Driver (D2)"), mapped to
/// the app's models.
library;

import 'models.dart';
import 'schedule.dart';

double _num(Object? v) => v == null ? 0 : (v as num).toDouble();

int _int(Object? v) => v == null ? 0 : (v as num).toInt();

DateTime? _time(Object? s) => s == null ? null : DateTime.parse(s as String).toLocal();

/// "2026-11-23" as a local date.
DateTime dateFrom(String s) {
  final p = s.split('-').map(int.parse).toList();
  return DateTime(p[0], p[1], p[2]);
}

String dateKey(DateTime d) =>
    '${d.year.toString().padLeft(4, '0')}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

int? _clockOrNull(Object? s) => s is String && s.isNotEmpty ? parseClock(s) : null;

RideDirection directionFrom(Object? s) => s == 'return' ? RideDirection.returnLeg : RideDirection.outbound;

OfferKind offerKindFrom(Object? s) => switch (s) {
      'slot' => OfferKind.slot,
      'ride' => OfferKind.ride,
      _ => OfferKind.series,
    };

OfferStatus offerStatusFrom(Object? s) => switch (s) {
      'open' => OfferStatus.open,
      'closing' => OfferStatus.closing,
      _ => OfferStatus.closed,
    };

PaymentMethod paymentFrom(Object? s) => s == 'cash' ? PaymentMethod.cash : PaymentMethod.upi;

DriverProfile driverFromJson(Map<String, Object?> j) {
  final name = j['name'] as String?;
  return DriverProfile(
    phone: (j['phone'] as String?) ?? '',
    name: name == null || name.isEmpty ? null : name,
    vehicleType: j['vehicle_type'] as String?,
  );
}

/// auth.verify: the user plus whether a driver profile exists.
DriverProfile signInFromJson(Map<String, Object?> j) {
  final user = driverFromJson(j['user'] as Map<String, Object?>);
  return DriverProfile(
    phone: user.phone,
    name: user.name,
    vehicleType: user.vehicleType,
    driverExists: (j['driver_exists'] as bool?) ?? true,
  );
}

OfferPlace placeFromJson(Map<String, Object?> j) =>
    OfferPlace(label: (j['label'] as String?) ?? (j['title'] as String?) ?? '', lat: _num(j['lat']), lng: _num(j['lng']));

OfferSlot slotFromJson(Map<String, Object?> j) => OfferSlot(
      weekday: _int(j['weekday']),
      direction: directionFrom(j['direction']),
      pickupTime: parseClock(j['local_pickup_time'] as String),
      estimatedDropTime: _clockOrNull(j['estimated_drop_time']),
      arriveByTime: _clockOrNull(j['arrive_by_time']),
    );

/// [fetchedAt] is the app's clock when the response arrived.
Offer offerFromJson(Map<String, Object?> j, {required DateTime fetchedAt}) {
  final rider = (j['rider'] as Map<String, Object?>?) ?? const {};
  final name = (rider['name'] as String?) ?? '';
  return Offer(
    id: j['id'] as String,
    kind: offerKindFrom(j['kind']),
    seriesId: j['series_id'] as String?,
    round: _int(j['round']),
    status: offerStatusFrom(j['status']),
    outcome: j['outcome'] as String?,
    openedAt: _time(j['opened_at'])!,
    expiresAt: _time(j['expires_at'])!,
    secondsLeft: _int(j['seconds_left']),
    fetchedAt: fetchedAt,
    canRespond: (j['can_respond'] as bool?) ?? false,
    interested: (j['interested'] as bool?) ?? false,
    interestedAt: _time(j['interested_at']),
    dismissed: (j['dismissed'] as bool?) ?? false,
    result: j['result'] as String?,
    rider: OfferRider(
      firstName: (rider['first_name'] as String?) ?? name.split(' ').first,
      name: name,
      rating: _num(rider['rating']),
    ),
    vehicleType: (j['vehicle_type'] as String?) ?? '',
    preferences: [for (final p in (j['preferences'] as List? ?? const [])) p as String],
    pickup: placeFromJson(j['pickup'] as Map<String, Object?>),
    drop: placeFromJson(j['drop'] as Map<String, Object?>),
    startDate: dateFrom(j['start_date'] as String),
    distanceKm: _num(j['distance_km']),
    travelMinutes: _int(j['travel_minutes']),
    farePerRide: _int(j['fare_per_ride']),
    ridesPerMonth: _int(j['rides_per_month']),
    farePerMonth: _int(j['fare_per_month']),
    usualPayment: paymentFrom(j['usual_payment']),
    slots: [for (final s in (j['slots'] as List? ?? const [])) slotFromJson(s as Map<String, Object?>)],
  );
}

OfferResult offerResultFromJson(Map<String, Object?> j) => OfferResult(
      offerId: j['offer_id'] as String,
      seriesId: j['series_id'] as String?,
      result: (j['result'] as String?) ?? '',
      message: (j['message'] as String?) ?? '',
      closedAt: _time(j['closed_at']),
    );

OfferList offerListFromJson(Map<String, Object?> j, {required DateTime fetchedAt}) => OfferList(
      offers: [for (final o in (j['offers'] as List? ?? const [])) offerFromJson(o as Map<String, Object?>, fetchedAt: fetchedAt)],
      results: [for (final r in (j['results'] as List? ?? const [])) offerResultFromJson(r as Map<String, Object?>)],
    );

WeekRide weekRideFromJson(Map<String, Object?> j) {
  final drop = _time(j['drop_at']);
  final pickup = j['pickup'] as Map<String, Object?>?;
  final dropPlace = j['drop'] as Map<String, Object?>?;
  return WeekRide(
    id: j['id'] as String,
    seriesId: j['series_id'] as String?,
    date: dateFrom(j['local_date'] as String),
    weekday: _int(j['weekday']),
    direction: directionFrom(j['direction']),
    pickupTime: parseClock(j['local_pickup_time'] as String),
    dropTime: drop == null ? null : drop.hour * 60 + drop.minute,
    riderName: (j['rider_name'] as String?) ?? '',
    fare: _int(j['fare']),
    isNew: (j['new'] as bool?) ?? false,
    status: (j['status'] as String?) ?? 'assigned',
    pickupLabel: pickup == null ? '' : placeFromJson(pickup).label,
    dropLabel: dropPlace == null ? '' : placeFromJson(dropPlace).label,
  );
}

DriverWeek weekFromJson(Map<String, Object?> j) => DriverWeek(
      weekStart: dateFrom(j['week_start'] as String),
      weekEnd: dateFrom(j['week_end'] as String),
      rides: [for (final r in (j['rides'] as List? ?? const [])) weekRideFromJson(r as Map<String, Object?>)],
    );

// D1 shapes (backend/API.md, "Driver sign-up and verification (D1)").

DateTime? _date(Object? s) => s is String && s.isNotEmpty ? dateFrom(s.substring(0, 10)) : null;

String? _str(Object? s) => s is String && s.isNotEmpty ? s : null;

DriverItem driverItemFromJson(Map<String, Object?> j) => DriverItem(
      kind: j['kind'] as String,
      label: (j['label'] as String?) ?? ItemKind.label(j['kind'] as String),
      state: itemStateFrom(j['state']),
      rejectReason: _str(j['reject_reason']),
      value: _str(j['value']),
      storagePath: _str(j['storage_path']),
      uploadedAt: _time(j['uploaded_at']),
    );

VehicleDetails vehicleFromJson(Map<String, Object?> j) {
  var type = (j['type'] as String?) ?? (j['vehicle_type'] as String?) ?? '';
  var cabClass = _str(j['cab_class']);
  if (type.startsWith('cab_')) {
    cabClass ??= type.substring(4);
    type = 'cab';
  }
  return VehicleDetails(
    type: type,
    cabClass: cabClass,
    makeModel: (j['make_model'] as String?) ?? '',
    year: j['year'] == null ? null : _int(j['year']),
    plate: (j['plate'] as String?) ?? '',
    colour: (j['colour'] as String?) ?? '',
    ac: (j['ac'] as bool?) ?? false,
    cngOrElectric: (j['cng_or_electric'] as bool?) ?? false,
    reviewState: itemStateFrom(j['review_state'] ?? j['state']),
    rejectReason: _str(j['reject_reason']),
  );
}

DriverPlan driverPlanFromJson(Map<String, Object?> j) => DriverPlan(
      vehicleType: (j['vehicle_type'] as String?) ?? '',
      cabClass: _str(j['cab_class']),
      label: (j['label'] as String?) ?? '',
      monthlyPrice: _int(j['monthly_price']),
    );

SubscriptionPayment paymentFromJson(Map<String, Object?> j) => SubscriptionPayment(
      id: j['id'] as String,
      periodStart: _date(j['period_start'])!,
      periodEnd: _date(j['period_end'])!,
      amount: _int(j['amount']),
      reference: (j['reference'] as String?) ?? '',
      status: paymentStatusFrom(j['status']),
      claimedAt: _time(j['claimed_at']),
      reviewedAt: _time(j['reviewed_at']),
      rejectReason: _str(j['reject_reason']),
    );

/// driver.get (also the `state` of most D1 errors).
Driver driverDetailFromJson(Map<String, Object?> j) {
  final vehicle = j['vehicle'];
  final plan = j['plan'];
  final payment = j['latest_payment'];
  final dayOff = j['day_off'];
  return Driver(
    id: (j['id'] as String?) ?? '',
    name: (j['name'] as String?) ?? '',
    phone: (j['phone'] as String?) ?? '',
    city: (j['city'] as String?) ?? 'blr',
    status: driverStatusFrom(j['status']),
    rejectReason: _str(j['reject_reason']),
    reference: (j['reference'] as String?) ?? '',
    upiId: _str(j['upi_id']),
    dayOff: dayOff is num ? dayOff.toInt() : null,
    dayOffSet: (j['day_off_set'] as bool?) ?? dayOff != null,
    subscriptionUntil: _date(j['subscription_until']),
    eligible: (j['eligible'] as bool?) ?? false,
    items: [for (final i in (j['items'] as List? ?? const [])) driverItemFromJson(i as Map<String, Object?>)],
    missing: [for (final m in (j['missing'] as List? ?? const [])) m as String],
    vehicle: vehicle is Map<String, Object?> ? vehicleFromJson(vehicle) : null,
    plan: plan is Map<String, Object?> ? driverPlanFromJson(plan) : null,
    latestPayment: payment is Map<String, Object?> ? paymentFromJson(payment) : null,
  );
}

/// Whether an error's `state` is a driver (driver.get shape).
bool looksLikeDriver(Map<String, Object?> j) => j.containsKey('items') && j.containsKey('status');

/// Whether an error's `state` is a subscription payment.
bool looksLikePayment(Map<String, Object?> j) => j.containsKey('period_start') && j.containsKey('amount');

PlanQuote planQuoteFromJson(Map<String, Object?> j) {
  final payment = j['latest_payment'];
  final plan = driverPlanFromJson((j['plan'] as Map<String, Object?>?) ?? const {});
  return PlanQuote(
    plan: plan.monthlyPrice == 0 && j['monthly_price'] != null
        ? DriverPlan(vehicleType: plan.vehicleType, cabClass: plan.cabClass, label: plan.label, monthlyPrice: _int(j['monthly_price']))
        : plan,
    amountDue: _int(j['amount_due']),
    periodStart: _date(j['period_start'])!,
    periodEnd: _date(j['period_end'])!,
    daysLeft: _int(j['days_left']),
    daysInMonth: _int(j['days_in_month']),
    reference: (j['reference'] as String?) ?? '',
    payeeUpi: (j['payee_upi'] as String?) ?? '',
    status: driverStatusFrom(j['status']),
    latestPayment: payment is Map<String, Object?> ? paymentFromJson(payment) : null,
  );
}

UploadTarget uploadTargetFromJson(Map<String, Object?> j) => UploadTarget(
      kind: (j['kind'] as String?) ?? '',
      uploadUrl: (j['upload_url'] as String?) ?? (j['url'] as String),
      method: (j['method'] as String?) ?? 'PUT',
      storagePath: (j['storage_path'] as String?) ?? (j['path'] as String),
      contentType: (j['content_type'] as String?) ?? 'image/jpeg',
      maxBytes: j['max_bytes'] == null ? maxUploadBytes : _int(j['max_bytes']),
      expiresAt: _time(j['expires_at']) ?? DateTime.now().add(const Duration(minutes: 10)),
    );
