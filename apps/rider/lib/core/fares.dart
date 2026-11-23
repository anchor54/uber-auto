import 'dart:math' as math;

import 'models.dart';

/// Travel and fare rules, as the backend applies them (backend/internal/travel,
/// seeded from backend/internal/seed): the demo backend uses these so demo
/// mode shows the same numbers as the real one.
class FareRule {
  const FareRule({required this.base, required this.perKm, required this.minimum, required this.speedKmh});
  final int base;
  final int perKm;
  final int minimum;

  /// Average speed for travel time.
  final double speedKmh;
}

const fareRules = <RideType, FareRule>{
  RideType.bike: FareRule(base: 20, perKm: 6, minimum: 35, speedKmh: 20),
  RideType.auto: FareRule(base: 30, perKm: 11, minimum: 40, speedKmh: 18),
  RideType.cabHatchback: FareRule(base: 50, perKm: 14, minimum: 90, speedKmh: 20),
  RideType.cabSedan: FareRule(base: 60, perKm: 16, minimum: 110, speedKmh: 20),
  RideType.cabSuv: FareRule(base: 80, perKm: 21, minimum: 150, speedKmh: 20),
};

/// Roads are this much longer than the straight line.
const roadFactor = 1.4;

/// Fare per ride in rupees for [roadKm]: base plus per km, at least the
/// minimum, to the nearest rupee.
int suggestedFare(RideType type, double roadKm) {
  final rule = fareRules[type]!;
  final raw = math.max(rule.minimum.toDouble(), rule.base + rule.perKm * roadKm);
  return (raw + 0.5).floor();
}

/// Straight-line distance in km between two coordinates.
double haversineKm(double lat1, double lng1, double lat2, double lng2) {
  const r = 6371.0;
  double rad(double d) => d * math.pi / 180;
  final dLat = rad(lat2 - lat1);
  final dLng = rad(lng2 - lng1);
  final a = math.sin(dLat / 2) * math.sin(dLat / 2) +
      math.cos(rad(lat1)) * math.cos(rad(lat2)) * math.sin(dLng / 2) * math.sin(dLng / 2);
  return 2 * r * math.asin(math.sqrt(a));
}

/// Road distance between two places: the straight line × [roadFactor].
double roadKm(Place a, Place b) => haversineKm(a.lat, a.lng, b.lat, b.lng) * roadFactor;

/// Minutes to drive [km] at [type]'s speed, rounded up.
int travelMinutes(RideType type, double km) {
  if (km <= 0) return 0;
  final m = (km / fareRules[type]!.speedKmh * 60 * 1e6).round() / 1e6;
  return m.ceil();
}

/// The trip between two places by [type]: road km to one decimal, minutes.
TripEstimate estimateRoadTrip(Place a, Place b, {RideType type = RideType.auto}) {
  final km = roadKm(a, b);
  return TripEstimate(distanceKm: (km * 10).round() / 10, minutes: math.max(1, travelMinutes(type, km)));
}

String formatRupees(int amount) => '₹$amount';
