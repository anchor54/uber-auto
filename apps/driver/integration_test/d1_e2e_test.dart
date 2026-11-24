import 'dart:convert';
import 'dart:math';

import 'package:driver/app.dart';
import 'package:driver/core/clock.dart';
import 'package:driver/core/http_api.dart';
import 'package:driver/core/session_store.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:integration_test/integration_test.dart';

import '../test/helpers/d1_robot.dart';

/// D1 in real mode (spec "App-level integration test"): the app on a device
/// or emulator against the Go backend. Sign up with code 123456, add every
/// item through the fake image picker, set the vehicle and day off, submit;
/// the test then calls the admin endpoints to verify, approve and confirm.
/// The app must show "You're approved", the waiting state after "I've
/// paid", then the "You're live" banner on Requests on reopening.
/// Skipped unless built with API_BASE_URL.
///
///   # backend: FIXED_OTP=123456, spec seed (admin 9000000001)
///   flutter test integration_test/d1_e2e_test.dart \
///     --dart-define=API_BASE_URL=http://10.0.2.2:8080 [--dart-define=ADMIN_PHONE=…]
const apiBaseUrl = String.fromEnvironment('API_BASE_URL');
const adminPhone = String.fromEnvironment('ADMIN_PHONE', defaultValue: '9000000001');

/// The eleven items the admin reviews.
const reviewItems = [
  'profile_photo', 'id_proof', 'licence', 'rc', 'insurance', 'permit', 'upi',
  'vehicle', 'vehicle_front', 'vehicle_back', 'vehicle_interior',
];

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  final rnd = Random();

  Future<Map<String, dynamic>?> call(String method, String path, {Object? body, String? token}) async {
    final req = http.Request(method, Uri.parse('$apiBaseUrl$path'))
      ..headers.addAll({'content-type': 'application/json', if (token != null) 'authorization': 'Bearer $token'});
    if (body != null) req.body = jsonEncode(body);
    final res = await http.Response.fromStream(await req.send());
    expect(res.statusCode, inInclusiveRange(200, 299), reason: '$method $path: ${res.body}');
    return res.body.isEmpty ? null : jsonDecode(res.body) as Map<String, dynamic>;
  }

  Future<String> adminToken() async {
    await call('POST', '/v1/auth/otp', body: {'phone': adminPhone});
    return (await call('POST', '/v1/auth/verify', body: {'phone': adminPhone, 'code': '123456'}))!['token'] as String;
  }

  testWidgets('D1 e2e: sign up to "You\'re live" against the Go backend', (tester) async {
    final api = HttpDriverApi(baseUrl: apiBaseUrl, store: MemorySessionStore());
    const clock = SystemClock();
    final h = D1Harness(api, clock, TestImageSource(), demoMode: false);
    await tester.pumpWidget(DriverApp(api: api, clock: clock, imageSource: h.images, demoMode: false));
    final robot = D1Robot(tester, h);
    await robot.settle();

    final phone = '98${rnd.nextInt(90000000) + 10000000}';
    final plate = 'ka ${rnd.nextInt(90) + 10} zx ${rnd.nextInt(9000) + 1000}';
    final admin = await robot.call(adminToken);
    Future<String> driverId() async => (await robot.call(() => api.me())).id;

    await d1HappyPath(
      robot,
      phone: phone,
      plate: plate,
      demoAmounts: false,
      approve: () async {
        final id = await driverId();
        await robot.call(() async {
          for (final item in reviewItems) {
            await call('POST', '/v1/admin/drivers/$id/review-item', token: admin, body: {'item': item, 'state': 'verified'});
          }
          await call('POST', '/v1/admin/drivers/$id/approve', token: admin);
        });
      },
      confirmPayment: () async {
        final id = await driverId();
        await robot.call(() async {
          final pending = await call('GET', '/v1/admin/pending', token: admin);
          final payment = (pending!['payments'] as List).cast<Map<String, dynamic>>().firstWhere((p) => p['driver_id'] == id);
          await call('POST', '/v1/admin/payments/${payment['id']}/confirm', token: admin);
        });
      },
    );
  }, skip: apiBaseUrl.isEmpty);
}
