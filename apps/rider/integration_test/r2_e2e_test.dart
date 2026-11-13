import 'dart:convert';
import 'dart:math';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:integration_test/integration_test.dart';
import 'package:rider/app.dart';
import 'package:rider/core/http_api.dart';
import 'package:rider/core/models.dart';
import 'package:rider/core/session_store.dart';

import '../test/helpers/r1_robot.dart';

/// Flow R2 in real mode: the app on a device or emulator against the backend
/// and Postgres, with the dev simulator driving the ride. Skipped unless
/// built with API_BASE_URL.
///
///   # backend: FIXED_OTP=123456 ENABLE_DRIVER_SIM=true RUN_JOBS_IN_API=true
///   flutter test integration_test/r2_e2e_test.dart \
///     --dart-define=API_BASE_URL=http://10.0.2.2:8080
const apiBaseUrl = String.fromEnvironment('API_BASE_URL');

Future<Map<String, Object?>> sim(String path, [Object body = const {}]) async {
  final res = await http.post(
    Uri.parse('$apiBaseUrl/dev/sim/$path'),
    headers: {'content-type': 'application/json'},
    body: jsonEncode(body),
  );
  expect(res.statusCode, inInclusiveRange(200, 202), reason: '$path: ${res.body}');
  return jsonDecode(res.body) as Map<String, Object?>;
}

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  /// Pumps until [finder] shows, for up to [seconds] of real time.
  Future<void> waitFor(WidgetTester tester, Finder finder, {int seconds = 60}) async {
    for (var i = 0; i < seconds * 2; i++) {
      if (finder.evaluate().isNotEmpty) return;
      await tester.pump(const Duration(milliseconds: 500));
    }
    expect(finder, findsOneWidget, reason: 'waited $seconds s');
  }

  testWidgets('R2: the driver comes, the rider rides, pays and rates', (tester) async {
    final store = MemorySessionStore();
    final api = HttpRiderApi(baseUrl: apiBaseUrl, store: store);
    await tester.pumpWidget(RiderApp(api: api, demoMode: false));
    await tester.pumpAndSettle();
    final robot = R1Robot(tester);

    // R1 + D2: a commute the driver has accepted, so its rides exist.
    await robot.signIn(phone: '9${Random().nextInt(900000000) + 100000000}');
    await robot.fillSetup();
    await robot.chooseRideType(RideType.cabSedan);
    await robot.confirm();
    final commute = (await api.home()).commute!;
    await sim('commutes/${commute.id}/accept');
    final ride = (await api.upcomingRides(days: 14)).first;
    expect(ride.state, RideState.scheduled);

    // Move the server to 20 minutes before that pickup and let the driver go.
    await sim('clock', {'now': ride.pickupAt.subtract(const Duration(minutes: 20)).toUtc().toIso8601String()});
    await sim('rides/${ride.id}/play', {'speed': 120});

    // Home → R2.1 once the driver is on the way.
    await waitFor(tester, find.byKey(const Key('home.commute')));
    for (var i = 0; i < 20 && find.byKey(const Key('home.track')).evaluate().isEmpty; i++) {
      await tester.fling(find.byKey(const Key('home.commute')), const Offset(0, 400), 1000);
      await tester.pumpAndSettle();
    }
    await robot.tap('home.track');
    await waitFor(tester, find.byKey(const Key('ride.onTheWay')));
    expect(find.byKey(const Key('ride.pin')), findsOneWidget);

    // R2.2, then R2.3, following the simulator through 5-second polls.
    await waitFor(tester, find.byKey(const Key('ride.inRide')), seconds: 90);
    await waitFor(tester, find.byKey(const Key('ride.arrived')), seconds: 120);
    await tester.pumpAndSettle();
    await robot.tap('pay.cash');
    await robot.tap('rate.star5');
    await robot.tap('rate.done');
    expect(find.byKey(const Key('home.commute')), findsOneWidget);

    final done = await api.ride(ride.id);
    expect(done.state, RideState.completed);
    expect(done.riderPayment, PaymentMethod.cash);
    expect(done.rating?.stars, 5);
  }, skip: apiBaseUrl.isEmpty);
}
