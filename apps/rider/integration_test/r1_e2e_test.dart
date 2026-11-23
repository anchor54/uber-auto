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

/// R1 in real mode (spec "App-level integration test"): the app on a device
/// or emulator against the Go backend, with drivers marked interested over
/// the driver API and rounds closed with the dev clock and the sweep.
/// Skipped unless built with API_BASE_URL.
///
///   # backend: FIXED_OTP=123456 ENABLE_DEV_ROUTES=true, spec seed
///   flutter test integration_test/r1_e2e_test.dart \
///     --dart-define=API_BASE_URL=http://10.0.2.2:8080   # emulator → host
///     [--dart-define=SWEEP_TOKEN=…]
const apiBaseUrl = String.fromEnvironment('API_BASE_URL');
const sweepToken = String.fromEnvironment('SWEEP_TOKEN');

/// The spec seed's live auto drivers (Ravi, Meena, Anil, Suresh).
const driverPhones = ['9000000201', '9000000202', '9000000203', '9000000204'];

/// A fresh rider per test, so runs don't see each other's series.
String freshPhone() => '9${Random().nextInt(900000000) + 100000000}';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  final skip = apiBaseUrl.isEmpty;

  Future<void> launch(WidgetTester tester, SessionStore store) async {
    await tester.pumpWidget(RiderApp(api: HttpRiderApi(baseUrl: apiBaseUrl, store: store), demoMode: false));
    await tester.pumpAndSettle();
  }

  Future<Map<String, dynamic>?> post(String path, {Object? body, String? token}) async {
    final res = await http.post(
      Uri.parse('$apiBaseUrl$path'),
      headers: {'content-type': 'application/json', if (token != null) 'authorization': 'Bearer $token'},
      body: jsonEncode(body ?? {}),
    );
    expect(res.statusCode, inInclusiveRange(200, 299), reason: '$path: ${res.body}');
    return res.body.isEmpty ? null : jsonDecode(res.body) as Map<String, dynamic>;
  }

  /// The rider's newest series, as the app sees it.
  Future<Series> latestSeries(SessionStore store) async =>
      (await HttpRiderApi(baseUrl: apiBaseUrl, store: store).home()).series.first;

  /// Every seeded auto driver says "I'm interested" in [seriesId].
  Future<void> driversInterested(String seriesId) async {
    for (final phone in driverPhones) {
      await post('/v1/driver/auth/otp', body: {'phone': phone});
      final token = (await post('/v1/driver/auth/verify', body: {'phone': phone, 'code': '123456'}))!['token'] as String;
      final res = await http.get(Uri.parse('$apiBaseUrl/v1/offers'), headers: {'authorization': 'Bearer $token'});
      final offers = ((jsonDecode(res.body) as Map<String, dynamic>)['offers'] as List).cast<Map<String, dynamic>>();
      for (final o in offers.where((o) => o['series_id'] == seriesId)) {
        await post('/v1/offers/${o['id']}/mark-interested', token: token);
      }
    }
  }

  /// "Move the clock 15 minutes": the dev clock, then one sweep.
  Future<void> closeRound() async {
    await post('/dev/clock/advance', body: {'minutes': 15});
    final res = await http.post(
      Uri.parse('$apiBaseUrl/internal/sweep'),
      headers: {if (sweepToken.isNotEmpty) 'authorization': 'Bearer $sweepToken'},
    );
    expect(res.statusCode, 200, reason: res.body);
  }

  /// R1.2 with an auto (the spec seed's drivers drive autos).
  Future<void> request(R1Robot robot, {Set<RidePreference> toggle = const {}}) async {
    await robot.fillSetup();
    await robot.chooseRideType(RideType.auto, toggle: toggle);
    await robot.request();
    expect(find.byKey(const Key('home.finding')), findsOneWidget);
    expect(find.text('Finding your driver'), findsOneWidget);
  }

  testWidgets('R1: request, "Finding your driver", drivers interested, 15 minutes on, the driver on Home', (tester) async {
    final store = MemorySessionStore();
    await launch(tester, store);
    final robot = R1Robot(tester);
    await robot.signIn(phone: freshPhone());
    await request(robot);

    final s = await latestSeries(store);
    expect(s.status, SeriesStatus.findingDriver);
    await driversInterested(s.id);
    await closeRound();
    await robot.refreshHome();
    expect(find.text('Driver confirmed'), findsOneWidget);

    // Relaunch with the same saved session: Home reads the series again.
    await tester.pumpWidget(const SizedBox());
    await launch(tester, store);
    expect(find.text('Driver confirmed'), findsOneWidget);
  }, skip: skip);

  testWidgets('R1: nobody interested, three rounds, "No driver yet", Try again', (tester) async {
    final store = MemorySessionStore();
    await launch(tester, store);
    final robot = R1Robot(tester);
    await robot.signIn(phone: freshPhone());
    await request(robot);
    for (var i = 0; i < 3; i++) {
      await closeRound();
    }
    await robot.refreshHome();
    expect(find.text('No driver yet'), findsOneWidget);
    await robot.tap('noDriver.retry');
    expect(find.text('Finding your driver'), findsOneWidget);
    expect((await latestSeries(store)).status, SeriesStatus.findingDriver);
    await robot.cancelRequest(); // leave nothing open behind
  }, skip: skip);

  testWidgets('R1: cancel the request', (tester) async {
    final store = MemorySessionStore();
    await launch(tester, store);
    final robot = R1Robot(tester);
    await robot.signIn(phone: freshPhone());
    await request(robot);
    await robot.cancelRequest();
    expect(find.byKey(const Key('home.setup')), findsOneWidget);
    expect(await HttpRiderApi(baseUrl: apiBaseUrl, store: store).home().then((h) => h.series), isEmpty);
  }, skip: skip);

  testWidgets('R1: a wrong code shows the backend message', (tester) async {
    await launch(tester, MemorySessionStore());
    final robot = R1Robot(tester);
    await robot.type('welcome.phone', freshPhone());
    await robot.tap('welcome.sendOtp');
    await robot.type('otp.code', '000000');
    expect(find.byKey(const Key('otp.error')), findsOneWidget);
  }, skip: skip);
}
