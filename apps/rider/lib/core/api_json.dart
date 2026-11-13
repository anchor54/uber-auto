/// JSON shapes of the backend API (backend/src), mapped to the app's models.
library;

import 'models.dart';

String _repeatKey(CommuteRepeat r) => switch (r) {
      CommuteRepeat.everyWeekday => 'every_weekday',
      CommuteRepeat.chosenDays => 'chosen_days',
    };

CommuteRepeat _repeatFrom(String s) => s == 'every_weekday' ? CommuteRepeat.everyWeekday : CommuteRepeat.chosenDays;

String _timingKey(TimingMode t) => switch (t) {
      TimingMode.pickupAt => 'pickup_at',
      TimingMode.arriveBy => 'arrive_by',
    };

TimingMode _timingFrom(String s) => s == 'arrive_by' ? TimingMode.arriveBy : TimingMode.pickupAt;

String preferenceKey(RidePreference p) => switch (p) {
      RidePreference.acOn => 'ac_on',
      RidePreference.quiet => 'quiet',
      RidePreference.womanDriver => 'woman_driver',
    };

RidePreference _preferenceFrom(String s) => RidePreference.values.firstWhere((p) => preferenceKey(p) == s);

PaymentMethod paymentFrom(String s) => s == 'cash' ? PaymentMethod.cash : PaymentMethod.upi;

String paymentKey(PaymentMethod m) => m.name; // 'upi' | 'cash'

CommuteStatus commuteStatusFrom(String s) => switch (s) {
      'awaiting_driver' => CommuteStatus.awaitingDriver,
      'active' => CommuteStatus.active,
      'paused' => CommuteStatus.paused,
      'declined' => CommuteStatus.declined,
      _ => CommuteStatus.cancelled,
    };

/// "2026-10-22" as a local date.
DateTime dateFrom(String s) {
  final p = s.split('-').map(int.parse).toList();
  return DateTime(p[0], p[1], p[2]);
}

String dateKey(DateTime d) =>
    '${d.year.toString().padLeft(4, '0')}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

double _num(Object? v) => (v as num).toDouble();

Map<String, Object?> placeToJson(Place p) => {'id': p.id, 'title': p.title, 'detail': p.detail, 'lat': p.lat, 'lng': p.lng};

Place placeFromJson(Map<String, Object?> j) => Place(
      id: j['id'] as String,
      title: j['title'] as String,
      detail: (j['detail'] as String?) ?? '',
      lat: _num(j['lat']),
      lng: _num(j['lng']),
    );

TripEstimate tripFromJson(Map<String, Object?> j) => TripEstimate(
      distanceKm: _num(j['distanceKm']),
      minutes: (j['minutes'] as num).toInt(),
      polyline: j['polyline'] as String?,
    );

/// What the backend's commuteRequestSchema accepts. Pickup time and start
/// date are not sent: the server works them out.
Map<String, Object?> commuteRequestToJson(CommuteRequest r) => {
      'pickup': placeToJson(r.pickup),
      'drop': placeToJson(r.drop),
      'repeat': _repeatKey(r.repeat),
      'weekdays': (r.weekdays.toList()..sort()),
      'timing': _timingKey(r.timing),
      'time': r.time,
      'returnTrip': r.returnTrip,
      'returnTime': r.returnTime,
      'rideType': r.rideType.configKey,
      'preferences': [for (final p in r.preferences) preferenceKey(p)],
    };

Driver driverFromJson(Map<String, Object?> j) => Driver(
      id: j['id'] as String,
      name: j['name'] as String,
      vehicle: j['vehicle'] as String,
      plate: j['plate'] as String,
      rating: _num(j['rating']),
      upiId: (j['upiId'] as String?) ?? '',
      phone: j['phone'] as String?,
    );

DriverMatch proposalFromJson(Map<String, Object?> j) => DriverMatch(
      proposalId: j['id'] as String,
      driver: driverFromJson(j['driver'] as Map<String, Object?>),
      fare: (j['fare'] as num).toInt(),
      trip: tripFromJson(j['trip'] as Map<String, Object?>),
      reasons: [for (final r in j['reasons'] as List) r as String],
      expiresAt: j['expiresAt'] == null ? null : DateTime.parse(j['expiresAt'] as String).toLocal(),
    );

Commute commuteFromJson(Map<String, Object?> j) {
  final weekdays = {for (final d in j['weekdays'] as List) (d as num).toInt()};
  final request = CommuteRequest(
    pickup: placeFromJson(j['pickup'] as Map<String, Object?>),
    drop: placeFromJson(j['drop'] as Map<String, Object?>),
    repeat: _repeatFrom(j['repeat'] as String),
    weekdays: weekdays,
    timing: _timingFrom(j['timing'] as String),
    time: (j['time'] as num).toInt(),
    pickupTime: (j['pickupTime'] as num).toInt(),
    returnTrip: j['returnTrip'] as bool,
    returnTime: (j['returnTime'] as num).toInt(),
    startDate: dateFrom(j['startDate'] as String),
    rideType: RideTypeInfo.fromKey(j['rideType'] as String),
    preferences: {for (final p in (j['preferences'] as List? ?? const [])) _preferenceFrom(p as String)},
  );
  return Commute(
    id: j['id'] as String,
    request: request,
    driver: driverFromJson(j['driver'] as Map<String, Object?>),
    fare: (j['fare'] as num).toInt(),
    usualPayment: paymentFrom(j['usualPayment'] as String),
    status: commuteStatusFrom(j['status'] as String),
    respondBy: j['respondBy'] == null ? null : DateTime.parse(j['respondBy'] as String).toLocal(),
    trip: j['trip'] == null ? null : tripFromJson(j['trip'] as Map<String, Object?>),
    declineReason: switch (j['declineReason']) {
      'declined' => DeclineReason.declined,
      'no_answer' => DeclineReason.noAnswer,
      _ => null,
    },
  );
}

