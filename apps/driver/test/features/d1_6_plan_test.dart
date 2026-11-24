import 'package:flutter_test/flutter_test.dart';

import '../helpers/d1_robot.dart';

/// D1.6 Your plan: one plan for the driver's vehicle type, its monthly
/// price, the payment rule; "Continue to payment" opens D1.7.
void main() {
  group('D1.6 Plan', () {
    testWidgets('D1.6 copy: title, the auto plan card, the note', (tester) async {
      final h = await pumpD1Screen(tester, () => PlanScreen(), setup: d1Approved);
      final robot = D1Robot(tester, h);
      final q = await robot.call(() => h.api.plan());
      expect(find.text('Driver subscription'), findsOneWidget);
      expect(find.text('One flat monthly fee. Keep every fare.'), findsOneWidget);
      expect(find.text('Auto plan'), findsOneWidget);
      expect(find.textContaining(rupees(q.monthlyPrice)), findsWidgets);
      expect(find.textContaining('/mo'), findsWidgets);
      expect(find.textContaining('No commission on any ride'), findsOneWidget);
      expect(find.textContaining('All regular and one-time requests for auto drivers'), findsOneWidget);
      await robot.scrollTo(find.textContaining('Pay by the 1st of each month by UPI or bank transfer.'));
      expect(
        find.textContaining(
            "Pay by the 1st of each month by UPI or bank transfer. If it isn't received by the 4th, your account pauses until you pay."),
        findsOneWidget,
      );
      // One plan per vehicle type: no tiers, no removed perks.
      expect(find.textContaining('Starter'), findsNothing);
      expect(find.textContaining('Fleet'), findsNothing);
      expect(find.textContaining('First look at requests'), findsNothing);
      expect(find.textContaining('Backup-driver cover'), findsNothing);
      expect(robot.label('Continue to payment'), findsOneWidget);
      await leaveD1(tester);
    });

    testWidgets('D1.6 "Continue to payment" opens D1.7', (tester) async {
      final h = await pumpD1Screen(tester, () => PlanScreen(), setup: d1Approved);
      final robot = D1Robot(tester, h);
      await robot.tap('continue_to_payment');
      expect(find.text('Last step: pay your first month'), findsOneWidget);
      await leaveD1(tester);
    });
  });
}
