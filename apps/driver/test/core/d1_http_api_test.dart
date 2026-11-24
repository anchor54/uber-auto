import 'dart:convert';

import 'package:driver/core/http_api.dart';
import 'package:driver/core/models.dart';
import 'package:driver/core/session_store.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import '../helpers/d1_robot.dart';

/// HttpDriverApi against canned responses in the shapes of backend/API.md
/// ("Driver sign-up and verification (D1)"). The real backend runs in
/// test/contract/d1_driver_api_contract_test.dart.
void main() {
  late List<http.Request> sent;
  late MemorySessionStore store;

  HttpDriverApi apiWith(http.Response Function(http.Request r) respond) {
    sent = [];
    return HttpDriverApi(
      baseUrl: 'http://api.test',
      store: store,
      client: MockClient((r) async {
        sent.add(r);
        return respond(r);
      }),
    );
  }

  http.Response json(Object? body, [int status = 200]) => http.Response.bytes(
      body == null ? const [] : utf8.encode(jsonEncode(body)), status,
      headers: {'content-type': 'application/json; charset=utf-8'});

  Map<String, Object?> bodyOf(http.Request r) => jsonDecode(r.body) as Map<String, Object?>;

  Matcher apiError(String code) => throwsA(isA<ApiException>().having((e) => e.code, 'code', code));

  Map<String, Object?> item(String kind, String label, String state, {String? reason, String? value}) => {
        'kind': kind,
        'label': label,
        'state': state,
        'reject_reason': reason,
        'storage_path': state == 'missing' || kind == 'upi' ? null : 'driver-docs/d1/$kind',
        'uploaded_at': state == 'missing' ? null : '2026-11-21T04:40:00Z',
        'reviewed_at': state == 'verified' || state == 'rejected' ? '2026-11-21T06:00:00Z' : null,
        if (value != null) 'value': value,
      };

  Map<String, Object?> paymentJson({String status = 'claimed', String? reason}) => {
        'id': 'p1', 'driver_id': 'd1', 'driver_name': 'Ravi Kumar',
        'period_start': '2026-11-21', 'period_end': '2026-11-30', 'amount': 400,
        'reference': 'DRV-20417', 'status': status, 'claimed_at': '2026-11-21T05:00:00Z',
        'reviewed_at': status == 'claimed' ? null : '2026-11-21T06:00:00Z', 'reject_reason': reason,
      };

  Map<String, Object?> vehicleJson({String state = 'verified', String? reason}) => {
        'type': 'auto', 'cab_class': null, 'ride_type': 'auto', 'make_model': 'Bajaj RE',
        'year': 2021, 'plate': 'KA 05 AB 1234', 'colour': 'Green', 'ac': false,
        'cng_or_electric': true, 'review_state': state, 'reject_reason': reason,
      };

  /// API.md's driver.get example: changes needed, the licence rejected.
  Map<String, Object?> driverJson({String status = 'changes_needed', Object? payment, Object? vehicle, String? rejectReason}) => {
        'id': 'd1', 'user_id': 'u1', 'name': 'Ravi Kumar', 'phone': '9800000201', 'city': 'blr',
        'status': status, 'reject_reason': rejectReason, 'reference': 'DRV-20417',
        'upi_id': 'ravi@okaxis', 'day_off': 7, 'day_off_name': 'SUN', 'day_off_set': true,
        'subscription_until': status == 'live' ? '2026-11-30' : null, 'eligible': status == 'live',
        'rating': 5, 'version': 3,
        'submitted_at': '2026-11-21T05:00:00Z', 'approved_at': null, 'created_at': '2026-11-21T04:30:00Z',
        'items': [
          item('profile_photo', 'Profile photo', 'verified'),
          item('id_proof', 'ID proof', 'verified'),
          item('licence', 'Driving licence', 'rejected', reason: 'photo blurred'),
          item('rc', 'Vehicle RC', 'verified'),
          item('insurance', 'Insurance', 'verified'),
          item('permit', 'Permit', 'verified'),
          item('upi', 'UPI ID', 'verified', value: 'ravi@okaxis'),
          item('vehicle', 'Vehicle', 'verified'),
          item('vehicle_front', 'Front', 'verified'),
          item('vehicle_back', 'Back', 'verified'),
          item('vehicle_interior', 'Interior', 'verified'),
        ],
        'documents_added': 7, 'documents_verified': 6, 'missing': ['licence'],
        'vehicle': vehicle ?? vehicleJson(),
        'plan': {'vehicle_type': 'auto', 'cab_class': '', 'ride_type': 'auto', 'label': 'Auto plan', 'monthly_price': 1199},
        'latest_payment': payment,
      };

  setUp(() => store = MemorySessionStore()..token = 'tok');

  test('driver.register: POST /v1/driver/signup {name, city, terms_accepted}', () async {
    final api = apiWith((_) => json({...driverJson(status: 'signing_up'), 'documents_added': 0, 'missing': documentKinds}, 201));
    final d = await api.driverSignup(name: 'Ravi Kumar', city: 'blr', termsAccepted: true);
    expect(sent.single.method, 'POST');
    expect(sent.single.url.path, '/v1/driver/signup');
    expect(sent.single.headers['authorization'], 'Bearer tok');
    expect(bodyOf(sent.single), {'name': 'Ravi Kumar', 'city': 'blr', 'terms_accepted': true});
    expect(d.status, DriverStatus.signingUp);
    expect(d.reference, 'DRV-20417');
  });

  test('driver.register: 409 phone_in_use_as_rider and already_registered; 422 terms_not_accepted', () async {
    for (final (status, code) in [(409, 'phone_in_use_as_rider'), (409, 'already_registered'), (422, 'terms_not_accepted')]) {
      final api = apiWith((_) => json({'code': code, 'message': 'No.', 'state': null}, status));
      await expectLater(api.driverSignup(name: 'Ravi Kumar', city: 'blr', termsAccepted: true), apiError(code));
    }
  });

  test('driver.get: GET /v1/driver maps status, items, reasons, vehicle, day off', () async {
    final api = apiWith((_) => json(driverJson()));
    final d = await api.me();
    expect(sent.single.method, 'GET');
    expect(sent.single.url.path, '/v1/driver');
    expect(d.id, 'd1');
    expect(d.name, 'Ravi Kumar');
    expect(d.phone, '9800000201');
    expect(d.city, 'blr');
    expect(d.status, DriverStatus.changesNeeded);
    expect(d.reference, 'DRV-20417');
    expect(d.upiId, 'ravi@okaxis');
    expect(d.dayOff, 7);
    expect(d.dayOffSet, isTrue);
    expect(d.subscriptionUntil, isNull);
    expect(d.eligible, isFalse);
    expect(d.documentsAdded, 7);
    expect(d.documentsVerified, 6);
    expect(d.missing, ['licence']);
    final licence = d.items.singleWhere((i) => i.kind == 'licence');
    expect(licence.label, 'Driving licence');
    expect(licence.state, ItemState.rejected);
    expect(licence.rejectReason, 'photo blurred');
    expect(licence.storagePath, 'driver-docs/d1/licence');
    expect(d.items.singleWhere((i) => i.kind == 'upi').value, 'ravi@okaxis');
    expect(d.items.singleWhere((i) => i.kind == 'rc').state, ItemState.verified);
    final v = d.vehicle!;
    expect(v.type, 'auto');
    expect(v.cabClass, isNull);
    expect(v.makeModel, 'Bajaj RE');
    expect(v.year, 2021);
    expect(v.plate, 'KA 05 AB 1234');
    expect(v.colour, 'Green');
    expect(v.ac, isFalse);
    expect(v.cngOrElectric, isTrue);
    expect(v.reviewState, ItemState.verified);
    expect(d.latestPayment, isNull);
  });

  test('driver.get: every status maps', () async {
    const statuses = {
      'signing_up': DriverStatus.signingUp,
      'submitted': DriverStatus.submitted,
      'changes_needed': DriverStatus.changesNeeded,
      'approved': DriverStatus.approved,
      'live': DriverStatus.live,
      'paused': DriverStatus.paused,
      'rejected': DriverStatus.rejected,
    };
    for (final MapEntry(key: wire, value: status) in statuses.entries) {
      final api = apiWith((_) => json(driverJson(status: wire)));
      expect((await api.me()).status, status, reason: wire);
    }
  });

  test('driver.get: a missing item, no vehicle yet, no day off', () async {
    final j = driverJson(status: 'signing_up')
      ..['vehicle'] = null
      ..['day_off'] = null
      ..['day_off_name'] = null
      ..['day_off_set'] = false
      ..['items'] = [item('licence', 'Driving licence', 'missing')];
    final api = apiWith((_) => json(j));
    final d = await api.me();
    expect(d.vehicle, isNull);
    expect(d.dayOff, isNull);
    expect(d.dayOffSet, isFalse);
    expect(d.items.single.state, ItemState.missing);
  });

  test('driver.get: rejected with a reason; live with subscription_until and the latest payment', () async {
    var api = apiWith((_) => json(driverJson(status: 'rejected', rejectReason: 'Licence expired')));
    var d = await api.me();
    expect(d.status, DriverStatus.rejected);
    expect(d.rejectReason, 'Licence expired');

    api = apiWith((_) => json(driverJson(status: 'live', payment: paymentJson(status: 'confirmed'))));
    d = await api.me();
    expect(d.status, DriverStatus.live);
    expect(d.subscriptionUntil, DateTime(2026, 11, 30));
    expect(d.eligible, isTrue);
    expect(d.latestPayment!.status, PaymentStatus.confirmed);
  });

  test('driver.get before signup: 404 not_registered', () async {
    final api = apiWith((_) => json({'code': 'not_registered', 'message': 'Sign up first.', 'state': null}, 404));
    await expectLater(api.me(), apiError('not_registered'));
  });

  test('driver.upload_url: POST /v1/driver/upload-url {kind, content_type}', () async {
    final api = apiWith((_) => json({
          'kind': 'licence',
          'upload_url': 'http://api.test/storage/driver-docs/d1/licence?op=put&exp=1&ct=image%2Fjpeg&sig=abc',
          'method': 'PUT',
          'storage_path': 'driver-docs/d1/licence',
          'content_type': 'image/jpeg',
          'max_bytes': 5242880,
          'expires_at': '2026-11-21T04:50:00Z',
        }));
    final t = await api.uploadUrl('licence', 'image/jpeg');
    expect(sent.single.method, 'POST');
    expect(sent.single.url.path, '/v1/driver/upload-url');
    expect(bodyOf(sent.single), {'kind': 'licence', 'content_type': 'image/jpeg'});
    expect(t.kind, 'licence');
    expect(t.uploadUrl, startsWith('http://api.test/storage/driver-docs/d1/licence'));
    expect(t.method, 'PUT');
    expect(t.storagePath, 'driver-docs/d1/licence');
    expect(t.contentType, 'image/jpeg');
    expect(t.maxBytes, 5242880);
    expect(t.expiresAt.toUtc(), DateTime.utc(2026, 11, 21, 4, 50));
  });

  group('uploadDocument', () {
    const signed = 'http://api.test/storage/driver-docs/d1/licence?op=put&exp=1&ct=image%2Fjpeg&sig=abc';

    http.Response route(http.Request r, {int storeStatus = 200, String? storeCode}) {
      if (r.url.path == '/v1/driver/upload-url') {
        return json({
          'kind': 'licence', 'upload_url': signed, 'method': 'PUT', 'storage_path': 'driver-docs/d1/licence',
          'content_type': 'image/jpeg', 'max_bytes': 5242880, 'expires_at': '2026-11-21T04:50:00Z',
        });
      }
      if (r.url.path.startsWith('/storage/')) {
        return storeCode == null ? json({'path': 'driver-docs/d1/licence'}, storeStatus) : json({'code': storeCode, 'message': 'No.', 'state': null}, storeStatus);
      }
      if (r.url.path == '/v1/driver/documents/licence') return json(item('licence', 'Driving licence', 'uploaded'));
      return json({'code': 'not_found', 'message': 'No.', 'state': null}, 404);
    }

    test('upload-url, PUT the bytes to the signed link, then PUT /v1/driver/documents/{kind}', () async {
      final bytes = jpegBytes(300);
      final api = apiWith(route);
      final it = await api.uploadDocument('licence', bytes, 'image/jpeg');
      expect(sent.map((r) => '${r.method} ${r.url.path}'), [
        'POST /v1/driver/upload-url',
        'PUT /storage/driver-docs/d1/licence',
        'PUT /v1/driver/documents/licence',
      ]);
      final put = sent[1];
      expect(put.url.toString(), signed, reason: 'the signed query is kept as issued');
      expect(put.headers['content-type'], startsWith('image/jpeg'));
      expect(put.bodyBytes, bytes);
      expect(bodyOf(sent[2]), {'storage_path': 'driver-docs/d1/licence'});
      expect(it.kind, 'licence');
      expect(it.state, ItemState.uploaded);
    });

    test('the store refuses the file: an ApiException, and the item is not marked uploaded', () async {
      final api = apiWith((r) => route(r, storeStatus: 413, storeCode: 'file_too_large'));
      await expectLater(api.uploadDocument('licence', jpegBytes(), 'image/jpeg'), throwsA(isA<ApiException>()));
      expect(sent.map((r) => r.url.path), isNot(contains('/v1/driver/documents/licence')));
    });

    test('409 locked from upload-url', () async {
      final api = apiWith((_) => json({'code': 'locked', 'message': 'Locked.', 'state': driverJson(status: 'submitted')}, 409));
      await expectLater(api.uploadDocument('licence', jpegBytes(), 'image/jpeg'), apiError('locked'));
      expect(sent, hasLength(1));
    });

    test('403 not_your_path from documents/{kind}', () async {
      final api = apiWith((r) => r.url.path == '/v1/driver/documents/licence'
          ? json({'code': 'not_your_path', 'message': 'No.', 'state': null}, 403)
          : route(r));
      await expectLater(api.uploadDocument('licence', jpegBytes(), 'image/jpeg'), apiError('not_your_path'));
    });
  });

  test('driver.set_upi: PUT /v1/driver/upi {upi_id}; 422 bad_upi', () async {
    var api = apiWith((_) => json(item('upi', 'UPI ID', 'uploaded', value: 'ravi@okaxis')));
    final it = await api.setUpi('ravi@okaxis');
    expect(sent.single.method, 'PUT');
    expect(sent.single.url.path, '/v1/driver/upi');
    expect(bodyOf(sent.single), {'upi_id': 'ravi@okaxis'});
    expect(it.kind, 'upi');
    expect(it.value, 'ravi@okaxis');
    api = apiWith((_) => json({'code': 'bad_upi', 'message': 'Enter a UPI ID like name@bank.', 'state': null}, 422));
    await expectLater(api.setUpi('ravi'), apiError('bad_upi'));
  });

  test('driver.set_vehicle: PUT /v1/driver/vehicle with every field', () async {
    final api = apiWith((_) => json(vehicleJson(state: 'uploaded')));
    final v = await api.setVehicle(
        type: 'auto', cabClass: null, makeModel: 'Bajaj RE', year: 2021, plate: 'ka 05 ab 1234', colour: 'Green', ac: false, cngOrElectric: true);
    expect(sent.single.method, 'PUT');
    expect(sent.single.url.path, '/v1/driver/vehicle');
    final b = bodyOf(sent.single);
    expect(b['vehicle_type'] ?? b['type'], 'auto');
    expect(b['cab_class'], isNull);
    expect(b['make_model'], 'Bajaj RE');
    expect(b['year'], 2021);
    expect(b['plate'], 'ka 05 ab 1234');
    expect(b['colour'], 'Green');
    expect(b['ac'], false);
    expect(b['cng_or_electric'], true);
    expect(v.plate, 'KA 05 AB 1234');
    expect(v.reviewState, ItemState.uploaded);
  });

  test('driver.set_vehicle: a cab sends its class; 409 plate_in_use', () async {
    var api = apiWith((_) => json({...vehicleJson(state: 'uploaded'), 'type': 'cab', 'cab_class': 'sedan', 'ride_type': 'cab_sedan', 'ac': true}));
    final v = await api.setVehicle(
        type: 'cab', cabClass: 'sedan', makeModel: 'Maruti Dzire', year: 2022, plate: 'KA01MN4321', colour: 'White', ac: true, cngOrElectric: false);
    expect(bodyOf(sent.single)['cab_class'], 'sedan');
    expect(v.cabClass, 'sedan');
    api = apiWith((_) => json({'code': 'plate_in_use', 'message': 'This plate is already registered.', 'state': null}, 409));
    await expectLater(
      api.setVehicle(type: 'auto', cabClass: null, makeModel: 'Bajaj RE', year: 2021, plate: 'KA05AB1234', colour: 'Green', ac: false, cngOrElectric: true),
      apiError('plate_in_use'),
    );
  });

  test('driver.set_day_off: PUT /v1/driver/day-off {day_off: 1..7 | null}', () async {
    final api = apiWith((_) => json(driverJson(status: 'signing_up')));
    await api.setDayOff(7);
    await api.setDayOff(null);
    expect(sent.map((r) => '${r.method} ${r.url.path}'), ['PUT /v1/driver/day-off', 'PUT /v1/driver/day-off']);
    expect(bodyOf(sent[0]), {'day_off': 7});
    expect(bodyOf(sent[1]), {'day_off': null});
  });

  test('driver.submit: POST /v1/driver/submit → submitted', () async {
    final api = apiWith((_) => json(driverJson(status: 'submitted')));
    final d = await api.submitForReview();
    expect(sent.single.method, 'POST');
    expect(sent.single.url.path, '/v1/driver/submit');
    expect(d.status, DriverStatus.submitted);
  });

  test('driver.submit: 409 incomplete keeps the driver (with missing) as state', () async {
    final api = apiWith((_) => json({
          'code': 'incomplete',
          'message': 'Some items are missing.',
          'missing': ['insurance', 'permit'],
          'state': {...driverJson(status: 'signing_up'), 'missing': ['insurance', 'permit']},
        }, 409));
    await expectLater(
      api.submitForReview(),
      throwsA(isA<ApiException>().having((e) => e.code, 'code', 'incomplete').having((e) => e.state, 'state', isNotNull)),
    );
  });

  test('plans.get: GET /v1/driver/plan maps price, amount due, period and reference', () async {
    final api = apiWith((_) => json({
          'plan': {'vehicle_type': 'auto', 'cab_class': '', 'label': 'Auto plan', 'monthly_price': 1199},
          'monthly_price': 1199, 'amount_due': 400, 'period_start': '2026-11-21', 'period_end': '2026-11-30',
          'days_left': 10, 'days_in_month': 30, 'reference': 'DRV-20417', 'payee_upi': 'commuteride@icici',
          'status': 'approved', 'latest_payment': null,
        }));
    final q = await api.plan();
    expect(sent.single.method, 'GET');
    expect(sent.single.url.path, '/v1/driver/plan');
    expect(q.label, 'Auto plan');
    expect(q.vehicleType, 'auto');
    expect(q.monthlyPrice, 1199);
    expect(q.amountDue, 400);
    expect(q.periodStart, DateTime(2026, 11, 21));
    expect(q.periodEnd, DateTime(2026, 11, 30));
    expect(q.daysLeft, 10);
    expect(q.daysInMonth, 30);
    expect(q.reference, 'DRV-20417');
    expect(q.payeeUpi, 'commuteride@icici');
    expect(q.latestPayment, isNull);
  });

  test('plans.get: 409 not_approved', () async {
    final api = apiWith((_) => json({'code': 'not_approved', 'message': 'Not approved yet.', 'state': driverJson(status: 'submitted')}, 409));
    await expectLater(api.plan(), apiError('not_approved'));
  });

  test("driver.claim_payment (I've paid): POST /v1/driver/payments → claimed", () async {
    final api = apiWith((_) => json(paymentJson(), 201));
    final p = await api.claimPayment();
    expect(sent.single.method, 'POST');
    expect(sent.single.url.path, '/v1/driver/payments');
    expect(p.id, 'p1');
    expect(p.status, PaymentStatus.claimed);
    expect(p.amount, 400);
    expect(p.periodStart, DateTime(2026, 11, 21));
    expect(p.periodEnd, DateTime(2026, 11, 30));
    expect(p.reference, 'DRV-20417');
    expect(p.claimedAt!.toUtc(), DateTime.utc(2026, 11, 21, 5));
  });

  test('driver.claim_payment with a period sends period_start', () async {
    final api = apiWith((_) => json(paymentJson(), 201));
    await api.claimPayment(periodStart: DateTime(2026, 11, 21));
    expect(bodyOf(sent.single)['period_start'], '2026-11-21');
  });

  test('driver.claim_payment: 409 already_claimed; a rejected payment maps its reason', () async {
    var api = apiWith((_) => json({'code': 'already_claimed', 'message': 'Already claimed.', 'state': paymentJson()}, 409));
    await expectLater(api.claimPayment(), apiError('already_claimed'));
    api = apiWith((_) => json(driverJson(status: 'approved', payment: paymentJson(status: 'rejected', reason: 'not received'))));
    final d = await api.me();
    expect(d.latestPayment!.status, PaymentStatus.rejected);
    expect(d.latestPayment!.rejectReason, 'not received');
  });

  test('409 locked carries the driver as state', () async {
    final api = apiWith((_) => json({'code': 'locked', 'message': 'Locked.', 'state': driverJson(status: 'submitted')}, 409));
    await expectLater(
      api.setDayOff(3),
      throwsA(isA<ApiException>().having((e) => e.code, 'code', 'locked').having((e) => e.state, 'state', isNotNull)),
    );
  });
}
