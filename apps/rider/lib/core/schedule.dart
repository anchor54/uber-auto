/// Date, time and schedule helpers. Pure functions so they are easy to test.
library;

const _weekdayShort = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
const _monthShort = [
  'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
  'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
];

const weekdaysMonToFri = {1, 2, 3, 4, 5};

String weekdayShort(int isoWeekday) => _weekdayShort[isoWeekday - 1];

/// 520 -> "8:40 AM"
String formatTime(int minutes) {
  final m = ((minutes % 1440) + 1440) % 1440;
  final h24 = m ~/ 60;
  final mm = m % 60;
  final suffix = h24 < 12 ? 'AM' : 'PM';
  final h12 = h24 % 12 == 0 ? 12 : h24 % 12;
  return '$h12:${mm.toString().padLeft(2, '0')} $suffix';
}

/// 520 -> ("8:40", "AM")
(String, String) splitTime(int minutes) {
  final parts = formatTime(minutes).split(' ');
  return (parts[0], parts[1]);
}

/// "Mon, 26 Oct"
String formatDate(DateTime d) =>
    '${_weekdayShort[d.weekday - 1]}, ${d.day} ${_monthShort[d.month - 1]}';

/// "Mon 26 Oct"
String formatDateCompact(DateTime d) =>
    '${_weekdayShort[d.weekday - 1]} ${d.day} ${_monthShort[d.month - 1]}';

DateTime dateOnly(DateTime d) => DateTime(d.year, d.month, d.day);

bool sameDay(DateTime a, DateTime b) =>
    a.year == b.year && a.month == b.month && a.day == b.day;

/// "Driver may arrive 8:35–8:45": a 10-minute window around the pickup.
String pickupWindow(int pickupTime) =>
    '${formatTime(pickupTime - 5).split(' ').first}–${formatTime(pickupTime + 5).split(' ').first}';

/// The first day after [today] whose weekday is in [weekdays].
/// Rides never start on the day they are booked.
DateTime firstRideDate(DateTime today, Set<int> weekdays) {
  assert(weekdays.isNotEmpty);
  var d = dateOnly(today).add(const Duration(days: 1));
  for (var i = 0; i < 7; i++) {
    if (weekdays.contains(d.weekday)) return d;
    d = d.add(const Duration(days: 1));
  }
  return d;
}

/// Ride days in an average month for the chosen weekdays.
int rideDaysPerMonth(Set<int> weekdays) => (weekdays.length * 52 / 12).round();

/// "About 44 (22 days × 2)" or "About 4" for one-way rides.
String ridesPerMonthLabel(Set<int> weekdays, {required bool returnTrip}) {
  final days = rideDaysPerMonth(weekdays);
  if (!returnTrip) return 'About $days';
  return 'About ${days * 2} ($days days × 2)';
}

/// "Mon–Fri", "Sat", "Mon, Wed, Fri", "Every day"
String daysLabel(Set<int> weekdays) {
  final sorted = weekdays.toList()..sort();
  if (sorted.length == 7) return 'Every day';
  if (sorted.length >= 3) {
    var consecutive = true;
    for (var i = 1; i < sorted.length; i++) {
      if (sorted[i] != sorted[i - 1] + 1) consecutive = false;
    }
    if (consecutive) {
      return '${weekdayShort(sorted.first)}–${weekdayShort(sorted.last)}';
    }
  }
  return sorted.map(weekdayShort).join(', ');
}

/// Pickup time for an arrive-by commute: leave enough time for the trip
/// plus a 10-minute buffer, rounded down to 5 minutes.
int pickupForArrival(int arriveBy, int tripMinutes) {
  final raw = arriveBy - tripMinutes - 10;
  return raw - (raw % 5);
}

/// Monday of the week that contains [d].
DateTime weekStart(DateTime d) =>
    dateOnly(d).subtract(Duration(days: d.weekday - 1));

String greetingFor(DateTime now) {
  if (now.hour < 12) return 'Good morning';
  if (now.hour < 17) return 'Good afternoon';
  return 'Good evening';
}
