import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';

/// Flow R1 end to end on the demo backend (spec "Testing", Flutter flow):
/// set up → request → Home shows "finding" → the fake clock moves 15 min →
/// Home shows the driver, at three screen sizes; the no-driver path; cancel.
void main() {
  const sizes = {
    'small phone 360x640': Size(360, 640),
    'iPhone 14 390x844': Size(390, 844),
    'Pixel 7 412x915': Size(412, 915),
  };

  for (final entry in sizes.entries) {
    testWidgets('R1 request → finding → driver confirmed on ${entry.key}', (tester) async {
      final api = await pumpRiderApp(tester, size: entry.value);
      final robot = R1Robot(tester);
      await robot.requestR1();
      expect(find.text("Request sent. We'll let you know who your driver is."), findsOneWidget);
      expect(find.text('Finding your driver'), findsOneWidget);
      expect(api.series!.status, SeriesStatus.findingDriver);

      await robot.advance();
      expect(find.byKey(const Key('home.finding')), findsNothing);
      expect(find.text('Driver confirmed'), findsOneWidget);
      expect(find.byKey(const Key('home.slot.MON')), findsOneWidget);
      expect(api.series!.status, SeriesStatus.active);
      expect(tester.takeException(), isNull);
    });
  }

  for (final type in RideType.values) {
    testWidgets('R1 gets a ${type.label} driver', (tester) async {
      final api = await pumpRiderApp(tester);
      await R1Robot(tester).completeR1(type: type);
      final s = api.series!;
      expect(s.rideType, type);
      final driver = s.slots.first.driver!;
      expect(find.text('${driver.name} · ${type.label}'), findsOneWidget);
    });
  }

  testWidgets('R1 per-day times: Friday later than the other days', (tester) async {
    final api = await pumpRiderApp(tester);
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.fillSetup();
    await robot.perDayTimes();
    await robot.setDayTime('FRI', '09:30');
    await robot.chooseRideType(RideType.cabSedan);
    await robot.request();
    final s = api.series!;
    expect(s.slotFor(1, RideDirection.outbound)!.pickupTime, 520);
    expect(s.slotFor(4, RideDirection.outbound)!.pickupTime, 520);
    expect(s.slotFor(5, RideDirection.outbound)!.pickupTime, 570);
    expect(s.slotFor(5, RideDirection.returnLeg)!.pickupTime, 1110);
    await robot.advance();
    expect(find.text('Driver confirmed'), findsOneWidget);
  });

  testWidgets('R1 a Saturday-only ride, one way, cash', (tester) async {
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
    expect(find.textContaining('Home → Embassy Tech Village'), findsOneWidget);
    await robot.request(pay: PaymentMethod.cash);

    final s = api.series!;
    expect(s.weekdays, {6});
    expect(s.returnTrip, isFalse);
    expect(s.usualPayment, PaymentMethod.cash);
    expect(s.startDate, DateTime(2026, 10, 24));
    expect(find.textContaining('24 Oct, once your driver is confirmed.'), findsOneWidget);
  });

  testWidgets('R1 no-driver path: three empty rounds, R1.7, try again, then cancel', (tester) async {
    final api = await pumpRiderApp(tester);
    final robot = R1Robot(tester);
    await robot.requestR1(toggle: {RidePreference.womanDriver});
    await robot.advance(const Duration(minutes: 45));
    expect(find.text('No driver yet'), findsOneWidget);
    expect(api.series!.status, SeriesStatus.noDriverFound);

    await robot.tap('noDriver.retry');
    expect(find.text('Finding your driver'), findsOneWidget);
    await robot.advance(const Duration(minutes: 45));
    expect(find.text('No driver yet'), findsOneWidget);

    await robot.cancelRequest();
    expect(api.series!.status, SeriesStatus.ended);
    expect(find.byKey(const Key('home.setup')), findsOneWidget);
  });

  testWidgets('R1 cancel during a round: no driver is ever assigned', (tester) async {
    final api = await pumpRiderApp(tester);
    final robot = R1Robot(tester);
    await robot.requestR1();
    await robot.advance(const Duration(minutes: 5));
    await robot.cancelRequest();
    await robot.advance();
    expect(api.series!.status, SeriesStatus.ended);
    expect(api.series!.slots.every((s) => s.driver == null), isTrue);
    expect(find.text('Driver confirmed'), findsNothing);
    expect(find.byKey(const Key('home.setup')), findsOneWidget);
  });

  testWidgets('R1 after cancelling, a new series can be set up', (tester) async {
    final api = await pumpRiderApp(tester);
    final robot = R1Robot(tester);
    await robot.requestR1();
    await robot.cancelRequest();
    await robot.fillSetup();
    await robot.chooseRideType(RideType.cabSedan);
    await robot.request();
    expect(api.allSeries, hasLength(2));
    expect(find.byKey(const Key('home.finding')), findsOneWidget);
  });

  testWidgets('R1 survives the slow network used in demo mode', (tester) async {
    final api = await pumpRiderApp(tester, api: FakeRiderApi(latency: const Duration(milliseconds: 500)));
    await R1Robot(tester).completeR1();
    expect(api.series!.status, SeriesStatus.active);
  });
}