RiderProfile riderFromJson(Map<String, Object?> j) =>
    RiderProfile(phone: j['phone'] as String, name: j['name'] as String?);

BookingOptions bookingOptionsFromJson(Map<String, Object?> j) {
  final flow = BookingFlow.values.firstWhere((f) => f.configKey == j['flow']);
  final options = <RideTypeOption>[];
  for (final o in j['options'] as List) {
    final m = o as Map<String, Object?>;
    final key = m['type'] as String;
    // Skip ride types this app version doesn't know rather than failing.
    if (!RideType.values.any((t) => t.configKey == key)) continue;
    options.add(RideTypeOption(
      type: RideTypeInfo.fromKey(key),
      fare: (m['fare'] as num).toInt(),
      driversOnRoute: (m['driversOnRoute'] as num).toInt(),
    ));
  }
  final def = j['defaultType'] as String?;
  return BookingOptions(
    flow: flow,
    options: options,
    defaultType: def == null || !options.any((o) => o.type.configKey == def) ? null : RideTypeInfo.fromKey(def),
  );
}

// ---------------------------------------------------------------- flow R2

RideState rideStateFrom(String s) => switch (s) {
      'scheduled' => RideState.scheduled,
      'on_the_way' => RideState.onTheWay,
      'arrived' => RideState.arrived,
      'in_ride' => RideState.inRide,
      'completed' => RideState.completed,
      'no_show' => RideState.noShow,
      'skipped' => RideState.skipped,
      _ => RideState.cancelled,
    };

PaymentStatus paymentStatusFrom(String s) => switch (s) {
      'paid' => PaymentStatus.paid,
      'paid_by_rider' => PaymentStatus.paidByRider,
      'unpaid' => PaymentStatus.unpaid,
      'disputed' => PaymentStatus.disputed,
      _ => PaymentStatus.unmarked,
    };

DriverPaymentMark? _driverMarkFrom(Object? s) => switch (s) {
      'upi' => DriverPaymentMark.upi,
      'cash' => DriverPaymentMark.cash,
      'not_paid' => DriverPaymentMark.notPaid,
      _ => null,
    };

DateTime? _time(Object? s) => s == null ? null : DateTime.parse(s as String).toLocal();

Map<String, Object?> ratingToJson(RideRating r) => {
      'stars': r.stars,
      'tags': r.tags,
      if (r.note != null && r.note!.trim().isNotEmpty) 'note': r.note!.trim(),
      'dontMatch': r.dontMatch,
    };

RideRating? _ratingFrom(Object? j) {
  if (j is! Map<String, Object?>) return null;
  return RideRating(
    stars: (j['stars'] as num).toInt(),
    tags: [for (final t in j['tags'] as List? ?? const []) t as String],
    note: j['note'] as String?,
    dontMatch: (j['dontMatch'] as bool?) ?? false,
  );
}

Ride rideFromJson(Map<String, Object?> j) => Ride(
      id: j['id'] as String,
      date: dateFrom(j['date'] as String),
      direction: j['direction'] == 'return' ? RideDirection.returnLeg : RideDirection.outbound,
      state: rideStateFrom(j['state'] as String),
      pickup: placeFromJson(j['pickup'] as Map<String, Object?>),
      drop: placeFromJson(j['drop'] as Map<String, Object?>),
      pickupAt: _time(j['pickupAt'])!,
      windowStart: _time(j['windowStart'])!,
      windowEnd: _time(j['windowEnd'])!,
      tripMinutes: (j['tripMinutes'] as num).toInt(),
      fare: (j['fare'] as num).toInt(),
      rideType: RideTypeInfo.fromKey(j['rideType'] as String),
      driver: driverFromJson(j['driver'] as Map<String, Object?>),
      pin: j['pin'] as String?,
      pinLocked: (j['pinLocked'] as bool?) ?? false,
      waitEndsAt: _time(j['waitEndsAt']),
      riderLateUsed: (j['riderLateUsed'] as bool?) ?? false,
      late: (j['late'] as bool?) ?? false,
      riderPayment: j['riderPayment'] == null ? null : paymentFrom(j['riderPayment'] as String),
      driverPayment: _driverMarkFrom(j['driverPayment']),
      paymentStatus: paymentStatusFrom((j['paymentStatus'] as String?) ?? 'unmarked'),
      rating: _ratingFrom(j['rating']),
      endedAt: _time(j['endedAt']),
      route: j['route'] as String?,
    );

LiveRide liveRideFromJson(Map<String, Object?> j) {
  final d = j['driver'] as Map<String, Object?>?;
  return LiveRide(
    state: rideStateFrom(j['state'] as String),
    serverTime: _time(j['serverTime'])!,
    driverAt: d == null ? null : GeoPoint(_num(d['lat']), _num(d['lng'])),
    positionAt: d == null ? null : _time(d['at']),
    etaMinutes: (j['etaMinutes'] as num?)?.toInt(),
    etaAt: _time(j['etaAt']),
    waitEndsAt: _time(j['waitEndsAt']),
    late: (j['late'] as bool?) ?? false,
    pinLocked: (j['pinLocked'] as bool?) ?? false,
    approachRoute: j['approachRoute'] as String?,
  );
}
