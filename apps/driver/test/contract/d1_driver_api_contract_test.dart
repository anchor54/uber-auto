@Tags(['contract'])
library;

import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:driver/core/api.dart';
import 'package:driver/core/fake_api.dart';
import 'package:driver/core/http_api.dart';
import 'package:driver/core/models.dart';
import 'package:driver/core/session_store.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import '../helpers/d1_robot.dart' show jpegBytes, d1Now, documentKinds, fileDocumentKinds, vehiclePhotoKinds;

/// One script of D1 DriverApi calls (sign-up, items, vehicle, day off,
/// submit, plan, I've paid) with the admin's review in between, run against
/// the demo backend and, when DRIVER_API_URL is set, the real backend
/// (backend/API.md, "Driver sign-up and verification (D1)" and "Admin (D1)").
///
///   DRIVER_API_URL=http://localhost:8080 flutter test --tags contract
///
/// The backend must run with FIXED_OTP=123456 and the spec seed (admin
/// 9000000001; ADMIN_PHONE overrides). Every test signs up a fresh driver
/// with a random number and plate, so runs don't collide.
void main() {
  HttpOverrides.global = null;
  final url = Platform.environment['DRIVER_API_URL'] ?? '';
  final adminPhone = Platform.environment['ADMIN_PHONE'] ?? '9000000001';

  final backends = <String, _Backend Function()>{
    'demo backend': _DemoBackend.new,
    if (url.isNotEmpty) 'real backend ($url)': () => _RealBackend(url, adminPhone),
  };

  Matcher apiError(String code) => throwsA(isA<ApiException>().having((e) => e.code, 'code', code));

  for (final MapEntry(key: name, value: make) in backends.entries) {
    group('D1 on $name', () {
      late _Backend b;
      late DriverApi api;

      setUp(() async {
        b = make();
        api = b.api;
        await api.sendOtp(b.phone);
        await api.verifyOtp(b.phone, FakeDriverApi.demoOtp);
      });

      Future<void> signUp() => api.driverSignup(name: 'Ravi Kumar', city: 'blr', termsAccepted: true);

      Future<void> documents({Iterable<String> kinds = fileDocumentKinds}) async {
        for (final k in kinds) {
          await api.uploadDocument(k, jpegBytes(), 'image/jpeg');
        }
        await api.setUpi('ravi@okaxis');
      }

      Future<void> vehicle() async {
        await api.setVehicle(
            type: 'auto', cabClass: null, makeModel: 'Bajaj RE', year: 2021, plate: b.plate.toLowerCase(), colour: 'Green', ac: false, cngOrElectric: true);
        for (final k in vehiclePhotoKinds) {
          await api.uploadDocument(k, jpegBytes(), 'image/jpeg');
        }
      }

      Future<void> submitted() async {
        await signUp();
        await documents();
        await vehicle();
        await api.setDayOff(7);
        await api.submitForReview();
      }

      test('signup: signing_up with a DRV-nnnnn reference, every document missing', () async {
        await signUp();
        final d = await api.me();
        expect(d.status, DriverStatus.signingUp);
        expect(d.name, 'Ravi Kumar');
        expect(d.reference, matches(RegExp(r'^DRV-\d{5}$')));
        expect(d.documentsAdded, 0);
        expect(d.missing, containsAll(documentKinds));
        await expectLater(signUp(), apiError('already_registered'));
      });

      test('items: upload_url under driver-docs/<id>/; uploaded items count; bad type, bad UPI refused', () async {
        await signUp();
        final d = await api.me();
        final t = await api.uploadUrl('licence', 'image/jpeg');
        expect(t.storagePath, 'driver-docs/${d.id}/licence');
        expect(t.maxBytes, 5242880);
        await expectLater(api.uploadUrl('licence', 'image/gif'), apiError('bad_content_type'));
        await expectLater(api.setUpi('ravi'), apiError('bad_upi'));
        await documents();
        final after = await api.me();
        expect(after.documentsAdded, 7);
        expect(after.items.singleWhere((i) => i.kind == 'licence').state, ItemState.uploaded);
        expect(after.upiId, 'ravi@okaxis');
      });

      test('vehicle and day off: the plate is upper-cased; day off ISO or none', () async {
        await signUp();
        await vehicle();
        await api.setDayOff(null);
        var d = await api.me();
        expect(d.vehicle!.plate, b.plate.toUpperCase());
        expect(d.vehicle!.type, 'auto');
        expect(d.dayOff, isNull);
        expect(d.dayOffSet, isTrue);
        await api.setDayOff(7);
        d = await api.me();
        expect(d.dayOff, 7);
      });

      test('submit with two documents missing: 409 incomplete; still signing_up', () async {
        await signUp();
        await documents(kinds: ['profile_photo', 'id_proof', 'licence', 'rc']);
        await vehicle();
        await api.setDayOff(7);
        await expectLater(api.submitForReview(), apiError('incomplete'));
        final d = await api.me();
        expect(d.status, DriverStatus.signingUp);
        expect(d.missing, containsAll(['insurance', 'permit']));
      });

      test('submitted: locked; plan and claim not_approved', () async {
        await submitted();
        expect((await api.me()).status, DriverStatus.submitted);
        await expectLater(api.uploadUrl('licence', 'image/jpeg'), apiError('locked'));
        await expectLater(api.setDayOff(3), apiError('locked'));
        await expectLater(api.plan(), apiError('not_approved'));
        await expectLater(api.claimPayment(), apiError('not_approved'));
      });

      test('a rejected item: changes_needed, only it can be replaced, submit again', () async {
        await submitted();
        await b.requestChanges('licence', 'photo blurred');
        var d = await api.me();
        expect(d.status, DriverStatus.changesNeeded);
        final licence = d.items.singleWhere((i) => i.kind == 'licence');
        expect(licence.state, ItemState.rejected);
        expect(licence.rejectReason, 'photo blurred');
        await expectLater(api.uploadUrl('rc', 'image/jpeg'), apiError('locked'));
        await api.uploadDocument('licence', jpegBytes(), 'image/jpeg');
        await api.submitForReview();
        d = await api.me();
        expect(d.status, DriverStatus.submitted);
        expect(d.items.singleWhere((i) => i.kind == 'rc').state, ItemState.verified);
      });

      test('approved → plan → claim → confirmed: live until the period ends', () async {
        await submitted();
        await b.approve();
        expect((await api.me()).status, DriverStatus.approved);
        final q = await api.plan();
        expect(q.vehicleType, 'auto');
        expect(q.monthlyPrice, greaterThan(0));
        expect(q.amountDue, (q.monthlyPrice * q.daysLeft / q.daysInMonth).ceil());
        expect(q.reference, (await api.me()).reference);
        final p = await api.claimPayment();
        expect(p.status, PaymentStatus.claimed);
        expect(p.amount, q.amountDue);
        await expectLater(api.claimPayment(), apiError('already_claimed'));
        await b.confirmPayment();
        final d = await api.me();
        expect(d.status, DriverStatus.live);
        expect(d.subscriptionUntil, q.periodEnd);
        expect(d.latestPayment!.status, PaymentStatus.confirmed);
        await api.offers();
      });

      test('a rejected claim can be claimed again', () async {
        await submitted();
        await b.approve();
        await api.claimPayment();
        await b.rejectPayment('not received');
        var d = await api.me();
        expect(d.latestPayment!.status, PaymentStatus.rejected);
        expect(d.latestPayment!.rejectReason, 'not received');
        await api.claimPayment();
        await b.confirmPayment();
        d = await api.me();
        expect(d.status, DriverStatus.live);
      });

      test('rejected for good: the reason; no plan', () async {
        await submitted();
        await b.reject('Licence not valid for commercial use');
        final d = await api.me();
        expect(d.status, DriverStatus.rejected);
        expect(d.rejectReason, 'Licence not valid for commercial use');
        await expectLater(api.plan(), apiError('not_approved'));
      });
    });
  }
}

