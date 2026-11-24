import 'package:driver/core/fake_api.dart';
import 'package:driver/core/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/d1_robot.dart';

/// D1.1 Sign up (spec "Screen designs and design changes").
/// Clock: Sat 21 Nov 2026, 10:00.
void main() {
  Future<D1Robot> open(WidgetTester tester) async {
    final h = await pumpD1Screen(tester, () => SignUpScreen());
    return D1Robot(tester, h);
  }

  group('D1.1 Sign up', () {
    testWidgets('D1.1 copy: step, title, benefits, fields, terms, buttons', (tester) async {
      final robot = await open(tester);
      expect(find.textContaining('Drive with us'), findsOneWidget);
      expect(find.textContaining('Step 1 of 5'), findsOneWidget);
      expect(find.text('Drive the same riders, every day'), findsOneWidget);
      for (final b in ['A fixed weekly schedule you agree to', 'Keep every fare, pay one monthly fee', 'Riders pay you directly, UPI or cash']) {
        expect(find.text(b), findsOneWidget, reason: b);
      }
      expect(find.text('Full name as on your licence'), findsWidgets);
      expect(find.text('Mobile number'), findsWidgets);
      expect(find.textContaining('+91'), findsWidgets);
      expect(find.text("City you'll drive in"), findsWidgets);
      expect(find.text('Bengaluru'), findsOneWidget, reason: 'cities from config: Bengaluru only');
      await robot.scrollTo(find.textContaining('I agree to the driver terms and to a background check.'));
      expect(find.textContaining('I agree to the driver terms and to a background check.'), findsOneWidget);
      expect(robot.label('Get code'), findsOneWidget);
      await robot.scrollTo(find.textContaining('Already registered?'));
      expect(find.textContaining('Already registered?'), findsOneWidget);
      // Design changes: no fleet owners, "Send OTP" is now "Get code".
      expect(find.textContaining('Fleet owner'), findsNothing);
      expect(find.text('Send OTP'), findsNothing);
      expect(find.text('Verify'), findsNothing, reason: 'the code field comes after Get code');
      await leaveD1(tester);
    });

    testWidgets('D1.1 Get code needs a 2+ character name, 10 digits and the tick', (tester) async {
      final robot = await open(tester);
      final verify = robot.key('verify');

      // One-letter name.
      await robot.fillSignUp(name: 'R');
      await robot.tap('get_code');
      expect(verify, findsNothing);

      // Nine digits.
      await robot.typeField('Full name as on your licence', 'Ra');
      await robot.typeField('Mobile number', '980000020');
      await robot.tap('get_code');
      expect(verify, findsNothing);

      // All set: the code field and Verify appear.
      await robot.typeField('Mobile number', signUpPhone);
      await robot.tap('get_code');
      expect(verify, findsOneWidget);
      await leaveD1(tester);
    });

    testWidgets('D1.1 without the tick, Get code does nothing', (tester) async {
      final robot = await open(tester);
      await robot.fillSignUp(terms: false);
      await robot.tap('get_code');
      expect(robot.key('verify'), findsNothing);
      await leaveD1(tester);
    });

    testWidgets("D1.1 a wrong code: \"That code didn't match\" under the field", (tester) async {
      final robot = await open(tester);
      await robot.fillSignUp();
      await robot.tap('get_code');
      await robot.enterCode('000000');
      expect(find.text("That code didn't match"), findsOneWidget);
      expect(find.byType(DocumentsScreen), findsNothing);
      await leaveD1(tester);
    });

    testWidgets('D1.1 the right code registers the driver and opens D1.2', (tester) async {
      final robot = await open(tester);
      await robot.fillSignUp();
      await robot.tap('get_code');
      await robot.enterCode(FakeDriverApi.demoOtp);
      expect(find.text('Your documents'), findsWidgets);
      expect(find.textContaining('Step 2 of 5'), findsOneWidget);
      final d = await robot.call(() => robot.api.me());
      expect(d.status, DriverStatus.signingUp);
      expect(d.name, signUpName);
      expect(d.reference, matches(RegExp(r'^DRV-\d{5}$')));
      await leaveD1(tester);
    });

    testWidgets('D1.1 "Already registered? Log in" goes to sign-in', (tester) async {
      final h = await pumpD1App(tester);
      final robot = D1Robot(tester, h);
      await robot.openSignUp();
      await robot.tapFinder(await robot.firstOf([robot.label('Already registered? Log in'), find.textContaining('Log in')]));
      expect(find.byKey(const Key('welcome.phone')), findsOneWidget);
      await leaveD1(tester);
    });
  });
}
