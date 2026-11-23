import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

/// The one place tests build a [CommuteRequest] (the body of series.preview,
/// series.create and series.retry), so a change to its constructor is
/// fixed here only.
///
/// [dayTimes] overrides the outbound time of single weekdays ("Same time
/// every day" off), in minutes after midnight.
CommuteRequest commuteRequest({
  Place? pickup,
  Place? drop,
  Set<int> weekdays = const {1, 2, 3, 4, 5},
  TimingMode timing = TimingMode.pickupAt,
  int time = 520, // 8:40 AM
  Map<int, int> dayTimes = const {},
  bool returnTrip = true,
  int returnTime = 1110, // 6:30 PM
  RideType rideType = RideType.cabSedan,
  Set<RidePreference> preferences = const {},
  PaymentMethod usualPayment = PaymentMethod.upi,
  DateTime? startDate,
}) {
  final days = weekdays.toList()..sort();
  return CommuteRequest(
    pickup: pickup ?? demoPlaces.firstWhere((p) => p.id == 'home'),
    drop: drop ?? demoPlaces.firstWhere((p) => p.id == 'etv'),
    slots: [
      for (final d in days) SlotRequest(weekday: d, timing: timing, time: dayTimes[d] ?? time),
      if (returnTrip)
        for (final d in days) SlotRequest(weekday: d, direction: RideDirection.returnLeg, time: returnTime),
    ],
    rideType: rideType,
    preferences: preferences,
    usualPayment: usualPayment,
    startDate: startDate,
  );
}

/// "MON" … "SUN", as in the test keys setup.time.MON and home.slot.MON.
const dayKeys = ['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT', 'SUN'];
String dayKey(int isoWeekday) => dayKeys[isoWeekday - 1];

/// 520 -> "08:40", the backend's local time format.
String hhmm(int minutes) => '${(minutes ~/ 60).toString().padLeft(2, '0')}:${(minutes % 60).toString().padLeft(2, '0')}';
