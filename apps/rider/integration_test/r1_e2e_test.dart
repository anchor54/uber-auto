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

/// Flows R1 and D2 in real mode: the app on a device or emulator, talking to
/// the backend and Postgres, with the dev simulator playing the driver.
/// Skipped unless built with API_BASE_URL.
///
///   # backend: FIXED_OTP=123456 ENABLE_DRIVER_SIM=true (auto-accept off)
///   flutter test integration_test/r1_e2e_test.dart \
///     --dart-define=API_BASE_URL=http://10.0.2.2:8080   # emulator → host
const apiBaseUrl = String.fromEnvironment('API_BASE_URL');

/// A fresh rider per test, so runs don't see each other's commutes.
String freshPhone() => '9${Random().nextInt(900000000) + 100000000}';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  final skip = apiBaseUrl.isEmpty;

  Future<void> launch(WidgetTester tester, SessionStore store) async {
    await tester.pumpWidget(RiderApp(api: HttpRiderApi(baseUrl: apiBaseUrl, store: store), demoMode: false));
    await tester.pumpAndSettle();
  }

  /// The driver answers through the dev simulator, as the driver app will.
  Future<void> driver(SessionStore store, String answer) async {
    final home = await HttpRiderApi(baseUrl: apiBaseUrl, store: store).home();
    final res = await http.post(Uri.parse('$apiBaseUrl/dev/sim/commutes/${home.commute!.id}/$answer'));
    expect(res.statusCode, 200, reason: res.body);
    expect(jsonDecode(res.body)['status'], answer == 'accept' ? 'active' : 'declined');
  }

  /// Pull down on Home to check for the driver's answer now.
  Future<void> refreshHome(WidgetTester tester) async {
    await tester.fling(find.byKey(const Key('home.commute')), const Offset(0, 400), 1000);
    await tester.pumpAndSettle();
  }

  testWidgets('R1 + D2: confirm, the driver accepts, the rider stays signed in', (tester) async {
    final store = MemorySessionStore();
    await launch(tester, store);
    final robot = R1Robot(tester);
    await robot.signIn(phone: freshPhone());
    expect(store.token, isNotNull, reason: 'the session is saved');
    await robot.fillSetup();
    await robot.chooseRideType(RideType.cabSedan);
    expect(find.byKey(const Key('match.found')), findsOneWidget);
    await robot.confirm(pay: PaymentMethod.cash);
    expect(find.byKey(const Key('home.awaiting')), findsOneWidget);

    await driver(store, 'accept');
    await refreshHome(tester);
    expect(find.text('Driver confirmed'), findsOneWidget);

    // Relaunch with the same saved session: straight to Home, commute intact.
    await tester.pumpWidget(const SizedBox());
    await launch(tester, store);
    expect(find.text('Driver confirmed'), findsOneWidget);
  }, skip: skip);

  testWidgets('D2: the driver declines; the rider books someone else for the same route', (tester) async {
    final store = MemorySessionStore();
    await launch(tester, store);
    final robot = R1Robot(tester);
    await robot.signIn(phone: freshPhone());
    await robot.fillSetup();
    await robot.chooseRideType(RideType.cabSedan);
    final first = tester.widget<Text>(find.byKey(const Key('match.driverName'))).data!;
    await robot.confirm();
    await driver(store, 'decline');
    await refreshHome(tester);
    expect(find.byKey(const Key('home.declined')), findsOneWidget);

    await robot.tap('home.rematch');
    expect(find.byKey(const Key('match.found')), findsOneWidget);
    final second = tester.widget<Text>(find.byKey(const Key('match.driverName'))).data!;
    expect(second, isNot(first), reason: 'the driver who declined is not offered again');
    await robot.confirm();
    expect(find.text('Waiting for ${second.split(' ').first}'), findsOneWidget);
    await driver(store, 'accept'); // leave nothing waiting behind
  }, skip: skip);

  testWidgets('R1: no driver fits, join the waitlist', (tester) async {
    await launch(tester, MemorySessionStore());
    final robot = R1Robot(tester);
    await robot.signIn(phone: freshPhone());
    await robot.fillSetup();
    // No seeded driver is a woman, so the backend finds nobody.
    await robot.chooseRideType(RideType.cabSedan, toggle: {RidePreference.womanDriver});
    expect(find.byKey(const Key('match.none')), findsOneWidget);
    await robot.tap('match.waitlist');
    expect(find.byKey(const Key('home.empty')), findsOneWidget);
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
