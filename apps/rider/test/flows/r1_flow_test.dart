import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';

/// Flow R1 end to end, at several phone sizes and with each ride type.
void main() {
  const sizes = {
    'small phone 360x640': Size(360, 640),
    'iPhone 14 390x844': Size(390, 844),
    'Pixel 7 412x915': Size(412, 915),
  };

  for (final entry in sizes.entries) {
    testWidgets('R1 happy path on ${entry.key}', (tester) async {
      final api = await pumpRiderApp(tester, size: entry.value);
      await R1Robot(tester).completeR1();
      expect(api.commute, isNotNull);
      expect(tester.takeException(), isNull);
    });
  }

  for (final type in RideType.values) {
    testWidgets('R1 books a ${type.label} commute', (tester) async {
      final api = await pumpRiderApp(tester);
      await R1Robot(tester).completeR1(type: type);
      expect(api.commute!.request.rideType, type);
      expect(find.text('${api.commute!.driver.name} · ${type.label}'), findsOneWidget);
    });
  }

  testWidgets('R1 with a Saturday-only ride, one way, cash', (tester) async {
    final api = await pumpRiderApp(tester);
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.fillSetup();
    await robot.tap('segment.Chosen days each week');
    for (final d in [1, 2, 3, 4, 5, 6]) {
      await robot.tap('setup.day.$d');
    }
    await robot.tap('setup.return');
    await robot.chooseRideType(RideType.auto);
    await robot.confirm(pay: PaymentMethod.cash);

    final c = api.commute!;
    expect(c.request.weekdays, {6});
    expect(c.request.returnTrip, isFalse);
    expect(c.request.flow, BookingFlow.recurringWeekly);
    expect(c.request.startDate, DateTime(2026, 10, 24));
    expect(find.text('First ride · Sat 24 Oct'), findsOneWidget);
  });

  testWidgets('R1 survives the slow network used in demo mode', (tester) async {
    final api = await pumpRiderApp(tester, api: FakeRiderApi(latency: const Duration(milliseconds: 500)));
    await R1Robot(tester).completeR1();
    expect(api.commute, isNotNull);
  });
}