abstract class _Backend {
  DriverApi get api;

  /// A number no driver has used.
  String get phone;

  /// A plate no driver has used.
  String get plate;

  Future<void> approve();
  Future<void> requestChanges(String item, String reason);
  Future<void> reject(String reason);
  Future<void> confirmPayment();
  Future<void> rejectPayment(String reason);
}

class _DemoBackend implements _Backend {
  final fake = FakeDriverApi(now: () => d1Now, seed: false);

  @override
  DriverApi get api => fake;
  @override
  String get phone => '9800000201';
  @override
  String get plate => 'KA 05 CD 5678';

  @override
  Future<void> approve() async => fake.adminApprove();
  @override
  Future<void> requestChanges(String item, String reason) async => fake.adminRequestChanges(item, reason);
  @override
  Future<void> reject(String reason) async => fake.adminReject(reason);
  @override
  Future<void> confirmPayment() async => fake.adminConfirmPayment();
  @override
  Future<void> rejectPayment(String reason) async => fake.adminRejectPayment(reason);
}

class _RealBackend implements _Backend {
  _RealBackend(this.url, this.adminPhone)
      : api = HttpDriverApi(baseUrl: url, store: MemorySessionStore()),
        phone = '98${(_rnd.nextInt(90000000) + 10000000)}',
        plate = 'KA ${(_rnd.nextInt(90) + 10)} ZX ${(_rnd.nextInt(9000) + 1000)}';

