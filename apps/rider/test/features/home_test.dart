import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';

void main() {
  group('R1.6b Home with no regular ride', () {
    testWidgets('greets by time of day and offers to set up a ride', (tester) async {
      await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      await robot.signIn();
      expect(find.text('Good morning'), findsOneWidget);
      expect(find.text('Where to?'), findsOneWidget);
      expect(find.byKey(const Key('home.setup')), findsOneWidget);
    });

    testWidgets('one-time booking and other tabs say which flow brings them', (tester) async {
      await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      await robot.signIn();
      await robot.tap('home.whereTo');
      expect(find.textContaining('flow R6'), findsOneWidget);
      await robot.tap('tab.schedule');
      expect(find.textContaining('flow R3'), findsOneWidget);
      await robot.tap('tab.account');
      expect(find.textContaining('flow R5'), findsOneWidget);
    });
  });

  group('R1.6 Home with a commute', () {
    testWidgets('shows the first ride, the driver and the week', (tester) async {
      await pumpRiderApp(tester);
      await R1Robot(tester).completeR1();

      expect(find.text('Your commute'), findsOneWidget);
      expect(find.text('First ride · Thu 22 Oct'), findsOneWidget);
      expect(find.text('Driver confirmed'), findsOneWidget);
      expect(find.byKey(const Key('home.nextTime')), findsOneWidget);
      expect(tester.widget<Text>(find.byKey(const Key('home.nextTime'))).data, '8:40');
      expect(find.text('AM · Home → Embassy Tech Village'), findsOneWidget);
      expect(find.text('Ravi Kumar · Cab · Sedan'), findsOneWidget);
      expect(find.text('White Dzire · KA 01 AB 1234'), findsOneWidget);
      expect(find.text('This week'), findsOneWidget);
      expect(find.text('Skip a day'), findsOneWidget);
    });

    testWidgets('a confirmation message names the driver and first pickup', (tester) async {
      await pumpRiderApp(tester);
      await R1Robot(tester).completeR1();
      expect(find.text('Commute confirmed. Ravi picks you up Thu 22 Oct at 8:40 AM.'), findsOneWidget);
    });
  });
}
