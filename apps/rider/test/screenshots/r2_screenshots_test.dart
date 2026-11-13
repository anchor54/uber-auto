@Tags(['screenshots'])
library;

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/fake_ride_script.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';
import '../helpers/r2_robot.dart';

/// Renders the D2 and R2 screens and states at phone size into
/// test/screenshots/goldens/, next to R1's, for review against the canvas.
void main() {
  Future<void> shot(WidgetTester tester, String name, {bool settle = true}) async {
    // The arrived countdown ticks every second, so that screen never "settles".
    if (settle) {
      await tester.pumpAndSettle();
    } else {
      await tester.pump();
    }
    await expectLater(find.byType(MaterialApp), matchesGoldenFile('goldens/$name.png'));
  }

  testWidgets('D2 waiting, confirmed and declined', (tester) async {
    final api = await pumpRiderApp(tester, api: FakeRiderApi(driverResponse: DemoDriverResponse.acceptLater, now: () => testNow));
    await R1Robot(tester).completeR1();
    tester.binding.scheduleFrame();
    await tester.pump(const Duration(seconds: 5)); // let the confirmation message go
    await shot(tester, 'D2_home_waiting_for_driver');
    api.driverDeclines();
    await tester.pump(const Duration(seconds: 10));
    await shot(tester, 'D2_home_driver_declined');
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('R2 normal leg', (tester) async {
    final r = await R2Robot.start(tester);
    await tester.pump(const Duration(seconds: 5));
    await r.playRide(inMinutes: 20);
    await shot(tester, 'R2_home_next_ride_today');
    await r.advance(20);
    await r.refreshHome();
    await shot(tester, 'R2_home_driver_on_the_way');
    await r.trackRide();
    await shot(tester, 'R2.1_on_the_way');
    await r.advance(6);
    await shot(tester, 'R2.1_arrived_countdown', settle: false);
    await r.advance(1);
    await shot(tester, 'R2.2_in_ride');
    await r.advance(40);
    await shot(tester, 'R2.3_arrived_pay_and_rate');
    await r.tap('pay.upi');
    await r.tap('rate.star4');
    await r.tap('rate.tag.Clean car');
    await shot(tester, 'R2.3_marked_and_rated');
  });

  testWidgets('R2 late driver and no-show', (tester) async {
    final r = await R2Robot.start(tester);
    await r.playRide(lateMinutes: 12, outcome: FakeRideOutcome.noShow);
    await r.trackRide();
    await shot(tester, 'R2.1_driver_late');
    await r.advance(18);
    await r.advance(5);
    await shot(tester, 'R2_no_show');
    await r.tap('ride.done');
    await shot(tester, 'R2_home_no_show');
  });
}
