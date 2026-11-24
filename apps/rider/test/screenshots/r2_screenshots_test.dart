@Tags(['screenshots'])
library;

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/fake_ride_script.dart';
import 'package:rider/core/models.dart';

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

  testWidgets('R1.5 finding, R1.6 confirmed and R1.7 no driver yet', (tester) async {
    await pumpRiderApp(tester);
    final robot = R1Robot(tester);
    await robot.requestR1();
    tester.binding.scheduleFrame();
    await tester.pump(const Duration(seconds: 5)); // let the toast go
    await shot(tester, 'R1_5_home_finding_driver');
    await robot.advance();
    await shot(tester, 'R1_6_home_driver_confirmed');
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('R1.7 no driver yet', (tester) async {
    await pumpRiderApp(tester);
    final robot = R1Robot(tester);
    await robot.requestR1(toggle: {RidePreference.womanDriver});
    for (var round = 1; round <= FakeRiderApi.maxRounds; round++) {
      await robot.advance();
    }
    await shot(tester, 'R1_7_home_no_driver_yet');
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('R2 normal leg', (tester) async {
    final r = await R2Robot.start(tester);
    await tester.pump(const Duration(seconds: 5));
    await r.playRide(inMinutes: 20);
    await shot(tester, 'R2_home_next_ride_today');
    await r.advanceMinutes(20);
    await r.refreshHome();
    await shot(tester, 'R2_home_driver_on_the_way');
    await r.trackRide();
    await shot(tester, 'R2.1_on_the_way');
    await r.advanceMinutes(6);
    await shot(tester, 'R2.1_arrived_countdown', settle: false);
    await r.advanceMinutes(1);
    await shot(tester, 'R2.2_in_ride');
    await r.advanceMinutes(40);
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
    await r.advanceMinutes(18);
    await r.advanceMinutes(5);
    await shot(tester, 'R2_no_show');
    await r.tap('ride.done');
    await shot(tester, 'R2_home_no_show');
  });
}
