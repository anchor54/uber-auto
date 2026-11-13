import 'dart:math';

import 'models.dart';

/// How a scripted demo ride ends.
enum FakeRideOutcome { completed, noShow }

/// A demo ride that plays itself out against a clock: on the way, arrived,
/// in the ride, completed (or a no-show). Times are in "ride minutes";
/// [speed] compresses them, so a demo can show a 38-minute ride in two.
///
/// The fake backend asks it for the state, position and ETA at any moment,
/// so tests move an injected clock instead of waiting.
class FakeRideScript {
  FakeRideScript({
    required this.departAt,
    required this.pickup,
    required this.drop,
    required this.tripMinutes,
    this.approachMinutes = 6,
    this.waitMinutes = 1,
    this.speed = 1,
    this.outcome = FakeRideOutcome.completed,
    this.lateMinutes = 0,
  });

  final DateTime departAt;
  final Place pickup;
  final Place drop;
  final int tripMinutes;

  /// Minutes from departing to arriving, before any lateness.
  final int approachMinutes;

  /// Minutes the driver waits before the rider gets in.
  final int waitMinutes;
  final double speed;
  final FakeRideOutcome outcome;

  /// Extra minutes on the way; any lateness flags the driver as late.
  final int lateMinutes;

  /// The rider said "I'm 5 min late": the wait runs 10 minutes.
  bool riderLate = false;

  Duration _ride(num minutes) => Duration(milliseconds: (minutes * 60000 / speed).round());

  DateTime get arriveAt => departAt.add(_ride(approachMinutes + lateMinutes));
  DateTime get waitEndsAt => arriveAt.add(_ride(riderLate ? 10 : 5));
  DateTime get startAt => arriveAt.add(_ride(waitMinutes));
  DateTime get endAt => startAt.add(_ride(tripMinutes));

  /// When the pickup was meant to be: where an on-time driver would arrive.
  DateTime get pickupAt => departAt.add(_ride(approachMinutes));

  bool get late => lateMinutes > 0;

  RideState stateAt(DateTime now) {
    if (now.isBefore(departAt)) return RideState.scheduled;
    if (now.isBefore(arriveAt)) return RideState.onTheWay;
    if (outcome == FakeRideOutcome.noShow) {
      return now.isBefore(waitEndsAt) ? RideState.arrived : RideState.noShow;
    }
    if (now.isBefore(startAt)) return RideState.arrived;
    if (now.isBefore(endAt)) return RideState.inRide;
    return RideState.completed;
  }

  /// About 2 km north of the pickup: where the driver sets off from.
  GeoPoint get startPoint => GeoPoint(pickup.lat + 0.018, pickup.lng);

  GeoPoint? driverAt(DateTime now) {
    switch (stateAt(now)) {
      case RideState.onTheWay:
        return _between(startPoint, GeoPoint(pickup.lat, pickup.lng), _fraction(now, departAt, arriveAt));
      case RideState.arrived:
        return GeoPoint(pickup.lat, pickup.lng);
      case RideState.inRide:
        return _between(GeoPoint(pickup.lat, pickup.lng), GeoPoint(drop.lat, drop.lng), _fraction(now, startAt, endAt));
      default:
        return null;
    }
  }

  /// Minutes to the pickup (on the way) or the drop-off (riding), in ride minutes.
  int? etaMinutes(DateTime now) {
    final until = switch (stateAt(now)) {
      RideState.onTheWay => arriveAt,
      RideState.inRide => endAt,
      RideState.arrived => now,
      _ => null,
    };
    if (until == null) return null;
    final realMs = until.difference(now).inMilliseconds;
    return max(0, (realMs * speed / 60000).ceil());
  }

  static double _fraction(DateTime now, DateTime from, DateTime to) {
    final total = to.difference(from).inMilliseconds;
    if (total <= 0) return 1;
    return (now.difference(from).inMilliseconds / total).clamp(0.0, 1.0);
  }

  static GeoPoint _between(GeoPoint a, GeoPoint b, double f) =>
      GeoPoint(a.lat + (b.lat - a.lat) * f, a.lng + (b.lng - a.lng) * f);
}
