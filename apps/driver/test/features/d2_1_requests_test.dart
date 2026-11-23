import 'package:driver/core/fake_api.dart';
import 'package:driver/core/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/driver_harness.dart';
import '../helpers/driver_robot.dart';

/// D2.1 Requests (spec "Screen designs"). Clock: Thu 19 Nov 2026, 10:00 AM.
void main() {
  Future<DriverRobot> open(WidgetTester tester, {bool seed = true}) async {
    final h = await pumpDriverApp(tester, backend: (now) => FakeDriverApi(now: now, seed: seed));
    final robot = DriverRobot(tester, h);
    await robot.signInToRequests();
    return robot;
  }

  Finder card(Offer o) => find.byKey(Key('requests.card.${o.id}'));

  group('D2.1 Requests', () {
    testWidgets('D2.1 header and bottom bar', (tester) async {
      await open(tester);
      expect(find.text('Requests'), findsWidgets);
      expect(
        find.textContaining(
            "drivers. Tap I'm interested; when a request closes, one interested driver whose week fits is picked at random."),
        findsOneWidget,
      );
      expect(find.textContaining('Regular rides open to '), findsOneWidget);
      for (final t in ['today', 'requests', 'earnings', 'account']) {
        expect(find.byKey(Key('tab.$t')), findsOneWidget, reason: t);
      }
      expect(find.text('Today'), findsOneWidget);
      expect(find.text('Earnings'), findsOneWidget);
      expect(find.text('Account'), findsOneWidget);
      await leave(tester);
    });

    testWidgets('D2.1 series request card', (tester) async {
      final robot = await open(tester);
      final o = await robot.seriesOffer();
      expect(card(o), findsOneWidget);
      expect(find.text('${o.rider.name} · details'), findsOneWidget);
      expect(find.textContaining('Starts ${dayDate(o.startDate)}'), findsWidgets);
      expect(find.textContaining('· ${o.rider.rating} rating'), findsWidgets);
      expect(find.text('Closes in 15 min'), findsOneWidget);
      expect(find.textContaining(o.pickup.label), findsWidgets);
      expect(find.textContaining(o.drop.label), findsWidgets);
      expect(find.text('Days'), findsWidgets);
      expect(find.text('Pickup'), findsWidgets);
      expect(find.text('Return'), findsWidgets);
      expect(find.text('—'), findsOneWidget, reason: 'no return leg');
      expect(find.text(minutes12(o.slots.first.pickupTime)), findsWidgets);
      expect(find.text('Fare per ride, paid to you'), findsOneWidget);
      expect(find.text('₹${o.farePerRide}'), findsWidgets);
      expect(find.text('Not for me'), findsWidgets);
      expect(find.text("I'm interested"), findsWidgets);
      // The old fit labels and Accept / Decline are gone.
      expect(find.text('Accept'), findsNothing);
      expect(find.text('Decline'), findsNothing);
      await leave(tester);
    });

    testWidgets('D2.1 the countdown chip counts down on the clock', (tester) async {
      final robot = await open(tester);
      expect(find.text('Closes in 15 min'), findsOneWidget);
      await robot.advance(const Duration(minutes: 5));
      expect(find.text('Closes in 10 min'), findsOneWidget);
      expect(find.text('Closes in 15 min'), findsNothing);
      await leave(tester);
    });

    testWidgets('D2.1 one-ride card, compact', (tester) async {
      final robot = await open(tester);
      final o = await robot.rideOffer();
      expect(card(o), findsOneWidget);
      expect(find.text('One ride'), findsOneWidget);
      expect(find.textContaining(' · cover for another driver'), findsOneWidget);
      expect(find.text('5 min left'), findsOneWidget);
      expect(find.textContaining('→'), findsWidgets);
      await leave(tester);
    });

    testWidgets('D2.1 newest first', (tester) async {
      final robot = await open(tester);
      final offers = (await robot.api.offers()).offers;
      for (var i = 1; i < offers.length; i++) {
        expect(offers[i - 1].openedAt.isBefore(offers[i].openedAt), isFalse);
      }
      await leave(tester);
    });

    testWidgets('D2.1 "I\'m interested" turns into "Interested" and says when we\'ll tell', (tester) async {
      final robot = await open(tester);
      final o = await robot.seriesOffer();
      await robot.interested(o);
      expect(find.descendant(of: find.byKey(Key('requests.interested.${o.id}')), matching: find.text('Interested')), findsOneWidget);
      expect(find.byIcon(Icons.check), findsWidgets);
      expect(find.text("We'll tell you by ${hm12(o.expiresAt)}"), findsOneWidget);
      expect((await robot.api.offer(o.id)).interested, isTrue);
      await leave(tester);
    });

    testWidgets('D2.1 "Not for me" hides the request for good', (tester) async {
      final robot = await open(tester);
      final o = await robot.seriesOffer();
      final other = await robot.rideOffer();
      await robot.tap('requests.notForMe.${o.id}');
      expect(card(o), findsNothing);
      expect(card(other), findsOneWidget, reason: 'only that request is hidden');
      expect((await robot.api.offers()).offers.map((x) => x.id), isNot(contains(o.id)));
      // For good: a minute later, with its window still open, Requests is
      // read again and the request stays hidden.
      await robot.advance(const Duration(minutes: 1));
      await robot.openRequests();
      expect(card(o), findsNothing);
      expect((await robot.api.offer(o.id)).dismissed, isTrue);
      await leave(tester);
    });

    testWidgets('D2.1 a win shows the green banner until tapped, which opens D2.3', (tester) async {
      final robot = await open(tester);
      final o = await robot.seriesOffer();
      await robot.interested(o);
      await robot.closeWindow(o);
      final banner = find.byKey(const Key('requests.winBanner'));
      expect(banner, findsOneWidget);
      expect(find.textContaining("You got it: ${o.rider.firstName}'s "), findsOneWidget);
      expect(find.textContaining('rides are yours from ${dayDate(o.startDate)}.'), findsOneWidget);
      expect(card(o), findsNothing, reason: 'no longer an open request');

      // Still there when Requests opens again.
      await robot.openRequests();
      expect(banner, findsOneWidget);

      await robot.tap('requests.winBanner');
      expect(find.text('Your week'), findsOneWidget);
      await robot.openRequests();
      expect(find.byKey(const Key('requests.winBanner')), findsNothing, reason: 'shown until tapped');
      await leave(tester);
    });

    testWidgets('D2.1 not picked: "Went to another driver", kept 24 hours', (tester) async {
      final robot = await open(tester);
      final o = await robot.seriesOffer();
      await robot.interested(o);
      robot.api.goesToAnotherDriver(o.id);
      await robot.openRequests();
      expect(find.text('Went to another driver'), findsOneWidget);
      expect(card(o), findsNothing);
      expect(find.byKey(const Key('requests.winBanner')), findsNothing);

      await robot.advance(const Duration(hours: 23));
      await robot.openRequests();
      expect(find.text('Went to another driver'), findsOneWidget);
      await robot.advance(const Duration(hours: 2));
      await robot.openRequests();
      expect(find.text('Went to another driver'), findsNothing);
      await leave(tester);
    });

    testWidgets('D2.1 the rider withdrew: "The rider withdrew this request"', (tester) async {
      final robot = await open(tester);
      final o = await robot.seriesOffer();
      await robot.interested(o);
      robot.api.riderWithdraws(o.id);
      await robot.openRequests();
      expect(find.text('The rider withdrew this request'), findsOneWidget);
      expect(card(o), findsNothing);
      await leave(tester);
    });

    testWidgets('D2.1 no result card for a request the driver was not interested in', (tester) async {
      final robot = await open(tester);
      final o = await robot.seriesOffer();
      robot.api.goesToAnotherDriver(o.id);
      await robot.openRequests();
      expect(card(o), findsNothing);
      expect(find.text('Went to another driver'), findsNothing);
      await leave(tester);
    });

    testWidgets('D2.1 empty state', (tester) async {
      await open(tester, seed: false);
      expect(find.text("No open requests right now. We'll notify you when a new one comes in."), findsOneWidget);
      expect(find.text("I'm interested"), findsNothing);
      await leave(tester);
    });

    testWidgets('D2.1 the list empties once every window has passed', (tester) async {
      final robot = await open(tester);
      await robot.advance(const Duration(minutes: 20));
      await robot.openRequests();
      expect(find.text("No open requests right now. We'll notify you when a new one comes in."), findsOneWidget);
      await leave(tester);
    });
  });
}
