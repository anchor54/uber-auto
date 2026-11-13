import 'package:flutter/foundation.dart';

import '../../core/models.dart';
import '../../core/schedule.dart';

/// Holds what the rider has chosen across R1.2 – R1.5.
class CommuteSetup extends ChangeNotifier {
  CommuteSetup({required this.today});

  /// The choices from an earlier commute, e.g. to find another driver after
  /// one declined. The start date is worked out again from [today]; [trip]
  /// (the one the server booked with) keeps an arrive-by pickup the same.
  factory CommuteSetup.fromRequest(CommuteRequest r, {required DateTime today, TripEstimate? trip}) {
    return CommuteSetup(today: today)
      ..trip = trip
      ..pickup = r.pickup
      ..drop = r.drop
      ..repeat = r.repeat
      ..chosenDays = {...r.weekdays}
      ..timing = r.timing
      ..time = r.time
      ..returnTrip = r.returnTrip
      ..returnTime = r.returnTime
      ..rideType = r.rideType
      ..preferences = {...r.preferences};
  }

  final DateTime today;

  Place? pickup;
  Place? drop;
  CommuteRepeat repeat = CommuteRepeat.everyWeekday;
  Set<int> chosenDays = {...weekdaysMonToFri};
  TimingMode timing = TimingMode.pickupAt;

  /// Minutes after midnight: pickup time, or arrive-by time in that mode.
  int time = 8 * 60 + 40;
  bool returnTrip = true;
  int returnTime = 18 * 60 + 30;

  TripEstimate? trip;
  BookingOptions? options;
  RideType? rideType;
  Set<RidePreference> preferences = {RidePreference.acOn};
  DriverMatch? match;
  PaymentMethod usualPayment = PaymentMethod.upi;

  Set<int> get weekdays => repeat == CommuteRepeat.everyWeekday ? weekdaysMonToFri : chosenDays;

  BookingFlow get flow =>
      repeat == CommuteRepeat.everyWeekday ? BookingFlow.recurringWeekday : BookingFlow.recurringWeekly;

  int get pickupTime {
    if (timing == TimingMode.pickupAt) return time;
    return pickupForArrival(time, trip?.minutes ?? 35);
  }

  DateTime get startDate => firstRideDate(today, weekdays);

  bool get samePlace => pickup != null && drop != null && pickup == drop;

  /// Why the rider can't continue yet, or null when they can.
  String? get blocker {
    if (pickup == null) return 'Choose a pickup';
    if (drop == null) return 'Choose a drop-off';
    if (samePlace) return "Pickup and drop-off can't be the same place";
    if (weekdays.isEmpty) return 'Pick at least one day';
    return null;
  }

  int? get selectedFare {
    final type = rideType;
    if (type == null) return null;
    return options?.optionFor(type)?.fare;
  }

  int? get lowestFare {
    final o = options?.options;
    if (o == null || o.isEmpty) return null;
    return o.map((e) => e.fare).reduce((a, b) => a < b ? a : b);
  }

  void setPickup(Place p) {
    pickup = p;
    _routeChanged();
  }

  void setDrop(Place p) {
    drop = p;
    _routeChanged();
  }

  void _routeChanged() {
    trip = null;
    options = null;
    match = null;
    notifyListeners();
  }

  void setRepeat(CommuteRepeat mode) {
    if (repeat == mode) return;
    repeat = mode;
    options = null;
    match = null;
    notifyListeners();
  }

  /// Tapping a day while on "Every weekday" switches to chosen days.
  void toggleDay(int isoWeekday) {
    if (repeat == CommuteRepeat.everyWeekday) {
      chosenDays = {...weekdaysMonToFri};
      repeat = CommuteRepeat.chosenDays;
      options = null;
    }
    final next = {...chosenDays};
    if (!next.remove(isoWeekday)) next.add(isoWeekday);
    chosenDays = next;
    match = null;
    notifyListeners();
  }

  void setTiming(TimingMode mode) {
    timing = mode;
    // Sensible defaults when switching: arrive by 9:30, or pick up at 8:40.
    time = mode == TimingMode.arriveBy ? 9 * 60 + 30 : 8 * 60 + 40;
    match = null;
    notifyListeners();
  }

  void setTime(int minutes) {
    time = minutes;
    match = null;
    notifyListeners();
  }

  void setReturnTrip(bool value) {
    returnTrip = value;
    match = null;
    notifyListeners();
  }

  void setReturnTime(int minutes) {
    returnTime = minutes;
    match = null;
    notifyListeners();
  }

  void setTrip(TripEstimate t) {
    trip = t;
    notifyListeners();
  }

  void setOptions(BookingOptions o) {
    options = o;
    final available = o.options.map((e) => e.type).toList();
    if (rideType == null || !available.contains(rideType)) {
      rideType = available.contains(o.defaultType) ? o.defaultType : (available.isEmpty ? null : available.first);
    }
    _fixPreferences();
    notifyListeners();
  }

  void setRideType(RideType t) {
    rideType = t;
    match = null;
    _fixPreferences();
    notifyListeners();
  }

  void togglePreference(RidePreference p) {
    final next = {...preferences};
    if (!next.remove(p)) next.add(p);
    preferences = next;
    match = null;
    notifyListeners();
  }

  void removePreference(RidePreference p) {
    preferences = {...preferences}..remove(p);
    match = null;
    notifyListeners();
  }

  /// "AC on" only makes sense for cabs.
  void _fixPreferences() {
    final t = rideType;
    if (t != null && !t.isCab) preferences = {...preferences}..remove(RidePreference.acOn);
  }

  void setMatch(DriverMatch m) {
    match = m;
    notifyListeners();
  }

  void setUsualPayment(PaymentMethod m) {
    usualPayment = m;
    notifyListeners();
  }

  CommuteRequest toRequest() => CommuteRequest(
        pickup: pickup!,
        drop: drop!,
        repeat: repeat,
        weekdays: {...weekdays},
        timing: timing,
        time: time,
        pickupTime: pickupTime,
        returnTrip: returnTrip,
        returnTime: returnTime,
        startDate: startDate,
        rideType: rideType!,
        preferences: {...preferences},
      );
}
