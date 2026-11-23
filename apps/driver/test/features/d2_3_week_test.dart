import 'package:driver/core/fake_api.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/driver_harness.dart';
import '../helpers/driver_robot.dart';

/// D2.3 Your week (spec "Screen designs"), reached from the D2.1 win banner.
void main() {
  Future<(DriverRobot, String)> winAndOpenWeek(WidgetTester tester) async {
    final h = await pumpDriverApp(tester);
    final robot = DriverRobot(tester, h);
    await robot.signInToRequests();
    final o = await robot.seriesOffer();
    await robot.interested(o);
    await robot.closeWindow(o);
    await robot.tap('requests.winBanner');
    expect(find.text('Your week'), findsOneWidget);
    return (robot, o.rider.firstName);
  }

  group('D2.3 Your week', () {
    testWidgets('D2.3 header: week range and ride count', (tester) async {
      await winAndOpenWeek(tester);
      expect(find.text('Your week'), findsOneWidget);
      expect(find.textContaining(RegExp(r' · \d+ rides?$')), findsOneWidget);
      await leave(tester);
    });

    testWidgets('D2.3 win banner names the rider and the start date', (tester) async {
      final (_, rider) = await winAndOpenWeek(tester);
      expect(find.text("You're $rider's regular driver from Mon 23 Nov."), findsOneWidget);
      expect(find.text("You were picked for $rider's request, and $rider has been told."), findsOneWidget);
      await leave(tester);
    });

    testWidgets('D2.3 the new rider has a row tagged "New rider" with rides on their days', (tester) async {
      await winAndOpenWeek(tester);
      expect(find.text('New rider'), findsOneWidget);
      expect(find.textContaining('Priya'), findsWidgets);
      for (final d in ['Mon', 'Wed', 'Fri']) {
        expect(find.textContaining(d), findsWidgets, reason: d);
      }
      await leave(tester);
    });

    testWidgets('D2.3 free time and buttons', (tester) async {
      final (robot, _) = await winAndOpenWeek(tester);
      expect(find.textContaining('Free every weekday '), findsOneWidget);
      expect(find.text('Open to midday commute requests'), findsOneWidget);
      expect(find.text('Take time off'), findsOneWidget);
      await robot.tapText('Requests');
      expect(find.textContaining("Tap I'm interested;"), findsOneWidget);
      await leave(tester);
    });

    testWidgets('D2.3 rides.week: the won rides from the start date, flagged new, 14 days ahead', (tester) async {
      final (robot, _) = await winAndOpenWeek(tester);
      final api = robot.api;
      final first = await api.week(start: DateTime(2026, 11, 23));
      expect(first.rides.map((r) => r.weekday).toSet(), {1, 3, 5});
      expect(first.rides.every((r) => r.isNew), isTrue);
      expect(first.rides.every((r) => r.riderName.startsWith('Priya')), isTrue);
      final second = await api.week(start: DateTime(2026, 11, 30));
      // Rides exist 14 days ahead: at least Mon 30 Nov and Wed 2 Dec.
      expect(second.rides.map((r) => r.weekday).toSet(), containsAll([1, 3]));
      // Thu 19 Nov + 14 days: nothing created yet for the week of 7 Dec.
      final third = await api.week(start: DateTime(2026, 12, 7));
      expect(third.rides, isEmpty);
      await leave(tester);
    });
  });

  testWidgets('D2.3 opened from the bottom bar without a win shows no banner', (tester) async {
    final h = await pumpDriverApp(tester, backend: (now) => FakeDriverApi(now: now));
    final robot = DriverRobot(tester, h);
    await robot.signIn();
    await robot.tap('tab.today');
    expect(find.textContaining("regular driver from"), findsNothing);
    expect(find.text('New rider'), findsNothing);
    await leave(tester);
  });
}
