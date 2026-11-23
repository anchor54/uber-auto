import 'package:flutter/foundation.dart';

import '../../core/models.dart';
import '../../core/schedule.dart';

/// Holds what the rider has chosen across R1.2 – R1.4.
class CommuteSetup extends ChangeNotifier {
  CommuteSetup({required this.today});

  /// The choices of an existing series: "Change times or ride type" on R1.7
  /// (then R1.4's button retries [retrySeriesId]), or "View request" on R1.5.
  factory CommuteSetup.fromSeries(Series s, {required DateTime today}) {
    final setup = CommuteSetup(today: today)
      ..pickup = s.pickup
      ..drop = s.drop
      ..rideType = s.rideType
      ..preferences = {...s.preferences}
      ..usualPayment = s.usualPayment
      ..retrySeriesId = s.id;
    final out = s.outbound;
    final days = {for (final x in out) x.weekday};
    setup.repeat = days.length == 5 && days.containsAll(weekdaysMonToFri) ? CommuteRepeat.everyWeekday : CommuteRepeat.chosenDays;
    setup.chosenDays = {...days};
    if (out.isNotEmpty) {
      setup.timing = out.first.timing;
      setup.time = out.first.enteredTime;
    }
    setup.dayTimes = {for (final x in out) x.weekday: x.enteredTime};
    final back = s.returns;
    setup.returnTrip = back.isNotEmpty;
    if (back.isNotEmpty) setup.returnTime = back.first.pickupTime;
    setup.returnDayTimes = {for (final x in back) x.weekday: x.pickupTime};
    setup.sameTime = setup.dayTimes.values.toSet().length <= 1 && setup.returnDayTimes.values.toSet().length <= 1;
    return setup;
  }

  final DateTime today;

  /// Set when these choices change a series that found no driver: R1.4 then
  /// retries it instead of creating a new one.
  String? retrySeriesId;

  Place? pickup;
  Place? drop;
  CommuteRepeat repeat = CommuteRepeat.everyWeekday;
  Set<int> chosenDays = {...weekdaysMonToFri};
  TimingMode timing = TimingMode.pickupAt;

  /// "Same time every day": one time for every chosen day. Off: [dayTimes].
  bool sameTime = true;

  /// Minutes after midnight: pickup time, or arrive-by time in that mode.
  int time = 8 * 60 + 40;

  /// Per weekday, when [sameTime] is off. Days missing here use [time].
  Map<int, int> dayTimes = {};

  bool returnTrip = true;
  int returnTime = 18 * 60 + 30;

  /// The return's per-weekday pickup times, when [sameTime] is off.
  Map<int, int> returnDayTimes = {};

  RideType? rideType;
  Set<RidePreference> preferences = {};
  PaymentMethod usualPayment = PaymentMethod.upi;

  /// R1.3's rows, loaded for the current route and times.
  List<RideTypeOption>? options;

  /// The server's view of the current choices (start date, fare, arrive-by
  /// pickups), or null while it loads or when it failed.
  SeriesPreview? preview;

  /// Why the last preview failed (e.g. `start_too_soon`), or null.
  ApiException? previewError;

  /// The type R1.2 previews with before the rider has chosen one.
  static const defaultRideType = RideType.auto;

  Set<int> get weekdays => repeat == CommuteRepeat.everyWeekday ? weekdaysMonToFri : chosenDays;

  List<int> get sortedDays => weekdays.toList()..sort();

  /// The time entered for [day]'s outbound leg.
  int timeFor(int day) => sameTime ? time : (dayTimes[day] ?? time);

  /// The return pickup time on [day].
  int returnTimeFor(int day) => sameTime ? returnTime : (returnDayTimes[day] ?? returnTime);

  /// The first chosen day: the one other days are compared with ("Different").
  int? get firstDay => weekdays.isEmpty ? null : sortedDays.first;

  bool differs(int day) => firstDay != null && day != firstDay && timeFor(day) != timeFor(firstDay!);

  bool returnDiffers(int day) => firstDay != null && day != firstDay && returnTimeFor(day) != returnTimeFor(firstDay!);

  /// The first chosen weekday from tomorrow on, or the server's when known.
  DateTime get startDate => preview?.startDate ?? firstRideDate(today, weekdays);

  bool get samePlace => pickup != null && drop != null && pickup == drop;

  /// Why the rider can't continue yet, or null when they can.
  String? get blocker {
    if (pickup == null) return 'Choose a pickup';
    if (drop == null) return 'Choose a drop-off';
    if (samePlace) return "Pickup and drop-off can't be the same place";
    if (weekdays.isEmpty) return 'Pick at least one day';
    return null;
  }

