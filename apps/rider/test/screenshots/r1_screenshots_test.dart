@Tags(['screenshots'])
library;

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/models.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';

/// Renders every R1 screen and Home state at phone size into
/// test/screenshots/goldens/. CI runs this with --update-goldens and uploads
/// the PNGs for review; they are not committed and never fail the build on
/// their own.
void main() {
  Future<void> shot(WidgetTester tester, String name) async {
    await tester.pumpAndSettle();
    await expectLater(find.byType(MaterialApp), matchesGoldenFile('goldens/$name.png'));
  }

  testWidgets('R1 screens', (tester) async {
    await pumpRiderApp(tester);
    final robot = R1Robot(tester);
    await shot(tester, 'R1.1_welcome');

    await robot.type('welcome.phone', '9886054321');
    await robot.tap('welcome.sendOtp');
    tester.testTextInput.hide();
    await shot(tester, 'R1.1b_otp');
    await robot.type('otp.code', '123456');
    await shot(tester, 'R1.6b_home_empty');

    await robot.tap('home.setup');
    await shot(tester, 'R1.2_setup_empty');
    await robot.choosePlace('setup.pickup', 'HSR', 'home');
    await robot.choosePlace('setup.drop', 'Embassy', 'etv');
    await shot(tester, 'R1.2_setup_filled');
    await robot.perDayTimes();
    await robot.setDayTime('FRI', '09:30');
    await shot(tester, 'R1.2_setup_per_day');

    await robot.tap('setup.continue');
    await shot(tester, 'R1.3_ride_type');
    await robot.tap('rideType.continue');
    await shot(tester, 'R1.4_review');

    await robot.tap('review.request');
    await shot(tester, 'R1.5_home_finding_with_toast');
    await tester.pump(const Duration(seconds: 5));
    await shot(tester, 'R1.5_home_finding');
    await robot.tap('home.cancelRequest');
    await shot(tester, 'R1.5_cancel_dialog');
    await robot.tap('cancelRequest.keep');

    await robot.advance();
    await shot(tester, 'R1.6_home_driver_confirmed');
  });

  testWidgets('R1.7 no driver yet', (tester) async {
    await pumpRiderApp(tester);
    final robot = R1Robot(tester);
    await robot.requestR1(toggle: {RidePreference.womanDriver});
    await robot.advance(const Duration(minutes: 45));
    await shot(tester, 'R1.7_home_no_driver');
    await robot.tap('noDriver.change');
    await robot.tap('setup.continue');
    await robot.tap('rideType.continue');
    await shot(tester, 'R1.7_review_try_again');
  });

  testWidgets('R1.4 on a small phone', (tester) async {
    await pumpRiderApp(tester, size: const Size(360, 640));
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.fillSetup();
    await robot.chooseRideType(RideType.cabSedan);
    await shot(tester, 'small_R1.4_review');
  });
}
