@Tags(['screenshots'])
library;

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/models.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';

/// Renders every R1 screen at phone size into test/screenshots/goldens/.
/// CI runs this with --update-goldens and uploads the PNGs for review;
/// they are not committed and never fail the build on their own.
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
    await shot(tester, 'R1.1_welcome_filled');
    await robot.tap('welcome.sendOtp');
    tester.testTextInput.hide();
    await shot(tester, 'R1.1b_otp');

    await robot.type('otp.code', '111111');
    await shot(tester, 'R1.1b_otp_wrong_code');
    await robot.type('otp.code', '123456');
    await shot(tester, 'R1.6b_home_empty');

    await robot.tap('home.setup');
    await shot(tester, 'R1.2_setup_empty');
    await robot.tap('setup.pickup');
    await robot.type('place.query', 'e');
    await shot(tester, 'R1.2_place_search');
    await robot.tap('place.home');
    await robot.choosePlace('setup.drop', 'Embassy', 'etv');
    await shot(tester, 'R1.2_setup_filled');
    await tester.drag(find.byKey(const Key('setup.list')), const Offset(0, -600));
    await shot(tester, 'R1.2_setup_filled_scrolled');

    await robot.tap('setup.continue');
    await shot(tester, 'R1.3_ride_type');
    await robot.tap('pref.womanDriver');
    await robot.tap('rideType.continue');
    await shot(tester, 'R1.4_no_driver');
    await robot.tap('match.relax');
    await shot(tester, 'R1.4_match');
    await robot.tap('match.others');
    await shot(tester, 'R1.4_other_drivers');
    await robot.tap('others.d_ravi');

    await robot.tap('match.continue');
    await shot(tester, 'R1.5_confirm');
    await robot.tap('confirm.submit');
    await shot(tester, 'R1.6_home_with_snackbar');
    await tester.pump(const Duration(seconds: 5));
    await shot(tester, 'R1.6_home');
  });

  testWidgets('R1.3 ride types on a small phone', (tester) async {
    await pumpRiderApp(tester, size: const Size(360, 640));
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.fillSetup();
    await robot.chooseRideType(RideType.cabSedan);
    await shot(tester, 'small_R1.4_match');
  });
}