  /// Every input R1.2 needs is there: route, at least one day, a time for every day.
  bool get complete => blocker == null;

  int? get selectedFare {
    final type = rideType;
    if (type == null) return null;
    for (final o in options ?? const <RideTypeOption>[]) {
      if (o.type == type) return o.fare;
    }
    return null;
  }

  /// The server's pickup time for [day]'s leg (arrive-by worked out), or the entered time.
  int pickupTimeFor(int day, RideDirection direction) {
    for (final s in preview?.slots ?? const <SeriesSlot>[]) {
      if (s.weekday == day && s.direction == direction) return s.pickupTime;
    }
    return direction == RideDirection.returnLeg ? returnTimeFor(day) : timeFor(day);
  }

  void _changed({bool route = false}) {
    preview = null;
    previewError = null;
    if (route) options = null;
    notifyListeners();
  }

  void setPickup(Place p) {
    pickup = p;
    _changed(route: true);
  }

  void setDrop(Place p) {
    drop = p;
    _changed(route: true);
  }

  void setRepeat(CommuteRepeat mode) {
    if (repeat == mode) return;
    repeat = mode;
    _changed();
  }

  /// Tapping a day while on "Every weekday" switches to chosen days.
  void toggleDay(int isoWeekday) {
    if (repeat == CommuteRepeat.everyWeekday) {
      chosenDays = {...weekdaysMonToFri};
      repeat = CommuteRepeat.chosenDays;
    }
    final next = {...chosenDays};
    if (!next.remove(isoWeekday)) next.add(isoWeekday);
    chosenDays = next;
    _changed();
  }

  void setTiming(TimingMode mode) {
    if (timing == mode) return;
    timing = mode;
    // Sensible defaults when switching: arrive by 9:30, or pick up at 8:40.
    time = mode == TimingMode.arriveBy ? 9 * 60 + 30 : 8 * 60 + 40;
    dayTimes = {};
    _changed();
  }

  /// Turning "Same time every day" off starts every day at the shared time.
  void setSameTime(bool value) {
    if (sameTime == value) return;
    sameTime = value;
    if (!value) {
      dayTimes = {for (final d in weekdays) d: dayTimes[d] ?? time};
      returnDayTimes = {for (final d in weekdays) d: returnDayTimes[d] ?? returnTime};
    } else {
      final first = firstDay;
      if (first != null) {
        time = dayTimes[first] ?? time;
        returnTime = returnDayTimes[first] ?? returnTime;
      }
    }
    _changed();
  }

  void setTime(int minutes) {
    time = minutes;
    _changed();
  }

  void setDayTime(int day, int minutes) {
    dayTimes = {...dayTimes, day: minutes};
    _changed();
  }

  void setReturnTrip(bool value) {
    returnTrip = value;
    _changed();
  }

  void setReturnTime(int minutes) {
    returnTime = minutes;
    _changed();
  }

  void setReturnDayTime(int day, int minutes) {
    returnDayTimes = {...returnDayTimes, day: minutes};
    _changed();
  }

  void setPreview(SeriesPreview p) {
    preview = p;
    previewError = null;
    notifyListeners();
  }

  void setPreviewError(ApiException e) {
    preview = null;
    previewError = e;
    notifyListeners();
  }

  void setOptions(List<RideTypeOption> o) {
    options = o;
    final available = o.map((e) => e.type).toList();
    if (rideType == null || !available.contains(rideType)) {
      rideType = available.contains(defaultRideType) ? defaultRideType : (available.isEmpty ? null : available.first);
    }
    notifyListeners();
  }

  void setRideType(RideType t) {
    if (rideType == t) return;
    rideType = t;
    preview = null;
    notifyListeners();
  }

  void togglePreference(RidePreference p) {
    final next = {...preferences};
    if (!next.remove(p)) next.add(p);
    preferences = next;
    notifyListeners();
  }

  void setUsualPayment(PaymentMethod m) {
    usualPayment = m;
    notifyListeners();
  }

  List<SlotRequest> get slots => [
        for (final d in sortedDays) ...[
          SlotRequest(weekday: d, timing: timing, time: timeFor(d)),
          if (returnTrip) SlotRequest(weekday: d, direction: RideDirection.returnLeg, time: returnTimeFor(d)),
        ],
      ];

  /// The request as chosen so far; before R1.3, with [defaultRideType].
  CommuteRequest toRequest() => CommuteRequest(
        pickup: pickup!,
        drop: drop!,
        slots: slots,
        rideType: rideType ?? defaultRideType,
        preferences: {...preferences},
        usualPayment: usualPayment,
      );
}
