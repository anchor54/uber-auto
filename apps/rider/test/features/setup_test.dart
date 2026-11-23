import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/models.dart';
import 'package:rider/core/schedule.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';
import '../helpers/series_fixtures.dart';

String textOf(WidgetTester tester, String key) => tester.widget<Text>(find.byKey(Key(key))).data!;

/// R1.2 Set up a regular ride (spec "Screen designs").
void main() {
  Future<R1Robot> openSetup(WidgetTester tester) async {
    await pumpRiderApp(tester);
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.tap('home.setup');
    return robot;
  }

  Future<R1Robot> openFilledSetup(WidgetTester tester) async {
    final robot = await openSetup(tester);
    await robot.choosePlace('setup.pickup', 'HSR', 'home');
    await robot.choosePlace('setup.drop', 'Embassy', 'etv');
    return robot;
  }

  group('R1.2 Set up a regular ride', () {
    testWidgets('R1.2 header, route, repeat and timing copy', (tester) async {
      await openSetup(tester);
      expect(find.text('Step 1 of 3'), findsOneWidget);
      expect(find.text('New commute'), findsOneWidget);
      expect(
        find.text("Same route, same time, every day you choose. We'll find you a regular driver for these days."),
        findsOneWidget,
      );
      expect(find.text('Every weekday'), findsOneWidget);
      expect(find.text('Chosen days each week'), findsOneWidget);
      expect(find.text('Weekly rides work too, e.g. only Saturdays. Pick the days below.'), findsOneWidget);
      for (var d = 1; d <= 7; d++) {
        expect(find.byKey(Key('setup.day.$d')), findsOneWidget);
      }
      expect(find.text('Pick me up at'), findsOneWidget);
      expect(find.text('Get me there by'), findsOneWidget);
      expect(find.text('Same time every day'), findsOneWidget);
      expect(find.text('Turn off to set a time for each day'), findsOneWidget);
      expect(find.text('Your driver may arrive up to 5 minutes either side of each time.'), findsOneWidget);
      expect(find.text('Add return trip'), findsOneWidget);
      expect(
        find.text('You pay your driver directly after each ride, by UPI or cash. Skip or change a day until 1 hour before pickup.'),
        findsOneWidget,
      );
      expect(find.text('Choose ride type'), findsOneWidget);
    });

    testWidgets('R1.2 "Same time every day" is on by default with a single time row', (tester) async {
      await openSetup(tester);
      final sw = tester.widget<Switch>(
        find.descendant(of: find.byKey(const Key('setup.sameTime')), matching: find.byType(Switch), matchRoot: true),
      );
      expect(sw.value, isTrue);
      // One row for every day, not one per day.
      expect(find.byKey(const Key('setup.time.TUE')), findsNothing);
      expect(find.byKey(const Key('setup.time.FRI')), findsNothing);
      expect(find.text('Different'), findsNothing);
    });

    testWidgets('R1.2 turning "Same time every day" off shows one row per chosen weekday', (tester) async {
      final robot = await openSetup(tester);
      await robot.perDayTimes();
      for (final d in ['MON', 'TUE', 'WED', 'THU', 'FRI']) {
        expect(find.byKey(Key('setup.time.$d')), findsOneWidget, reason: d);
      }
      expect(find.byKey(const Key('setup.time.SAT')), findsNothing);
      expect(find.byKey(const Key('setup.time.SUN')), findsNothing);
      // Each outbound day row has its own "Change" (return rows need not).
      for (final d in ['MON', 'TUE', 'WED', 'THU', 'FRI']) {
        expect(find.descendant(of: find.byKey(Key('setup.time.$d')), matching: find.text('Change')), findsOneWidget, reason: d);
      }
      expect(find.text('8:40 AM'), findsWidgets);
      expect(find.text('Different'), findsNothing);
    });

    testWidgets('R1.2 per-day rows follow the chosen days', (tester) async {
      final robot = await openSetup(tester);
      await robot.perDayTimes();
      await robot.tap('setup.day.3'); // Wednesday off
      expect(find.byKey(const Key('setup.time.WED')), findsNothing);
      await robot.tap('setup.day.6'); // Saturday on
      expect(find.byKey(const Key('setup.time.SAT')), findsOneWidget);
    });

    testWidgets('R1.2 a day whose time differs from the first chosen day is tagged "Different"', (tester) async {
      final robot = await openFilledSetup(tester);
      await robot.perDayTimes();
      await robot.setDayTime('FRI', '09:30');
      expect(find.text('9:30 AM'), findsOneWidget);
      expect(find.text('Different'), findsOneWidget);
      // Setting Friday back to the first day's time removes the tag.
      await robot.setDayTime('FRI', '08:40');
      expect(find.text('Different'), findsNothing);
    });

    testWidgets('R1.2 the time picker moves in 5-minute steps', (tester) async {
      final robot = await openFilledSetup(tester);
      await robot.perDayTimes();
      await robot.tap('setup.time.MON');
      expect(find.byKey(const Key('time.08:45')), findsOneWidget);
      expect(find.byKey(const Key('time.08:41')), findsNothing);
      await robot.tap('time.08:45');
    });

    testWidgets('R1.2 the return trip row reads "{time} every day from {drop-off}"', (tester) async {
      await openFilledSetup(tester);
      expect(find.text('6:30 PM every day from Embassy Tech Village'), findsOneWidget);
    });

    testWidgets('R1.2 with "Same time every day" off, the return gets its own per-day rows', (tester) async {
      final robot = await openFilledSetup(tester);
      await robot.perDayTimes();
      expect(find.text('6:30 PM every day from Embassy Tech Village'), findsNothing);
      // Outbound and return rows for each weekday.
      expect(find.text('6:30 PM'), findsNWidgets(5));
    });

    testWidgets('R1.2 summary rows: Starts, Rides in a month, Fare per ride', (tester) async {
      await openFilledSetup(tester);
      expect(find.text('Starts'), findsOneWidget);
      expect(find.text('Rides in a month'), findsOneWidget);
      expect(find.text('Fare per ride'), findsOneWidget);
      // Wed 21 Oct: the first chosen weekday from tomorrow on.
      expect(textOf(tester, 'setup.starts'), contains('22 Oct'));
      expect(textOf(tester, 'setup.ridesPerMonth'), ridesPerMonthLabel({1, 2, 3, 4, 5}, returnTrip: true));
      expect(textOf(tester, 'setup.ridesPerMonth'), endsWith('days × 2)'));
    });

    testWidgets('R1.2 "× 2" only with a return trip', (tester) async {
      final robot = await openFilledSetup(tester);
      await robot.tap('setup.return');
      expect(textOf(tester, 'setup.ridesPerMonth'), ridesPerMonthLabel({1, 2, 3, 4, 5}, returnTrip: false));
      expect(textOf(tester, 'setup.ridesPerMonth'), isNot(contains('× 2')));
    });

    testWidgets('R1.2 a Saturday-only ride starts this Saturday', (tester) async {
      final robot = await openFilledSetup(tester);
      await robot.tap('segment.Chosen days each week');
      for (final d in [1, 2, 3, 4, 5]) {
        await robot.tap('setup.day.$d');
      }
      await robot.tap('setup.day.6');
      expect(textOf(tester, 'setup.starts'), contains('24 Oct'));
      expect(textOf(tester, 'setup.ridesPerMonth'), ridesPerMonthLabel({6}, returnTrip: true));
    });

    testWidgets('R1.2 "Choose ride type" needs a pickup, a drop-off and at least one day', (tester) async {
      final robot = await openSetup(tester);
      expect(isEnabled(tester, 'setup.continue'), isFalse);
      await robot.choosePlace('setup.pickup', 'HSR', 'home');
      expect(isEnabled(tester, 'setup.continue'), isFalse);
      await robot.choosePlace('setup.drop', 'Embassy', 'etv');
      expect(isEnabled(tester, 'setup.continue'), isTrue);
      for (final d in [1, 2, 3, 4, 5]) {
        await robot.tap('setup.day.$d');
      }
      expect(isEnabled(tester, 'setup.continue'), isFalse);
      await robot.tap('setup.day.2');
      expect(isEnabled(tester, 'setup.continue'), isTrue);
    });

    testWidgets('R1.2 per-day times still need only a time for every chosen day', (tester) async {
      final robot = await openFilledSetup(tester);
      await robot.perDayTimes();
      expect(isEnabled(tester, 'setup.continue'), isTrue);
    });

    testWidgets('R1.2 the same place for pickup and drop-off is not allowed', (tester) async {
      final robot = await openSetup(tester);
      await robot.choosePlace('setup.pickup', 'HSR', 'home');
      await robot.choosePlace('setup.drop', 'HSR', 'home');
      expect(isEnabled(tester, 'setup.continue'), isFalse);
    });

    testWidgets('R1.2 place search shows a message when nothing matches', (tester) async {
      final robot = await openSetup(tester);
      await robot.tap('setup.pickup');
      await robot.type('place.query', 'Mysore');
      expect(find.text('No places match "Mysore"'), findsOneWidget);
    });

    testWidgets('R1.2 per-day times reach R1.4 grouped by equal times', (tester) async {
      final robot = await openFilledSetup(tester);
      await robot.perDayTimes();
      await robot.setDayTime('FRI', '09:30');
      await robot.chooseRideType(RideType.cabSedan);
      expect(find.text('Mon–Thu'), findsOneWidget);
      expect(find.text('Fri'), findsOneWidget);
      expect(find.text('9:30 AM'), findsOneWidget);
      expect(find.text('8:40 AM'), findsOneWidget);
      expect(find.text('6:30 PM'), findsNWidgets(2));
    });
  });

  test('dayKey matches the spec test keys', () {
    expect(dayKey(1), 'MON');
    expect(dayKey(7), 'SUN');
  });
}
