import 'package:driver/core/fake_api.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/driver_harness.dart';
import '../helpers/driver_robot.dart';

/// Flow D2 end to end on the demo backend: see the request → I'm interested
/// → the window closes → win banner → Your week shows the new rides; at
/// three screen sizes. Plus the losing and withdrawing paths.
void main() {
  const sizes = {
    'small phone 360x640': Size(360, 640),
    'iPhone 14 390x844': Size(390, 844),
    'Pixel 7 412x915': Size(412, 915),
  };

  for (final entry in sizes.entries) {
    testWidgets('D2 see request → interested → win banner → week on ${entry.key}', (tester) async {
      final h = await pumpDriverApp(tester, size: entry.value);
      final robot = DriverRobot(tester, h);
      await robot.signInToRequests();

      final o = await robot.seriesOffer();
      expect(find.text('${o.rider.name} · details'), findsOneWidget);
      await robot.openDetail(o);
      await robot.tap('detail.interested');
      expect(find.text("You're interested. We'll tell you by ${hm12(o.expiresAt)} whether you got it."), findsOneWidget);
      await robot.tap('back');
      expect(find.text('Interested'), findsWidgets);

      await robot.closeWindow(o);
      expect(find.byKey(const Key('requests.winBanner')), findsOneWidget);
      expect(find.textContaining("You got it: ${o.rider.firstName}'s "), findsOneWidget);
      expect((await robot.api.offer(o.id)).result, 'picked');

      await robot.tap('requests.winBanner');
      expect(find.text('Your week'), findsOneWidget);
      expect(find.text("You're ${o.rider.firstName}'s regular driver from ${dayDate(o.startDate)}."), findsOneWidget);
      expect(find.text('New rider'), findsOneWidget);
      final week = await robot.api.week(start: o.startDate);
      expect(week.rides, isNotEmpty);
      expect(week.rides.every((r) => r.seriesId == o.seriesId && r.isNew), isTrue);
      expect(tester.takeException(), isNull);
      await leave(tester);
    });
  }

  testWidgets('D2 interested, then withdrawn: the window closes with no win', (tester) async {
    final h = await pumpDriverApp(tester);
    final robot = DriverRobot(tester, h);
    await robot.signInToRequests();
    final o = await robot.seriesOffer();
    await robot.openDetail(o);
    await robot.tap('detail.interested');
    await robot.tap('detail.withdraw');
    await robot.tap('back');
    await robot.closeWindow(o);
    expect(find.byKey(const Key('requests.winBanner')), findsNothing);
    expect(find.byKey(Key('requests.card.${o.id}')), findsNothing);
    expect((await robot.api.week(start: o.startDate)).rides, isEmpty);
    await leave(tester);
  });

  testWidgets('D2 interested, another driver picked: result card, no rides', (tester) async {
    final h = await pumpDriverApp(tester);
    final robot = DriverRobot(tester, h);
    await robot.signInToRequests();
    final o = await robot.seriesOffer();
    await robot.interested(o);
    robot.api.goesToAnotherDriver(o.id);
    await robot.openRequests();
    expect(find.text('Went to another driver'), findsOneWidget);
    expect((await robot.api.week(start: o.startDate)).rides, isEmpty);
    await leave(tester);
  });

  testWidgets('D2 survives the slow network used in demo mode', (tester) async {
    final h = await pumpDriverApp(
      tester,
      backend: (now) => FakeDriverApi(now: now, latency: const Duration(milliseconds: 500)),
    );
    final robot = DriverRobot(tester, h);
    await robot.signInToRequests();
    final o = await robot.seriesOffer();
    await robot.interested(o);
    await robot.closeWindow(o);
    expect(find.byKey(const Key('requests.winBanner')), findsOneWidget);
    await leave(tester);
  });
}
