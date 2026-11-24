import 'package:driver/core/fake_api.dart';
import 'package:driver/core/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/d1_robot.dart';

/// D1.4 Your day off (replaces "Where and when you drive"): one choice of
/// seven day chips or "No fixed day off", none pre-selected; "Submit for
/// review" submits, or lists the missing items.
void main() {
  /// Everything but the day off.
  Future<void> ready(FakeDriverApi api) async {
    await d1SignedUp(api);
    await d1AddDocuments(api);
    await d1AddVehicle(api);
  }

  Future<D1Robot> open(WidgetTester tester, {Future<void> Function(FakeDriverApi api)? setup}) async {
    final h = await pumpD1Screen(tester, () => DayOffScreen(), setup: setup ?? ready);
    return D1Robot(tester, h);
  }

  int selectedChips(WidgetTester tester) =>
      tester.widgetList<ChoiceChip>(find.byType(ChoiceChip)).where((c) => c.selected).length;

  group('D1.4 Day off', () {
    testWidgets('D1.4 copy: step, title, line, seven chips and "No fixed day off", note, button', (tester) async {
      final robot = await open(tester);
      expect(find.textContaining('Step 4 of 5'), findsOneWidget);
      expect(find.text('Your day off'), findsWidgets);
      expect(find.text("Pick the day you never drive. You won't be offered regular rides on that day."), findsOneWidget);
      expect(find.text('M'), findsOneWidget);
      expect(find.text('T'), findsNWidgets(2));
      expect(find.text('W'), findsOneWidget);
      expect(find.text('F'), findsOneWidget);
      expect(find.text('S'), findsNWidgets(2));
      expect(find.text('No fixed day off'), findsOneWidget);
      expect(find.textContaining("You'll see every request for auto drivers in Bengaluru, and choose the ones you want."), findsOneWidget);
      expect(find.textContaining('For a holiday, use Take time off later.'), findsOneWidget);
      expect(robot.label('Submit for review'), findsOneWidget);
      // The old screen is gone.
      expect(find.textContaining('Where and when you drive'), findsNothing);
      expect(find.textContaining('max distance'), findsNothing);
      await leaveD1(tester);
    });

    testWidgets('D1.4 nothing is pre-selected and a choice is required', (tester) async {
      final robot = await open(tester);
      expect(selectedChips(tester), 0);
      await robot.tap('submit_for_review');
      expect(find.text('Your day off'), findsWidgets);
      final d = await robot.call(() => robot.api.me());
      expect(d.status, DriverStatus.signingUp);
      expect(d.dayOffSet, isFalse);
      await leaveD1(tester);
    });

    testWidgets('D1.4 single choice: the last tapped day wins; submit → D1.5', (tester) async {
      final robot = await open(tester);
      await robot.pickDayOff(7);
      await robot.pickDayOff(3);
      if (find.byType(ChoiceChip).evaluate().isNotEmpty) expect(selectedChips(tester), 1);
      await robot.tap('submit_for_review');
      final d = await robot.call(() => robot.api.me());
      expect(d.dayOff, 3);
      expect(d.status, DriverStatus.submitted);
      expect(find.text("We're checking your details"), findsOneWidget);
      await leaveD1(tester);
    });

    testWidgets('D1.4 "No fixed day off" is a choice', (tester) async {
      final robot = await open(tester);
      await robot.pickDayOff(7);
      await robot.pickDayOff(null);
      if (find.byType(ChoiceChip).evaluate().isNotEmpty) expect(selectedChips(tester), 1);
      await robot.tap('submit_for_review');
      final d = await robot.call(() => robot.api.me());
      expect(d.dayOff, isNull);
      expect(d.dayOffSet, isTrue);
      expect(d.status, DriverStatus.submitted);
      await leaveD1(tester);
    });

    testWidgets('D1.4 incomplete: a sheet lists the missing items, each opening its screen', (tester) async {
      final robot = await open(tester, setup: (api) async {
        await d1SignedUp(api);
        await d1AddDocuments(api, kinds: ['profile_photo', 'id_proof', 'licence', 'rc']);
        await d1AddVehicle(api);
      });
      await robot.pickDayOff(7);
      await robot.tap('submit_for_review');
      expect(find.text('Insurance'), findsWidgets);
      expect(find.text('Permit'), findsWidgets);
      expect(find.text('Driving licence'), findsNothing, reason: 'only missing items are listed');
      expect((await robot.call(() => robot.api.me())).status, DriverStatus.signingUp);

      await robot.tapFinder(find.text('Insurance').last);
      expect(find.text('Your documents'), findsWidgets);
      await leaveD1(tester);
    });
  });
}
