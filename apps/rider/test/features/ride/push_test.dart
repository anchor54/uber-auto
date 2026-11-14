import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/push.dart';

import '../../helpers/app_harness.dart';
import '../../helpers/r2_robot.dart';
import 'ride_tracker_test.dart' show CountingApi;

/// Push notifications (R2 step 10): the device registers, a tapped
/// notification opens its ride, and one that arrives with the app open
/// makes the ride screen ask straight away.
void main() {
  const firstRide = 'ride_20261022_outbound'; // the commute's first leg, Thu 22 Oct

  testWidgets('Home registers the device for pushes once the rider has a commute', (tester) async {
    final push = FakePush();
    final r = await R2Robot.start(tester, push: push);
    // Each Home registers; the backend keeps one row per token.
    expect(r.api.pushTokens.toSet(), {'fake-push-token-0001'});
  });

  testWidgets('no push token (permission refused): nothing is registered', (tester) async {
    final r = await R2Robot.start(tester, push: FakePush(deviceToken: null));
    expect(r.api.pushTokens, isEmpty);
  });

  testWidgets('tapping "Driver on the way" opens that ride on R2.1', (tester) async {
    final push = FakePush();
    final r = await R2Robot.start(tester, push: push);
    await r.playRide();
    final ride = (await r.api.upcomingRides()).first;
    expect(find.byKey(const Key('ride.onTheWay')), findsNothing);

    push.deliver(PushEvent(kind: 'driver_on_the_way', rideId: ride.id, opened: true));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('ride.onTheWay')), findsOneWidget);

    // A second tap on the same ride doesn't stack another screen.
    push.deliver(PushEvent(kind: 'driver_arrived', rideId: ride.id, opened: true));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('ride.onTheWay')), findsOneWidget);
  });

  testWidgets('opening the app from a notification goes to its ride', (tester) async {
    final r = await R2Robot.start(tester);
    // The app is closed; the rider taps "Leg is on", which starts it again.
    final push = FakePush(launchEvent: const PushEvent(kind: 'leg_is_on', rideId: firstRide, opened: true));
    await tester.pumpWidget(const SizedBox());
    await pumpRiderApp(tester, api: r.api, push: push);
    expect(find.byKey(const Key('ride.onTheWay')), findsOneWidget, reason: 'R2.1 for the tapped ride');
    expect(await push.takeLaunchEvent(), isNull, reason: 'taken once');
  });

  testWidgets('a push for the open ride asks for it at once, not at the next 5-second poll', (tester) async {
    final push = FakePush();
    late CountingApi api;
    final r = await R2Robot.start(tester, push: push, backend: (now) => api = CountingApi(now: now));
    await r.playRide();
    await r.trackRide();
    final ride = (await r.api.upcomingRides()).first;
    final before = api.liveCalls;

    push.deliver(PushEvent(kind: 'driver_arrived', rideId: ride.id, opened: false));
    await tester.pump();
    await tester.pump();
    expect(api.liveCalls, before + 1);

    // A push about another ride is not this screen's business.
    push.deliver(const PushEvent(kind: 'driver_arrived', rideId: 'ride_other', opened: false));
    await tester.pump();
    expect(api.liveCalls, before + 1);
  });

  test('push data without a ride id is ignored', () {
    expect(PushEvent.fromData({'kind': 'leg_is_on'}, opened: true), isNull);
    final e = PushEvent.fromData({'kind': 'leg_is_on', 'rideId': 'r1'}, opened: false)!;
    expect([e.kind, e.rideId, e.opened], ['leg_is_on', 'r1', false]);
  });
}
