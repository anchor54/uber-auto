import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/schedule.dart';

void main() {
  final wednesday = DateTime(2026, 10, 21, 9);

  group('formatTime', () {
    test('morning, noon, evening and midnight', () {
      expect(formatTime(8 * 60 + 40), '8:40 AM');
      expect(formatTime(12 * 60), '12:00 PM');
      expect(formatTime(18 * 60 + 30), '6:30 PM');
      expect(formatTime(0), '12:00 AM');
    });

    test('wraps past midnight', () {
      expect(formatTime(24 * 60 + 5), '12:05 AM');
      expect(formatTime(-10), '11:50 PM');
    });
  });

  test('pickup window is five minutes either side', () {
    expect(pickupWindow(8 * 60 + 40), '8:35–8:45');
  });

  group('firstRideDate', () {
    test('weekday commutes start the next working day', () {
      expect(firstRideDate(wednesday, weekdaysMonToFri), DateTime(2026, 10, 22));
    });

    test('a Friday booking starts on Monday', () {
      expect(firstRideDate(DateTime(2026, 10, 23, 20), weekdaysMonToFri), DateTime(2026, 10, 26));
    });

    test('Saturday-only rides start on the coming Saturday', () {
      expect(firstRideDate(wednesday, {6}), DateTime(2026, 10, 24));
    });

    test('never starts on the day of booking', () {
      expect(firstRideDate(wednesday, {3}), DateTime(2026, 10, 28));
    });
  });

  group('rides per month', () {
    test('weekdays with a return trip', () {
      expect(ridesPerMonthLabel(weekdaysMonToFri, returnTrip: true), 'About 44 (22 days × 2)');
    });

    test('one day a week, one way', () {
      expect(ridesPerMonthLabel({6}, returnTrip: false), 'About 4');
    });
  });

  group('daysLabel', () {
    test('ranges, single days, lists and every day', () {
      expect(daysLabel(weekdaysMonToFri), 'Mon–Fri');
      expect(daysLabel({6}), 'Sat');
      expect(daysLabel({1, 3, 5}), 'Mon, Wed, Fri');
      expect(daysLabel({1, 2, 3, 4, 5, 6, 7}), 'Every day');
      expect(daysLabel({6, 7}), 'Sat, Sun');
    });
  });

  test('arrive-by pickup leaves trip time plus a buffer', () {
    // Arrive by 9:30 with a 38 minute trip: 9:30 - 48 min = 8:42, rounded down to 8:40.
    expect(pickupForArrival(9 * 60 + 30, 38), 8 * 60 + 40);
  });

  test('dates are formatted like the designs', () {
    expect(formatDate(DateTime(2026, 10, 26)), 'Mon, 26 Oct');
    expect(formatDateCompact(DateTime(2026, 10, 26)), 'Mon 26 Oct');
  });

  test('greeting follows the time of day', () {
    expect(greetingFor(DateTime(2026, 1, 1, 8)), 'Good morning');
    expect(greetingFor(DateTime(2026, 1, 1, 14)), 'Good afternoon');
    expect(greetingFor(DateTime(2026, 1, 1, 19)), 'Good evening');
  });
}
