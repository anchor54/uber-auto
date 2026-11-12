import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';

String textOf(WidgetTester tester, String key) => tester.widget<Text>(find.byKey(Key(key))).data!;

void main() {
  Future<R1Robot> openSetup(WidgetTester tester) async {
    await pumpRiderApp(tester);
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.tap('home.setup');
    return robot;
  }

  group('R1.2 Set up a commute', () {
    testWidgets('starts with weekday defaults and no route', (tester) async {
      await openSetup(tester);
      expect(find.text('New commute'), findsOneWidget);
      expect(find.text('Step 1 of 3'), findsOneWidget);
      expect(textOf(tester, 'setup.time'), '8:40 AM');
      expect(textOf(tester, 'setup.window'), 'Driver may arrive 8:35–8:45');
      expect(textOf(tester, 'setup.starts'), 'Thu, 22 Oct');
      expect(textOf(tester, 'setup.ridesPerMonth'), 'About 44 (22 days × 2)');
      expect(textOf(tester, 'setup.fareFrom'), '–');
      expect(isEnabled(tester, 'setup.continue'), isFalse);
    });

    testWidgets('choosing both places quotes the lowest fare and enables Continue', (tester) async {
      final robot = await openSetup(tester);
      await robot.choosePlace('setup.pickup', 'HSR', 'home');
      expect(find.text('Home · 14th Main, HSR Layout'), findsOneWidget);
      expect(find.byKey(const Key('setup.blocker')), findsOneWidget);
      expect(find.text('Choose a drop-off'), findsOneWidget);

      await robot.choosePlace('setup.drop', 'Embassy', 'etv');
      expect(find.text('Embassy Tech Village, Gate 3'), findsOneWidget);
      expect(textOf(tester, 'setup.fareFrom'), 'From ₹90');
      expect(find.text('6:30 PM from Embassy Tech Village · Change'), findsOneWidget);
      expect(isEnabled(tester, 'setup.continue'), isTrue);
    });

    testWidgets('the same place for pickup and drop-off is not allowed', (tester) async {
      final robot = await openSetup(tester);
      await robot.choosePlace('setup.pickup', 'HSR', 'home');
      await robot.choosePlace('setup.drop', 'HSR', 'home');
      expect(find.text("Pickup and drop-off can't be the same place"), findsNWidgets(2));
      expect(isEnabled(tester, 'setup.continue'), isFalse);
    });

    testWidgets('place search shows a message when nothing matches', (tester) async {
      final robot = await openSetup(tester);
      await robot.tap('setup.pickup');
      await robot.type('place.query', 'Mysore');
      expect(find.text('No places match "Mysore"'), findsOneWidget);
    });

    testWidgets('tapping Saturday switches to chosen days and adds it', (tester) async {
      final robot = await openSetup(tester);
      await robot.tap('setup.day.6');
      expect(textOf(tester, 'setup.ridesPerMonth'), 'About 52 (26 days × 2)');
      expect(textOf(tester, 'setup.starts'), 'Thu, 22 Oct');
    });

    testWidgets('a Saturday-only ride starts this Saturday', (tester) async {
      final robot = await openSetup(tester);
      await robot.tap('segment.Chosen days each week');
      for (final d in [1, 2, 3, 4, 5]) {
        await robot.tap('setup.day.$d');
      }
      await robot.tap('setup.day.6');
      expect(textOf(tester, 'setup.starts'), 'Sat, 24 Oct');
      expect(textOf(tester, 'setup.ridesPerMonth'), 'About 8 (4 days × 2)');
    });

    testWidgets('removing every day blocks Continue', (tester) async {
      final robot = await openSetup(tester);
      await robot.choosePlace('setup.pickup', 'HSR', 'home');
      await robot.choosePlace('setup.drop', 'Embassy', 'etv');
      for (final d in [1, 2, 3, 4, 5]) {
        await robot.tap('setup.day.$d');
      }
      expect(find.text('Pick at least one day'), findsNWidgets(2));
      expect(isEnabled(tester, 'setup.continue'), isFalse);
    });

    testWidgets('get-me-there-by works out the pickup from the trip time', (tester) async {
      final robot = await openSetup(tester);
      await robot.choosePlace('setup.pickup', 'HSR', 'home');
      await robot.choosePlace('setup.drop', 'Embassy', 'etv');
      await robot.tap('segment.Get me there by');
      expect(textOf(tester, 'setup.time'), '9:30 AM');
      expect(textOf(tester, 'setup.window'), 'Pickup around 8:40 AM (about 38 min trip)');
    });

    testWidgets('turning off the return trip halves the rides', (tester) async {
      final robot = await openSetup(tester);
      await robot.tap('setup.return');
      expect(textOf(tester, 'setup.ridesPerMonth'), 'About 22');
      expect(find.text('One way only'), findsOneWidget);
    });
  });
}
