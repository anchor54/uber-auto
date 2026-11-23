/// JSON shapes of the backend API (backend/API.md), mapped to the app's models.
library;

import 'models.dart';
import 'schedule.dart';

String timingKey(TimingMode t) => switch (t) {
      TimingMode.pickupAt => 'pickup_at',
      TimingMode.arriveBy => 'arrive_by',
    };

TimingMode timingFrom(Object? s) => s == 'arrive_by' ? TimingMode.arriveBy : TimingMode.pickupAt;

String directionKey(RideDirection d) => d == RideDirection.returnLeg ? 'return' : 'outbound';

RideDirection directionFrom(Object? s) => s == 'return' ? RideDirection.returnLeg : RideDirection.outbound;

String preferenceKey(RidePreference p) => switch (p) {
      RidePreference.acOn => 'ac',
      RidePreference.quiet => 'quiet',
      RidePreference.womanDriver => 'woman_driver',
    };

RidePreference? _preferenceFrom(Object? s) => switch (s) {
      'ac' || 'ac_on' => RidePreference.acOn,
      'quiet' => RidePreference.quiet,
      'woman_driver' => RidePreference.womanDriver,
      _ => null,
    };

Set<RidePreference> preferencesFrom(Object? list) =>
    {for (final p in (list as List? ?? const [])) if (_preferenceFrom(p) case final v?) v};

PaymentMethod paymentFrom(Object? s) => s == 'cash' ? PaymentMethod.cash : PaymentMethod.upi;

String paymentKey(PaymentMethod m) => m.name; // 'upi' | 'cash'

SeriesStatus seriesStatusFrom(Object? s) => switch (s) {
      'finding_driver' => SeriesStatus.findingDriver,
      'active' => SeriesStatus.active,
      'paused' => SeriesStatus.paused,
      'no_driver_found' => SeriesStatus.noDriverFound,
      _ => SeriesStatus.ended,
    };

OfferStatus offerStatusFrom(Object? s) => switch (s) {
      'open' => OfferStatus.open,
      'closing' => OfferStatus.closing,
      _ => OfferStatus.closed,
    };

OfferOutcome? offerOutcomeFrom(Object? s) => switch (s) {
      'assigned' => OfferOutcome.assigned,
      'no_fit' => OfferOutcome.noFit,
      'cancelled' => OfferOutcome.cancelled,
      _ => null,
    };

/// "2026-10-22" as a local date.
DateTime dateFrom(String s) {
  final p = s.split('-').map(int.parse).toList();
  return DateTime(p[0], p[1], p[2]);
}

String dateKey(DateTime d) =>
    '${d.year.toString().padLeft(4, '0')}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

double _num(Object? v) => (v as num).toDouble();

/// What the backend's series request takes: `{lat, lng, label}`.
Map<String, Object?> placeToJson(Place p) => {'lat': p.lat, 'lng': p.lng, 'label': p.title};

/// Place search results (`{id, title, detail, lat, lng}`) and series places
/// (`{lat, lng, label}`).
Place placeFromJson(Map<String, Object?> j) {
  final title = (j['title'] as String?) ?? (j['label'] as String?) ?? '';
  return Place(
    id: (j['id'] as String?) ?? title,
    title: title,
    detail: (j['detail'] as String?) ?? '',
    lat: _num(j['lat']),
    lng: _num(j['lng']),
  );
}

Map<String, Object?> slotToJson(SlotRequest s) => {
      'weekday': s.weekday,
      'direction': directionKey(s.direction),
      'timing': timingKey(s.timing),
      'time': clockKey(s.time),
    };

/// The series request body (series.preview, series.create, series.retry).
Map<String, Object?> commuteRequestToJson(CommuteRequest r) => {
      'pickup': placeToJson(r.pickup),
      'drop': placeToJson(r.drop),
      'vehicle_type': r.rideType.configKey,
      'preferences': [for (final p in r.preferences) preferenceKey(p)],
      'slots': [for (final s in r.slots) slotToJson(s)],
      'usual_payment': paymentKey(r.usualPayment),
      if (r.startDate != null) 'start_date': dateKey(r.startDate!),
    };

