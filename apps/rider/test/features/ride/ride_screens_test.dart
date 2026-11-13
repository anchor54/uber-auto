import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/features/ride/ride_screen.dart';

import '../../helpers/r2_robot.dart';
import 'ride_tracker_test.dart' show CountingApi;

/// R2.1 details the flow tests don't reach.
void main() {
  testWidgets('before the driver departs there is no PIN, just the pickup time', (tester) async {
    final r = await R2Robot.start(tester);
    await r.playRide(inMinutes: 20);
    final id = (await r.api.upcomingRides()).first.id;
    tester.state<NavigatorState>(find.byType(Navigator)).push(MaterialPageRoute<void>(builder: (_) => RideScreen(rideId: id)));
    await tester.pumpAndSettle();
    expect(find.text('Ravi picks you up at 9:26 AM'), findsOneWidget);
    expect(find.byKey(const Key('ride.pin')), findsNothing);
    await r.advance(20);
    expect(find.byKey(const Key('ride.pin')), findsOneWidget);
  });

  testWidgets('a position over 60 s old shows "Updating…" instead of a frozen ETA', (tester) async {
    late CountingApi api;
    final r = await R2Robot.start(tester, backend: (now) => api = CountingApi(now: now));
    await r.playRide();
    await r.trackRide();
    expect(find.text('Ravi arrives in 6 min'), findsOneWidget);
    expect(find.byKey(const Key('ride.updating')), findsNothing);

    api.positionAge = const Duration(seconds: 61);
    await r.advance(1);
    expect(find.byKey(const Key('ride.updating')), findsOneWidget);
    expect(find.text('Updating…'), findsOneWidget);
    expect(find.text('Ravi is on the way'), findsOneWidget, reason: 'no ETA from an old position');
    expect(find.text('Last known position'), findsOneWidget);

    api.positionAge = null; // positions flow again
    await r.advance(1);
    expect(find.byKey(const Key('ride.updating')), findsNothing);
  });
}
