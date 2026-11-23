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
