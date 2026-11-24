import 'package:driver/core/fake_api.dart';
import 'package:driver/core/models.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/d1_robot.dart';

/// D1.5 Verification in each of its four states (submitted, changes needed,
/// approved, rejected): three rows Documents / Vehicle / Approval, the
/// footer, and the actions of each state. The screen reads driver.get when
/// it opens.
void main() {
  Future<D1Robot> open(WidgetTester tester, Future<void> Function(FakeDriverApi api) setup) async {
    final h = await pumpD1Screen(tester, () => VerificationScreen(), setup: setup);
    return D1Robot(tester, h);
  }

  Future<void> licenceRejected(FakeDriverApi api) async {
    await d1Submitted(api);
    api.adminRequestChanges('licence', 'photo blurred');
  }

  const footer = "We'll notify you when this changes.";

  group('D1.5 Verification', () {
    testWidgets('D1.5 submitted: "We\'re checking your details", everything waiting', (tester) async {
      final robot = await open(tester, d1Submitted);
      expect(find.textContaining('Step 5 of 5'), findsOneWidget);
      expect(find.text("We're checking your details"), findsOneWidget);
      expect(find.text('Documents'), findsOneWidget);
      expect(find.text('0 of 7 verified'), findsOneWidget);
      expect(find.text('Vehicle'), findsOneWidget);
      expect(find.text('Approval'), findsOneWidget);
      expect(find.text('Waiting'), findsNWidgets(2), reason: 'vehicle and approval');
      await robot.scrollTo(find.textContaining(footer));
      expect(find.textContaining(footer), findsOneWidget);
      // Removed from the old design.
      expect(find.textContaining('Background check'), findsNothing);
      expect(find.textContaining('nspection'), findsNothing);
      expect(find.textContaining('raining'), findsNothing);
      // No actions while waiting.
      expect(robot.label('Choose your plan'), findsNothing);
      expect(robot.label('Submit again'), findsNothing);
      expect(robot.label('Contact support'), findsNothing);
      await leaveD1(tester);
    });

    testWidgets('D1.5 changes needed: the rejected item, its reason and "Fix now"', (tester) async {
      final robot = await open(tester, licenceRejected);
      expect(find.text('A few things need fixing'), findsOneWidget);
      expect(find.text('6 of 7 verified'), findsOneWidget);
      expect(find.text('Verified'), findsWidgets, reason: 'the vehicle row');
      expect(find.text('Waiting'), findsOneWidget, reason: 'approval');
      await robot.scrollTo(find.textContaining('photo blurred'));
      expect(find.textContaining('Driving licence'), findsWidgets);
      expect(find.textContaining('photo blurred'), findsOneWidget);
      expect(robot.label('Fix now'), findsOneWidget);
      expect(robot.label('Choose your plan'), findsNothing);
      await robot.scrollTo(find.textContaining(footer));
      expect(find.textContaining(footer), findsOneWidget);
      await leaveD1(tester);
    });

    testWidgets('D1.5 changes needed: "Submit again" does nothing until the item is replaced', (tester) async {
      final robot = await open(tester, licenceRejected);
      if (robot.shows(robot.label('Submit again'))) {
        await robot.tap('submit_again');
      }
      expect((await robot.call(() => robot.api.me())).status, DriverStatus.changesNeeded);
      expect(find.text('A few things need fixing'), findsOneWidget);
      await leaveD1(tester);
    });

    testWidgets('D1.5 "Fix now" opens the documents screen at the rejected item', (tester) async {
      final robot = await open(tester, licenceRejected);
      await robot.tap('fix_now');
      expect(find.text('Your documents'), findsWidgets);
      await robot.scrollTo(robot.row('licence'));
      expect(robot.inRow('licence', find.text('Fix')), findsOneWidget);
      await leaveD1(tester);
    });

    testWidgets('D1.5 fixed via "Fix now": back on D1.5, "Submit again" → submitted', (tester) async {
      final robot = await open(tester, licenceRejected);
      await robot.tap('fix_now');
      await robot.uploadDoc('licence');
      await robot.tap('back');
      await robot.tap('submit_again');
      expect(find.text("We're checking your details"), findsOneWidget);
      final d = await robot.call(() => robot.api.me());
      expect(d.status, DriverStatus.submitted);
      expect(d.items.singleWhere((i) => i.kind == 'rc').state, ItemState.verified, reason: 'verified items stay verified');
      await leaveD1(tester);
    });

    testWidgets('D1.5 replaced before opening: "Submit again" submits', (tester) async {
      final robot = await open(tester, (api) async {
        await licenceRejected(api);
        await api.uploadDocument('licence', jpegBytes(), 'image/jpeg');
      });
      await robot.tap('submit_again');
      expect(find.text("We're checking your details"), findsOneWidget);
      expect((await robot.call(() => robot.api.me())).status, DriverStatus.submitted);
      await leaveD1(tester);
    });

    testWidgets('D1.5 a rejected vehicle: the Vehicle row says "Fix"; "Fix now" opens D1.3', (tester) async {
      final robot = await open(tester, (api) async {
        await d1Submitted(api);
        api.adminRequestChanges('vehicle', 'plate not readable');
      });
      expect(find.text('A few things need fixing'), findsOneWidget);
      expect(find.text('7 of 7 verified'), findsOneWidget);
      expect(find.text('Fix'), findsWidgets);
      await robot.scrollTo(find.textContaining('plate not readable'));
      expect(find.textContaining('plate not readable'), findsOneWidget);
      await robot.tap('fix_now');
      expect(find.text('Your vehicle'), findsWidgets);
      await leaveD1(tester);
    });

    testWidgets('D1.5 approved: "You\'re approved", all verified, approval done, "Choose your plan"', (tester) async {
      final robot = await open(tester, d1Approved);
      expect(find.text("You're approved"), findsOneWidget);
      expect(find.text('7 of 7 verified'), findsOneWidget);
      expect(find.text('Done'), findsOneWidget);
      expect(find.text('Waiting'), findsNothing);
      await robot.tap('choose_your_plan');
      expect(find.text('Driver subscription'), findsOneWidget);
      await leaveD1(tester);
    });

    testWidgets("D1.5 rejected: \"We can't approve you\", the reason and Contact support only", (tester) async {
      final robot = await open(tester, (api) async {
        await d1Submitted(api);
        api.adminReject('Licence not valid for commercial use');
      });
      expect(find.text("We can't approve you"), findsOneWidget);
      await robot.scrollTo(find.textContaining('Licence not valid for commercial use'));
      expect(find.textContaining('Licence not valid for commercial use'), findsOneWidget);
      expect(robot.label('Contact support'), findsOneWidget);
      expect(robot.label('Choose your plan'), findsNothing);
      expect(robot.label('Submit again'), findsNothing);
      expect(robot.label('Fix now'), findsNothing);
      await leaveD1(tester);
    });

    testWidgets('D1.5 reads driver.get each time it opens', (tester) async {
      final robot = await open(tester, d1Submitted);
      expect(find.text("We're checking your details"), findsOneWidget);
      robot.api.adminApprove();
      await robot.reopenScreen(() => VerificationScreen());
      expect(find.text("You're approved"), findsOneWidget);
      await leaveD1(tester);
    });
  });
}
