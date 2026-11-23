import 'package:driver/app.dart';
import 'package:driver/core/clock.dart';
import 'package:driver/core/fake_api.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// Thu 19 Nov 2026, 10:00 AM: the spec seed's clock. Every driver widget
/// test starts here, so the seeded request (Priya, Mon/Wed/Fri 08:30 from
/// Mon 23 Nov) opens at 10:00 and closes at 10:15.
final driverTestNow = DateTime(2026, 11, 19, 10);

/// Phone-sized viewport (390 x 844 logical pixels, as in the designs).
const phoneSize = Size(390, 844);

/// The app and its demo backend share one clock the test moves.
class DriverHarness {
  DriverHarness(this.api, this.clock);
  final FakeDriverApi api;
  final ManualClock clock;
}

Future<DriverHarness> pumpDriverApp(
  WidgetTester tester, {
  FakeDriverApi Function(DateTime Function() now)? backend,
  Size size = phoneSize,
  DateTime? now,
}) async {
  tester.view.physicalSize = size * 3;
  tester.view.devicePixelRatio = 3;
  addTearDown(tester.view.reset);
  final clock = ManualClock(now ?? driverTestNow);
  final api = backend?.call(clock.now) ?? FakeDriverApi(now: clock.now);
  await tester.pumpWidget(DriverApp(api: api, clock: clock));
  await tester.pumpAndSettle();
  return DriverHarness(api, clock);
}

/// Unmounts the app so countdown timers stop.
Future<void> leave(WidgetTester tester) => tester.pumpWidget(const SizedBox());

const _weekdayShort = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
const _monthShort = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/// 12-hour clock, as in the designs: "10:15 AM".
String hm12(DateTime t) {
  final h = t.hour % 12 == 0 ? 12 : t.hour % 12;
  return '$h:${t.minute.toString().padLeft(2, '0')} ${t.hour < 12 ? 'AM' : 'PM'}';
}

/// Minutes after midnight in the 12-hour clock: 510 -> "8:30 AM".
String minutes12(int m) => hm12(DateTime(2000, 1, 1, m ~/ 60, m % 60));

/// "Mon 23 Nov".
String dayDate(DateTime d) => '${_weekdayShort[d.weekday - 1]} ${d.day} ${_monthShort[d.month - 1]}';
