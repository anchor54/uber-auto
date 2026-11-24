import 'package:driver/core/fake_api.dart';
import 'package:driver/core/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/d1_robot.dart';

/// Flow D1 end to end on the demo backend, the admin played by the fake's
/// hooks: sign up → 7 documents → vehicle → day off → submit → approved →
/// plan → I've paid → confirmed → Requests shows "You're live". Plus the
/// changes-needed and rejected branches. Clock: Sat 21 Nov 2026, 10:00.
void main() {
  const sizes = {
    'small phone 360x640': Size(360, 640),
    'iPhone 14 390x844': Size(390, 844),
  };

  for (final entry in sizes.entries) {
    testWidgets('D1 sign up → documents → vehicle → day off → approved → paid → live on ${entry.key}', (tester) async {
      final h = await pumpD1App(tester, size: entry.value);
      final robot = D1Robot(tester, h);
      await d1HappyPath(
        robot,
        approve: () async => robot.api.adminApprove(),
        confirmPayment: () async => robot.api.adminConfirmPayment(),
      );
      expect(find.text("No open requests right now. We'll notify you when a new one comes in."), findsOneWidget);
      await leaveD1(tester);
    });
  }

  testWidgets('D1 survives the slow network used in demo mode', (tester) async {
    final h = await pumpD1App(tester, backend: (now) => FakeDriverApi(now: now, seed: false, latency: const Duration(milliseconds: 300)));
    final robot = D1Robot(tester, h);
    await d1HappyPath(
      robot,
      approve: () async => robot.api.adminApprove(),
      confirmPayment: () async => robot.api.adminConfirmPayment(),
    );
    await leaveD1(tester);
  });

  /// Sign-up through submit, through the app.
  Future<D1Robot> submitted(WidgetTester tester) async {
    final h = await pumpD1App(tester);
    final robot = D1Robot(tester, h);
    await robot.signUp();
    await robot.addAllDocuments();
    await robot.tap('continue_to_vehicle');
    await robot.fillVehicle();
    await robot.tap('continue');
    await robot.pickDayOff(7);
    await robot.tap('submit_for_review');
    expect(find.text("We're checking your details"), findsOneWidget);
    return robot;
  }

  testWidgets('D1 changes needed: the licence is rejected, replaced, submitted again, then approved', (tester) async {
    final robot = await submitted(tester);

    robot.api.adminRequestChanges('licence', 'photo blurred');
    await robot.reopenApp();
    expect(find.text('A few things need fixing'), findsOneWidget);
    await robot.scrollTo(find.textContaining('photo blurred'));
    expect(find.textContaining('photo blurred'), findsOneWidget);

    await robot.tap('fix_now');
    expect(find.text('Your documents'), findsWidgets);
    await robot.scrollTo(robot.row('licence'));
    expect(robot.inRow('licence', find.text('Fix')), findsOneWidget);
    expect(robot.inRow('rc', find.text('Verified')), findsOneWidget);
    await robot.uploadDoc('licence');
    await robot.scrollTo(robot.row('licence'));
    expect(robot.inRow('licence', find.text('Uploaded')), findsOneWidget);
    await robot.tap('back');

    await robot.tap('submit_again');
    expect(find.text("We're checking your details"), findsOneWidget);
    var d = await robot.call(() => robot.api.me());
    expect(d.status, DriverStatus.submitted);
    expect(d.items.singleWhere((i) => i.kind == 'rc').state, ItemState.verified);
    expect(d.items.singleWhere((i) => i.kind == 'licence').state, ItemState.uploaded);

    robot.api.adminApprove();
    await robot.reopenApp();
    expect(find.text("You're approved"), findsOneWidget);
    await robot.tap('choose_your_plan');
    expect(find.text('Driver subscription'), findsOneWidget);
    d = await robot.call(() => robot.api.me());
    expect(d.status, DriverStatus.approved);
    await leaveD1(tester);
  });

  testWidgets('D1 rejected: the reason, Contact support, no plan', (tester) async {
    final robot = await submitted(tester);
    robot.api.adminReject('Licence not valid for commercial use');
    await robot.reopenApp();
    expect(find.text("We can't approve you"), findsOneWidget);
    await robot.scrollTo(find.textContaining('Licence not valid for commercial use'));
    expect(find.textContaining('Licence not valid for commercial use'), findsOneWidget);
    expect(robot.label('Contact support'), findsOneWidget);
    expect(robot.label('Choose your plan'), findsNothing);
    expect(robot.label('Submit again'), findsNothing);
    await expectLater(
      robot.call(() => robot.api.plan()),
      throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_approved')),
    );
    await leaveD1(tester);
  });

  testWidgets('D1 a payment the admin could not find: claim again, then live', (tester) async {
    final h = await pumpD1App(tester, setup: d1Approved);
    final robot = D1Robot(tester, h);
    expect(find.text("You're approved"), findsOneWidget);
    await robot.tap('choose_your_plan');
    await robot.tap('continue_to_payment');
    await robot.tap('ive_paid');
    expect(find.text('Checking your payment'), findsOneWidget);

    robot.api.adminRejectPayment('not received');
    await robot.reopenApp();
    // Approved with no open claim: back on D1.5, on to D1.7 again.
    if (robot.shows(robot.label('Choose your plan'))) {
      await robot.tap('choose_your_plan');
      await robot.tap('continue_to_payment');
    }
    await robot.scrollTo(find.textContaining("We couldn't find your payment"));
    expect(find.textContaining("We couldn't find your payment: not received"), findsOneWidget);
    await robot.tap('ive_paid');
    expect(find.text('Checking your payment'), findsOneWidget);

    robot.api.adminConfirmPayment();
    await robot.reopenApp();
    expect(find.textContaining("You're live."), findsOneWidget);
    await leaveD1(tester);
  });
}
