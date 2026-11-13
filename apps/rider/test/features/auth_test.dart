import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';

void main() {
  group('R1.1 Welcome', () {
    testWidgets('shows the promise and the phone field', (tester) async {
      await pumpRiderApp(tester);
      expect(find.text('Your daily commute, sorted'), findsOneWidget);
      expect(find.textContaining('Pay your driver directly'), findsOneWidget);
      expect(find.text('+91'), findsOneWidget);
    });

    testWidgets('Send OTP stays disabled until 10 digits are entered', (tester) async {
      await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      await robot.type('welcome.phone', '98860');
      await robot.tap('welcome.sendOtp');
      expect(find.byKey(const Key('otp.code')), findsNothing);

      await robot.type('welcome.phone', '9886054321');
      await robot.tap('welcome.sendOtp');
      expect(find.byKey(const Key('otp.code')), findsOneWidget);
      expect(find.textContaining('+91 98860 54321'), findsOneWidget);
    });

    testWidgets('letters are ignored and the number is capped at 10 digits', (tester) async {
      await pumpRiderApp(tester);
      await tester.enterText(find.byKey(const Key('welcome.phone')), '98a860-543219999');
      await tester.pump();
      final field = tester.widget<TextField>(find.byKey(const Key('welcome.phone')));
      expect(field.controller!.text, '9886054321');
    });

    testWidgets('a number that is not a mobile says why, without asking the server', (tester) async {
      final api = _CountingSends();
      await pumpRiderApp(tester, api: api);
      final robot = R1Robot(tester);
      await robot.type('welcome.phone', '5123456789');
      await robot.tap('welcome.sendOtp');
      expect(find.text('Enter a 10-digit mobile number starting with 6, 7, 8 or 9'), findsOneWidget);
      expect(find.byKey(const Key('otp.code')), findsNothing);
      expect(api.sends, 0);
    });

    testWidgets('with the keyboard up, the error scrolls into view above Send OTP', (tester) async {
      await pumpRiderApp(tester);
      // A 300-point keyboard on the 844-point screen.
      tester.view.viewInsets = const FakeViewPadding(bottom: 900);
      addTearDown(tester.view.resetViewInsets);
      await tester.pumpAndSettle();
      final robot = R1Robot(tester);
      await robot.type('welcome.phone', '5123456789');
      await tester.tap(find.byKey(const Key('welcome.sendOtp')));
      await tester.pumpAndSettle();
      final error = tester.getRect(find.byKey(const Key('welcome.error')));
      final button = tester.getRect(find.byKey(const Key('welcome.sendOtp')));
      expect(error.bottom, lessThanOrEqualTo(button.top), reason: 'not hidden behind the button');
      expect(error.top, greaterThanOrEqualTo(0));
    });

    testWidgets('the driver sign-up link explains where drivers sign up', (tester) async {
      await pumpRiderApp(tester);
      await R1Robot(tester).tap('welcome.driverSignup');
      expect(find.textContaining('driver app'), findsOneWidget);
    });
  });

  group('R1.1b Enter the code', () {
    Future<R1Robot> openOtp(WidgetTester tester) async {
      await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      await robot.type('welcome.phone', '9886054321');
      await robot.tap('welcome.sendOtp');
      return robot;
    }

    testWidgets('demo mode shows the demo code', (tester) async {
      await openOtp(tester);
      expect(find.text('Demo mode: use ${FakeRiderApi.demoOtp}'), findsOneWidget);
    });

    testWidgets('a wrong code shows an error and clears the field', (tester) async {
      final robot = await openOtp(tester);
      await robot.type('otp.code', '111111');
      expect(find.byKey(const Key('otp.error')), findsOneWidget);
      final field = tester.widget<TextField>(find.byKey(const Key('otp.code')));
      expect(field.controller!.text, isEmpty);
    });

    testWidgets('the right code signs in and lands on Home with no regular ride', (tester) async {
      final robot = await openOtp(tester);
      await robot.type('otp.code', FakeRiderApi.demoOtp);
      expect(find.byKey(const Key('home.empty')), findsOneWidget);
      expect(find.text('No regular rides yet'), findsOneWidget);
      // Back from Home must not return to the sign-in screens.
      expect(find.byKey(const Key('back')), findsNothing);
    });

    testWidgets('back returns to Welcome to fix the number', (tester) async {
      final robot = await openOtp(tester);
      await robot.tap('back');
      expect(find.text('Your daily commute, sorted'), findsOneWidget);
    });
  });
}

/// The demo backend, counting codes asked for.
class _CountingSends extends FakeRiderApi {
  int sends = 0;

  @override
  Future<void> sendOtp(String phone) {
    sends++;
    return super.sendOtp(phone);
  }
}