  static final _rnd = Random();

  /// Every item the admin reviews (API.md: "all eleven").
  static const items = [
    'profile_photo', 'id_proof', 'licence', 'rc', 'insurance', 'permit', 'upi',
    'vehicle', 'vehicle_front', 'vehicle_back', 'vehicle_interior',
  ];

  final String url;
  final String adminPhone;
  @override
  final DriverApi api;
  @override
  final String phone;
  @override
  final String plate;
  String? _adminToken;

  Future<Map<String, dynamic>?> _call(String method, String path, {Object? body, String? token}) async {
    final req = http.Request(method, Uri.parse('$url$path'))
      ..headers.addAll({'content-type': 'application/json', if (token != null) 'authorization': 'Bearer $token'});
    if (body != null) req.body = jsonEncode(body);
    final res = await http.Response.fromStream(await req.send());
    expect(res.statusCode, inInclusiveRange(200, 299), reason: '$method $path: ${res.body}');
    return res.body.isEmpty ? null : jsonDecode(res.body) as Map<String, dynamic>;
  }

  Future<String> _admin() async {
    if (_adminToken != null) return _adminToken!;
    await _call('POST', '/v1/auth/otp', body: {'phone': adminPhone});
    final res = await _call('POST', '/v1/auth/verify', body: {'phone': adminPhone, 'code': '123456'});
    return _adminToken = res!['token'] as String;
  }

  Future<String> _driverId() async => (await api.me()).id;

  Future<void> _review({String? rejected, String? reason}) async {
    final token = await _admin();
    final id = await _driverId();
    for (final item in items) {
      await _call('POST', '/v1/admin/drivers/$id/review-item',
          token: token,
          body: item == rejected ? {'item': item, 'state': 'rejected', 'reason': reason} : {'item': item, 'state': 'verified'});
    }
  }

  @override
  Future<void> approve() async {
    await _review();
    await _call('POST', '/v1/admin/drivers/${await _driverId()}/approve', token: await _admin());
  }

  @override
  Future<void> requestChanges(String item, String reason) => _review(rejected: item, reason: reason);

  @override
  Future<void> reject(String reason) async {
    await _call('POST', '/v1/admin/drivers/${await _driverId()}/reject', token: await _admin(), body: {'reason': reason});
  }

  Future<String> _claimedPayment() async {
    final id = await _driverId();
    final pending = await _call('GET', '/v1/admin/pending', token: await _admin());
    final payments = (pending!['payments'] as List).cast<Map<String, dynamic>>();
    return payments.firstWhere((p) => p['driver_id'] == id)['id'] as String;
  }

  @override
  Future<void> confirmPayment() async {
    await _call('POST', '/v1/admin/payments/${await _claimedPayment()}/confirm', token: await _admin());
  }

  @override
  Future<void> rejectPayment(String reason) async {
    await _call('POST', '/v1/admin/payments/${await _claimedPayment()}/reject', token: await _admin(), body: {'reason': reason});
  }
}
