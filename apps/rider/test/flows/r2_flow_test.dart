import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/dialer.dart';
import 'package:rider/core/fake_ride_script.dart';
import 'package:rider/core/models.dart';

import '../helpers/r2_robot.dart';

/// Flow R2 end to end on the demo backend: Home → R2.1 → R2.2 → R2.3 → Home,
/// and the no-show and late-driver branches.
void main() {
  const sizes = {
    'small phone 360x640': Size(360, 640),
    'iPhone 14 390x844': Size(390, 844),
    'Pixel 7 412x915': Size(412, 915),
  };

  for (final entry in sizes.entries) {
    testWidgets('R2 normal leg on ${entry.key}', (tester) async {
      final r = await R2Robot.start(tester, size: entry.value);
      expect(find.byKey(const Key('home.track')), findsNothing, reason: 'nothing to track before the driver departs');

      await r.playRide();
      expect(find.text('Ravi is on the way'), findsOneWidget);
      await r.trackRide();

      // R2.1: ETA, car and plate, the PIN.
      expect(find.text('Ravi arrives in 6 min'), findsOneWidget);
      expect(find.text('White Dzire · KA 01 AB 1234'), findsOneWidget);
      final pin = tester.widget<Text>(find.byKey(const Key('ride.pin'))).data!;
      expect(pin, matches(RegExp(r'^\d{4}$')));
      expect(find.byKey(const Key('ride.map')), findsOneWidget);

      await r.advance(3);
      expect(find.text('Ravi arrives in 3 min'), findsOneWidget, reason: 'the ETA falls as the driver moves');

      // Arrived: the wait countdown.
      await r.advance(3);
      expect(find.byKey(const Key('ride.arrivedBanner')), findsOneWidget);
      expect(find.textContaining('(5:00 left)'), findsOneWidget);

      // R2.2 in the ride.
      await r.advance(1);
      expect(find.byKey(const Key('ride.inRide')), findsOneWidget);
      expect(find.byKey(const Key('ride.sos')), findsOneWidget);
      expect(find.byKey(const Key('ride.pin')), findsNothing);

      // R2.3 arrived, pay and rate.
      await r.advance(40);
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('ride.arrived')), findsOneWidget);
      expect(find.text('₹250'), findsOneWidget);
      expect(find.text('UPI: ravi.kumar@okbank'), findsOneWidget);
      await r.tap('pay.upi');
      expect(find.text('Ravi marked this received.'), findsOneWidget);
      await r.tap('rate.star5');
      await r.tap('rate.tag.On time');
      await r.tap('rate.done');

      expect(find.byKey(const Key('home.commute')), findsOneWidget);
      final done = (await r.api.upcomingRides()).firstWhere((x) => x.state == RideState.completed);
      expect(done.riderPayment, PaymentMethod.upi);
      expect(done.rating!.stars, 5);
      expect(done.rating!.tags, ['On time']);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('a no-show: the wait runs out, the rider is told, Home says so', (tester) async {
    final r = await R2Robot.start(tester);
    await r.playRide(outcome: FakeRideOutcome.noShow);
    await r.trackRide();
    await r.advance(6);
    expect(find.byKey(const Key('ride.arrivedBanner')), findsOneWidget);
    await r.advance(5);
    await tester.pumpAndSettle();
    expect(find.text('Marked as a no-show'), findsOneWidget);
    await r.tap('ride.done');
    expect(find.byKey(const Key('home.noShow')), findsOneWidget);
    expect(find.textContaining('Your return ride is still booked.'), findsOneWidget);
  });

  testWidgets('a late driver: R2.1 shows the banner with Call', (tester) async {
    final r = await R2Robot.start(tester);
    await r.playRide(lateMinutes: 12);
    await r.trackRide();
    expect(find.byKey(const Key('ride.late')), findsOneWidget);
    expect(find.text('Ravi is running late. Call to check where they are.'), findsOneWidget);

    final calls = <Uri>[];
    openUri = (uri) async {
      calls.add(uri);
      return true;
    };
    addTearDown(() => openUri = (uri) async => false);
    await r.tap('ride.call');
    expect(calls.single.toString(), 'tel:9000000010');
  });

  testWidgets('"I\'m 5 min late" once: the driver waits 10 minutes', (tester) async {
    final r = await R2Robot.start(tester);
    await r.playRide();
    await r.trackRide();
    await r.tap('ride.runningLate');
    expect(find.text("We told Ravi. They'll wait 10 minutes instead of 5."), findsOneWidget);
    expect(find.byKey(const Key('ride.runningLate')), findsNothing);
    await r.advance(6);
    expect(find.textContaining('(10:00 left)'), findsOneWidget);
  });

  testWidgets('SOS asks first, then dials 112', (tester) async {
    final r = await R2Robot.start(tester);
    await r.playRide();
    await r.trackRide();
    await r.advance(7);
    await tester.pumpAndSettle();
    final calls = <Uri>[];
    openUri = (uri) async {
      calls.add(uri);
      return true;
    };
    addTearDown(() => openUri = (uri) async => false);
    await r.tap('ride.sos');
    expect(find.text('Call 112?'), findsOneWidget);
    await r.tap('ride.sos.confirm');
    expect(calls.single.toString(), 'tel:112');
  });

  testWidgets('a finished ride left unmarked opens pay-and-rate on Home, once; Done without either is fine', (tester) async {
    final r = await R2Robot.start(tester);
    await r.playRide();
    // The rider never opens the ride; it finishes while they're on Home.
    await r.advance(60);
    await r.refreshHome();
    expect(find.byKey(const Key('ride.arrived')), findsOneWidget);
    await r.tap('rate.done');
    expect(find.byKey(const Key('home.commute')), findsOneWidget);
    await r.refreshHome();
    expect(find.byKey(const Key('ride.arrived')), findsNothing, reason: "doesn't reopen in the same session");
  });

  testWidgets('Home: the next pickup comes from the ride records', (tester) async {
    final r = await R2Robot.start(tester);
    expect(find.text('First ride · Thu 22 Oct'), findsOneWidget);
    await r.playRide(inMinutes: 20);
    // Today's played ride is now the next one; no Track ride until departure.
    expect(find.textContaining('Today ·'), findsOneWidget);
    expect(find.byKey(const Key('home.track')), findsNothing);
    await r.advance(20);
    await r.refreshHome();
    expect(find.byKey(const Key('home.track')), findsOneWidget);
  });
}