int? _clockOrNull(Object? s) => s is String && s.isNotEmpty ? parseClock(s) : null;

Driver driverFromJson(Map<String, Object?> j) => Driver(
      id: j['id'] as String,
      name: (j['name'] as String?) ?? '',
      vehicle: (j['vehicle_desc'] as String?) ?? (j['vehicle'] as String?) ?? '',
      plate: (j['vehicle_number'] as String?) ?? (j['plate'] as String?) ?? '',
      rating: j['rating'] == null ? 0 : _num(j['rating']),
      upiId: (j['upi_id'] as String?) ?? (j['upiId'] as String?) ?? '',
      phone: j['phone'] as String?,
      rideType: RideTypeInfo.tryKey(j['vehicle_type'] as String?),
    );

SeriesSlot seriesSlotFromJson(Map<String, Object?> j) => SeriesSlot(
      id: j['id'] as String?,
      weekday: (j['weekday'] as num).toInt(),
      direction: directionFrom(j['direction']),
      timing: timingFrom(j['timing']),
      arriveByTime: _clockOrNull(j['arrive_by_time']),
      pickupTime: parseClock(j['local_pickup_time'] as String),
      estimatedDropTime: _clockOrNull(j['estimated_drop_time']),
      driver: j['driver'] is Map<String, Object?> ? driverFromJson(j['driver'] as Map<String, Object?>) : null,
      active: (j['active'] as bool?) ?? true,
    );

List<SeriesSlot> _slots(Object? list) => [for (final s in (list as List? ?? const [])) seriesSlotFromJson(s as Map<String, Object?>)];

DateTime? _time(Object? s) => s == null ? null : DateTime.parse(s as String).toLocal();

SeriesPreview seriesPreviewFromJson(Map<String, Object?> j) => SeriesPreview(
      rideType: RideTypeInfo.fromKey((j['vehicle_type'] ?? j['ride_type']) as String),
      distanceKm: _num(j['distance_km']),
      travelMinutes: (j['travel_minutes'] as num).toInt(),
      startDate: dateFrom(j['start_date'] as String),
      suggestedFare: (j['suggested_fare'] as num).toInt(),
      ridesPerMonth: (j['rides_per_month'] as num?)?.toInt() ?? 0,
      slots: _slots(j['slots']),
    );

SeriesOffer? _offerFrom(Object? j) {
  if (j is! Map<String, Object?>) return null;
  return SeriesOffer(
    id: j['id'] as String,
    round: (j['round'] as num).toInt(),
    status: offerStatusFrom(j['status']),
    openedAt: _time(j['opened_at'])!,
    expiresAt: _time(j['expires_at'])!,
    outcome: offerOutcomeFrom(j['outcome']),
  );
}

Series seriesFromJson(Map<String, Object?> j) => Series(
      id: j['id'] as String,
      status: seriesStatusFrom(j['status']),
      city: (j['city'] as String?) ?? 'blr',
      rideType: RideTypeInfo.fromKey((j['vehicle_type'] ?? j['ride_type']) as String),
      preferences: preferencesFrom(j['preferences']),
      pickup: placeFromJson(j['pickup'] as Map<String, Object?>),
      drop: placeFromJson(j['drop'] as Map<String, Object?>),
      startDate: dateFrom(j['start_date'] as String),
      distanceKm: _num(j['distance_km']),
      travelMinutes: (j['travel_minutes'] as num).toInt(),
      suggestedFare: (j['suggested_fare'] as num).toInt(),
      usualPayment: paymentFrom(j['usual_payment']),
      requestedAt: _time(j['requested_at'])!,
      version: (j['version'] as num?)?.toInt() ?? 1,
      slots: _slots(j['slots']),
      offer: _offerFrom(j['offer']),
    );

RiderProfile riderFromJson(Map<String, Object?> j) {
  final name = j['name'] as String?;
  return RiderProfile(phone: j['phone'] as String, name: name == null || name.isEmpty ? null : name);
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
