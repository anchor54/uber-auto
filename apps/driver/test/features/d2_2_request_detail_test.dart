import 'package:driver/core/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/driver_harness.dart';
import '../helpers/driver_robot.dart';

/// D2.2 Request detail (spec "Screen designs"). Clock: Thu 19 Nov 2026, 10:00 AM.
void main() {
  Future<(DriverRobot, Offer)> openDetail(WidgetTester tester) async {
    final h = await pumpDriverApp(tester);
    final robot = DriverRobot(tester, h);
    await robot.signInToRequests();
    final o = await robot.seriesOffer();
    await robot.openDetail(o);
    return (robot, o);
  }

  const note =
      "Saying you're interested isn't a booking. When the request closes, one interested driver whose week fits every one of these rides is picked at random.";

  group('D2.2 Request detail', () {
    testWidgets('D2.2 header: rider, start date, ride type, rating, route and trip time', (tester) async {
      final (_, o) = await openDetail(tester);
      expect(find.text('Request'), findsOneWidget);
      expect(find.byKey(const Key('back')), findsOneWidget);
      expect(find.text("${o.rider.firstName}'s regular ride"), findsOneWidget);
      expect(find.textContaining('Starts ${dayDate(o.startDate)} · '), findsOneWidget);
      expect(find.textContaining('${o.rider.rating} rating'), findsOneWidget);
      expect(find.text('${o.pickup.label} → ${o.drop.label} · about ${o.travelMinutes} min'), findsOneWidget);
      await leave(tester);
    });

    testWidgets('D2.2 schedule table: Days, Pickup, "Drop, about", grouped by equal times', (tester) async {
      final (_, o) = await openDetail(tester);
      expect(find.text('Days'), findsOneWidget);
      expect(find.text('Pickup'), findsOneWidget);
      expect(find.text('Drop, about'), findsOneWidget);
      // The seed is Mon/Wed/Fri at one time: a single row.
      final out = o.slots.where((s) => s.direction == RideDirection.outbound).toList();
      expect(out.map((s) => s.pickupTime).toSet(), hasLength(1));
      expect(find.text('Mon, Wed, Fri'), findsOneWidget);
      expect(find.text(minutes12(out.first.pickupTime)), findsOneWidget);
      expect(find.text(minutes12(out.first.estimatedDropTime!)), findsOneWidget);
      // No return leg in the seed.
      expect(find.textContaining('Return, '), findsNothing);
      await leave(tester);
    });

    testWidgets('D2.2 earnings: fare per ride, rides a month, usual payment', (tester) async {
      final (_, o) = await openDetail(tester);
      expect(find.text('Fare per ride, paid to you'), findsOneWidget);
      expect(find.text('₹${o.farePerRide}'), findsOneWidget);
      expect(find.text('Rides a month'), findsOneWidget);
      expect(find.text('About ${o.ridesPerMonth}'), findsOneWidget);
      expect(find.text('Usually pays by'), findsOneWidget);
      expect(find.text(o.usualPayment == PaymentMethod.cash ? 'Cash' : 'UPI'), findsOneWidget);
      await leave(tester);
    });

    testWidgets('D2.2 before interest: countdown, "Not for me" and "I\'m interested"', (tester) async {
      await openDetail(tester);
      expect(find.textContaining('Closes in 15'), findsOneWidget);
      expect(find.text('Not for me'), findsOneWidget);
      expect(find.text("I'm interested"), findsOneWidget);
      expect(find.text(note), findsNothing);
      await leave(tester);
    });

    testWidgets('D2.2 after interest: the panel, the note and "Not interested any more"', (tester) async {
      final (robot, o) = await openDetail(tester);
      await robot.tap('detail.interested');
      expect(find.text("You're interested. We'll tell you by ${hm12(o.expiresAt)} whether you got it."), findsOneWidget);
      expect(find.text(note), findsOneWidget);
      expect(find.text('Not interested any more'), findsOneWidget);
      expect(find.text("I'm interested"), findsNothing);
      expect((await robot.api.offer(o.id)).interested, isTrue);
      await leave(tester);
    });

    testWidgets('D2.2 withdrawing interest before the window closes', (tester) async {
      final (robot, o) = await openDetail(tester);
      await robot.tap('detail.interested');
      await robot.advance(const Duration(minutes: 5));
      await robot.tap('detail.withdraw');
      expect(find.text("I'm interested"), findsOneWidget);
      expect(find.text('Not interested any more'), findsNothing);
      expect((await robot.api.offer(o.id)).interested, isFalse);
      await leave(tester);
    });

    testWidgets('D2.2 interest shows on the D2.1 card on the way back', (tester) async {
      final (robot, o) = await openDetail(tester);
      await robot.tap('detail.interested');
      await robot.tap('back');
      expect(find.text("We'll tell you by ${hm12(o.expiresAt)}"), findsOneWidget);
      await leave(tester);
    });

    testWidgets('D2.2 "Not for me" hides the request and goes back to the list', (tester) async {
      final (robot, o) = await openDetail(tester);
      await robot.tap('detail.notForMe');
      expect(find.byKey(Key('requests.card.${o.id}')), findsNothing);
      expect((await robot.api.offers()).offers.map((x) => x.id), isNot(contains(o.id)));
      await leave(tester);
    });

    testWidgets('D2.2 after the window closes the button reads "Closed"', (tester) async {
      final (robot, o) = await openDetail(tester);
      await robot.advance(o.expiresAt.difference(robot.h.clock.now()) + const Duration(seconds: 1));
      expect(find.text('Closed'), findsOneWidget);
      expect(find.text("I'm interested"), findsNothing);
      await leave(tester);
    });

    testWidgets('D2.2 a late tap gets 409 offer_closed and the screen redraws as "Closed"', (tester) async {
      final (robot, o) = await openDetail(tester);
      // The clock passes the window without the screen ticking.
      robot.h.clock.advance(o.expiresAt.difference(robot.h.clock.now()) + const Duration(seconds: 1));
      await tester.tap(find.byKey(const Key('detail.interested')));
      await tester.pumpAndSettle();
      expect(find.text('Closed'), findsOneWidget);
      expect(find.text("You're interested. We'll tell you by ${hm12(o.expiresAt)} whether you got it."), findsNothing);
      await leave(tester);
    });
  });
}
