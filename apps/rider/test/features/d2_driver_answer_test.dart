import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';

/// Flow D2 from the rider's side: after confirming, Home waits for the
/// driver, then shows they accepted, or offers another driver.
void main() {
  Future<(FakeRiderApi, R1Robot)> confirmAndWait(WidgetTester tester) async {
    final api = await pumpRiderApp(
      tester,
      api: FakeRiderApi(driverResponse: DemoDriverResponse.acceptLater, now: () => testNow),
    );
    final robot = R1Robot(tester);
    await robot.completeR1();
    return (api, robot);
  }

  /// Home checks back every 10 s while waiting; this stops that timer.
  Future<void> leave(WidgetTester tester) => tester.pumpWidget(const SizedBox());

  testWidgets('Home says it is waiting for the driver, with the deadline', (tester) async {
    await confirmAndWait(tester);
    expect(find.text('Waiting for Ravi'), findsOneWidget);
    expect(
      find.text("We've asked Ravi to confirm by 9:00 PM. If they can't, we'll help you pick another driver."),
      findsOneWidget,
    );
    // Nothing to track or skip until the driver has accepted.
    expect(find.byKey(const Key('home.track')), findsNothing);
    expect(find.byKey(const Key('home.skip')), findsNothing);
    await leave(tester);
  });

  testWidgets('when the driver accepts, Home shows it on its next check', (tester) async {
    final (api, _) = await confirmAndWait(tester);
    api.driverAccepts();
    await tester.pump(const Duration(seconds: 10));
    await tester.pumpAndSettle();
    expect(find.text('Driver confirmed'), findsOneWidget);
    expect(find.byKey(const Key('home.skip')), findsOneWidget);
    expect(api.commute!.driver.phone, isNotNull);
  });

  testWidgets('pulling down refreshes straight away', (tester) async {
    final (api, _) = await confirmAndWait(tester);
    api.driverAccepts();
    await tester.fling(find.byKey(const Key('home.commute')), const Offset(0, 400), 1000);
    await tester.pumpAndSettle();
    expect(find.text('Driver confirmed'), findsOneWidget);
  });

  testWidgets('a decline offers another driver for the same route, without the one who declined', (tester) async {
    final (api, robot) = await confirmAndWait(tester);
    api.driverDeclines();
    await tester.pump(const Duration(seconds: 10));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('home.declined')), findsOneWidget);
    expect(find.text("Ravi can't take this commute"), findsOneWidget);

    await robot.tap('home.rematch');
    expect(find.byKey(const Key('match.found')), findsOneWidget);
    expect(find.text('Suresh M'), findsOneWidget);
    await robot.tap('match.others');
    expect(find.byKey(const Key('others.d_ravi')), findsNothing);
    await tester.tapAt(const Offset(10, 10)); // close the sheet
    await tester.pumpAndSettle();

    await robot.confirm();
    expect(find.text('Waiting for Suresh'), findsOneWidget);
    final c = api.commute!;
    expect(c.driver.name, 'Suresh M');
    expect(c.request.pickup.id, 'home');
    expect(c.request.drop.id, 'etv');
    await leave(tester);
  });

  testWidgets('no answer in time says so and offers another driver', (tester) async {
    final (api, _) = await confirmAndWait(tester);
    api.driverNeverAnswers();
    await tester.pump(const Duration(seconds: 10));
    await tester.pumpAndSettle();
    expect(find.text("Ravi didn't reply in time"), findsOneWidget);
    expect(find.byKey(const Key('home.rematch')), findsOneWidget);
  });

  testWidgets('the demo driver used in demo mode answers after its delay', (tester) async {
    var now = DateTime(2026, 10, 21, 9);
    final api = FakeRiderApi(driverResponse: DemoDriverResponse.acceptLater, now: () => now);
    await pumpRiderApp(tester, api: api);
    await R1Robot(tester).completeR1();
    expect(find.text('Waiting for Ravi'), findsOneWidget);
    now = now.add(const Duration(seconds: 8));
    await tester.pump(const Duration(seconds: 10));
    await tester.pumpAndSettle();
    expect(find.text('Driver confirmed'), findsOneWidget);
    expect(api.commute!.status, CommuteStatus.active);
  });
}
