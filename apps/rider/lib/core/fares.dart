import 'dart:math' as math;

import 'models.dart';

/// Suggested-fare rules. The real values live in backend config; these are
/// the demo defaults and match the backend's seed config.
class FareRule {
  const FareRule({required this.base, required this.perKm, required this.minimum});
  final int base;
  final int perKm;
  final int minimum;
}

const fareRules = <RideType, FareRule>{
  RideType.bike: FareRule(base: 20, perKm: 6, minimum: 35),
  RideType.auto: FareRule(base: 30, perKm: 11, minimum: 40),
  RideType.cabHatchback: FareRule(base: 50, perKm: 14, minimum: 90),
  RideType.cabSedan: FareRule(base: 60, perKm: 16, minimum: 110),
  RideType.cabSuv: FareRule(base: 80, perKm: 21, minimum: 150),
};

/// Fare per ride in rupees, rounded to the nearest ₹5.
int suggestedFare(RideType type, double distanceKm) {
  final rule = fareRules[type]!;
  final raw = math.max(rule.minimum.toDouble(), rule.base + rule.perKm * distanceKm);
  return (raw / 5).round() * 5;
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

/// Rough road trip estimate used by the demo backend: roads are ~1.85x the
/// straight line in Bengaluru, at ~19 km/h average commute speed.
TripEstimate estimateRoadTrip(Place a, Place b) {
  final km = haversineKm(a.lat, a.lng, b.lat, b.lng) * 1.85;
  final rounded = (km * 10).round() / 10;
  final minutes = math.max(5, (km / 19 * 60).round());
  return TripEstimate(distanceKm: rounded, minutes: minutes);
}

String formatRupees(int amount) => '₹$amount';
