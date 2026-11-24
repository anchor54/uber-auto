import 'package:driver/core/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/d1_robot.dart';

/// D1.7 Pay and go live: before "I've paid", after it (waiting), after a
/// rejected claim; then going live (the Requests banner).
/// Clock: Sat 21 Nov 2026, 10:00 → 10 of 30 days due now.
void main() {
  /// Records what the screen copies to the clipboard.
  List<String> mockClipboard(WidgetTester tester) {
    final copied = <String>[];
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') copied.add((call.arguments as Map)['text'] as String);
      if (call.method == 'Clipboard.getData') return {'text': copied.isEmpty ? null : copied.last};
      return null;
    });
    addTearDown(() => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, null));
    return copied;
  }

  group('D1.7 Pay', () {
    testWidgets("D1.7 before I've paid: plan, amount now, UPI ID and reference, the button", (tester) async {
      final h = await pumpD1Screen(tester, () => PayScreen(), setup: d1Approved);
      final robot = D1Robot(tester, h);
      final q = await robot.call(() => h.api.plan());
      expect(q.amountDue, 400);

      expect(find.text('Approved'), findsOneWidget);
      expect(find.text('Last step: pay your first month'), findsOneWidget);
      expect(find.textContaining('Auto plan'), findsWidgets);
      expect(find.textContaining(rupees(q.monthlyPrice)), findsWidgets);
      expect(find.textContaining(RegExp(r'Pay now \(.*21.*30.*\)')), findsOneWidget, reason: 'Pay now (21–30 Nov)');
      expect(find.textContaining(rupees(q.amountDue)), findsWidgets);
      expect(find.textContaining('Then by the 1st of each month'), findsOneWidget);
      await robot.scrollTo(find.text('Pay by UPI or bank transfer'));
      expect(find.text('Pay by UPI or bank transfer'), findsOneWidget);
      expect(find.text(q.payeeUpi), findsOneWidget);
      expect(find.text('Add this reference'), findsOneWidget);
      expect(find.text(q.reference), findsOneWidget);
      expect(find.text('Copy'), findsNWidgets(2));
      await robot.scrollTo(find.textContaining('Bank details are in Account'));
      expect(find.textContaining('Bank details are in Account › Plan.'), findsOneWidget);
      expect(find.textContaining('We confirm payments within'), findsOneWidget);
      expect(robot.label("I've paid"), findsOneWidget);
      expect(find.text('You go live as soon as we confirm it.'), findsOneWidget);
      expect(find.text('Checking your payment'), findsNothing);
      await leaveD1(tester);
    });

    testWidgets('D1.7 Copy puts the UPI ID and the reference on the clipboard', (tester) async {
      final copied = mockClipboard(tester);
      final h = await pumpD1Screen(tester, () => PayScreen(), setup: d1Approved);
      final robot = D1Robot(tester, h);
      final q = await robot.call(() => h.api.plan());
      final copy = await robot.firstOf([robot.key('copy'), find.text('Copy')]);
      await robot.tapFinder(copy.first);
      await robot.tapFinder(copy.last);
      expect(copied, containsAll([q.payeeUpi, q.reference]));
      await leaveD1(tester);
    });

    testWidgets("D1.7 after I've paid: \"Checking your payment\" (disabled) and \"Paid on …\"", (tester) async {
      final h = await pumpD1Screen(tester, () => PayScreen(), setup: d1Approved);
      final robot = D1Robot(tester, h);
      await robot.tap('ive_paid');
      expect(find.text('Checking your payment'), findsOneWidget);
      expect(find.textContaining('Paid on'), findsOneWidget);
      expect(find.textContaining("We'll notify you when it's confirmed."), findsOneWidget);
      expect(robot.label("I've paid"), findsNothing);
      var d = await robot.call(() => h.api.me());
      expect(d.status, DriverStatus.approved);
      expect(d.latestPayment!.status, PaymentStatus.claimed);
      final id = d.latestPayment!.id;

      // Disabled: tapping again claims nothing new.
      await robot.tapFinder(find.text('Checking your payment'));
      d = await robot.call(() => h.api.me());
      expect(d.latestPayment!.id, id);
      expect(find.text('Checking your payment'), findsOneWidget);
      await leaveD1(tester);
    });

    testWidgets('D1.7 opened with a claim in: the waiting state', (tester) async {
      final h = await pumpD1Screen(tester, () => PayScreen(), setup: d1Claimed);
      final robot = D1Robot(tester, h);
      expect(find.text('Checking your payment'), findsOneWidget);
      expect(find.textContaining('Paid on'), findsOneWidget);
      expect(robot.label("I've paid"), findsNothing);
      await leaveD1(tester);
    });

    testWidgets('D1.7 a rejected claim: the orange note with the reason; "I\'ve paid" works again', (tester) async {
      final h = await pumpD1Screen(tester, () => PayScreen(), setup: (api) async {
        await d1Claimed(api);
        api.adminRejectPayment('not received');
      });
      final robot = D1Robot(tester, h);
      await robot.scrollTo(find.textContaining("We couldn't find your payment"));
      expect(find.textContaining("We couldn't find your payment: not received"), findsOneWidget);
      expect(find.text('Checking your payment'), findsNothing);
      await robot.tap('ive_paid');
      expect(find.text('Checking your payment'), findsOneWidget);
      final d = await robot.call(() => h.api.me());
      expect(d.latestPayment!.status, PaymentStatus.claimed);
      await leaveD1(tester);
    });
  });

  group('Going live', () {
    testWidgets("Requests: the green \"You're live\" banner and the empty state", (tester) async {
      final h = await pumpD1App(tester, setup: d1Live);
      expect(find.textContaining("You're live."), findsOneWidget);
      expect(find.textContaining('New requests for auto drivers show up here.'), findsOneWidget);
      expect(find.text("No open requests right now. We'll notify you when a new one comes in."), findsOneWidget);
      expect(find.byKey(const Key('tab.requests')), findsOneWidget);
      final d = await D1Robot(tester, h).call(() => h.api.me());
      expect(d.status, DriverStatus.live);
      expect(d.subscriptionUntil, DateTime(2026, 11, 30));
      await leaveD1(tester);
    });
  });
}
