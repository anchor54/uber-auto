/// Date and time helpers. Pure functions so they are easy to test.
library;

const _weekdayShort = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
const _monthShort = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

String weekdayShort(int isoWeekday) => _weekdayShort[isoWeekday - 1];

/// 3 -> "Wednesdays"
String weekdayPlural(int isoWeekday) => const [
      'Mondays', 'Tuesdays', 'Wednesdays', 'Thursdays', 'Fridays', 'Saturdays', 'Sundays', //
    ][isoWeekday - 1];

/// 510 -> "8:30 AM"
String formatTime(int minutes) {
  final m = ((minutes % 1440) + 1440) % 1440;
  final h24 = m ~/ 60;
  final suffix = h24 < 12 ? 'AM' : 'PM';
  final h12 = h24 % 12 == 0 ? 12 : h24 % 12;
  return '$h12:${(m % 60).toString().padLeft(2, '0')} $suffix';
}

/// The clock time of [t] as "10:15 AM".
String formatClock(DateTime t) => formatTime(t.hour * 60 + t.minute);

/// "Mon 23 Nov"
String formatDate(DateTime d) => '${_weekdayShort[d.weekday - 1]} ${d.day} ${_monthShort[d.month - 1]}';

/// "23 Nov"
String formatDayMonth(DateTime d) => '${d.day} ${_monthShort[d.month - 1]}';

/// "23–29 Nov", or "30 Nov – 6 Dec" across months.
String formatWeekRange(DateTime start, DateTime end) => start.month == end.month
    ? '${start.day}–${end.day} ${_monthShort[end.month - 1]}'
    : '${formatDayMonth(start)} – ${formatDayMonth(end)}';

/// "08:30" -> 510.
int parseClock(String hhmm) {
  final p = hhmm.split(':');
  return int.parse(p[0]) * 60 + int.parse(p[1]);
}

DateTime dateOnly(DateTime d) => DateTime(d.year, d.month, d.day);

bool sameDay(DateTime a, DateTime b) => a.year == b.year && a.month == b.month && a.day == b.day;

/// Monday of the week that contains [d].
DateTime weekStart(DateTime d) => DateTime(d.year, d.month, d.day - (d.weekday - 1));

DateTime addDays(DateTime d, int n) => DateTime(d.year, d.month, d.day + n);

/// "Mon–Fri", "Sat", "Mon, Wed, Fri", "Every day"
String daysLabel(Set<int> weekdays) {
  final sorted = weekdays.toList()..sort();
  if (sorted.length == 7) return 'Every day';
  if (sorted.length >= 3 && _consecutive(sorted)) return '${weekdayShort(sorted.first)}–${weekdayShort(sorted.last)}';
  return sorted.map(weekdayShort).join(', ');
}

/// "Mon–Thu" for runs of two or more days, else as [daysLabel].
String daysRangeLabel(Set<int> weekdays) {
  final sorted = weekdays.toList()..sort();
  if (sorted.length >= 2 && sorted.length < 7 && _consecutive(sorted)) {
    return '${weekdayShort(sorted.first)}–${weekdayShort(sorted.last)}';
  }
  return daysLabel(weekdays);
}

bool _consecutive(List<int> sorted) {
  for (var i = 1; i < sorted.length; i++) {
    if (sorted[i] != sorted[i - 1] + 1) return false;
  }
  return true;
}

/// Days that share the same values, in weekday order: the schedule rows
/// ("Mon–Thu 8:40 AM 9:15 AM", "Fri 9:30 AM 10:05 AM").
List<(Set<int>, T)> groupDays<T>(Map<int, T> byDay) {
  final groups = <(Set<int>, T)>[];
  final days = byDay.keys.toList()..sort();
  for (final d in days) {
    final v = byDay[d] as T;
    final i = groups.indexWhere((g) => g.$2 == v);
    if (i < 0) {
      groups.add(({d}, v));
    } else {
      groups[i].$1.add(d);
    }
  }
  return groups;
}
