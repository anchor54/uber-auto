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

/// One script of DriverApi calls (offers.list, offer.get, mark/withdraw
/// interest, dismiss, rides.week), run against the demo backend and, when
/// DRIVER_API_URL is set, against the real backend (backend/API.md).
///
///   DRIVER_API_URL=http://localhost:8080 flutter test --tags contract
///
/// The backend must run with FIXED_OTP=123456, ENABLE_DEV_ROUTES=true and the
/// spec seed (live auto driver Anil, 9000000203). A fresh rider creates each
/// request over the rider endpoints. Set SWEEP_TOKEN if the sweep needs one.
void main() {
  HttpOverrides.global = null;
  final url = Platform.environment['DRIVER_API_URL'] ?? '';
  final sweepToken = Platform.environment['SWEEP_TOKEN'] ?? '';
  final driverPhone = Platform.environment['DRIVER_PHONE'] ?? '9000000203';

  final backends = <String, _Backend Function()>{
    'demo backend': _DemoBackend.new,
    if (url.isNotEmpty) 'real backend ($url)': () => _RealBackend(url, sweepToken),
  };

  for (final MapEntry(key: name, value: make) in backends.entries) {
    group(name, () {
      late _Backend b;

      setUpAll(() async {
        b = make();
        final phone = b is _DemoBackend ? FakeDriverApi.demoPhone : driverPhone;
        await b.api.sendOtp(phone);
        final me = await b.api.verifyOtp(phone, FakeDriverApi.demoOtp);
        expect(me.name, isNotEmpty);
      });

      test('a wrong code is invalid_code', () async {
        final other = make().api;
        final phone = b is _DemoBackend ? FakeDriverApi.demoPhone : driverPhone;
        await other.sendOtp(phone);
        await expectLater(other.verifyOtp(phone, '000000'),
            throwsA(isA<ApiException>().having((e) => e.code, 'code', 'invalid_code')));
      });

      test('offers.list shows a new request with its terms; offer.get agrees', () async {
        final id = await b.newRequest();
        final o = (await b.api.offers()).offers.firstWhere((x) => x.id == id);
        expect(o.kind, OfferKind.series);
        expect(o.status, OfferStatus.open);
        expect(o.canRespond, isTrue);
        expect(o.interested, isFalse);
        expect(o.round, 1);
        expect(o.expiresAt.difference(o.openedAt), const Duration(minutes: 15));
        expect(o.rider.firstName, isNotEmpty);
        expect(o.rider.name, startsWith(o.rider.firstName));
        expect(o.slots, isNotEmpty);
        expect(o.farePerRide, greaterThan(0));
        expect(o.travelMinutes, greaterThan(0));
        expect(o.startDate.isAfter(DateTime(2026, 1, 1)), isTrue);

        final d = await b.api.offer(id);
        expect(d.id, id);
        expect(d.seriesId, o.seriesId);
        expect(d.farePerRide, o.farePerRide);
        expect(d.slots.length, o.slots.length);
      });

      test('mark and withdraw interest; both repeatable', () async {
        final id = await b.newRequest();
        await b.api.markInterested(id);
        await b.api.markInterested(id);
        expect((await b.api.offer(id)).interested, isTrue);
        await b.api.withdrawInterest(id);
        await b.api.withdrawInterest(id);
        expect((await b.api.offer(id)).interested, isFalse);
      });

      test('Not for me: dismissed requests leave the list', () async {
        final id = await b.newRequest();
        await b.api.dismissOffer(id);
        expect((await b.api.offers()).offers.map((x) => x.id), isNot(contains(id)));
      });

      test('interested when the window closes: picked, a result card, new rides in the week', () async {
        final id = await b.newRequest();
        final o = await b.api.offer(id);
        await b.api.markInterested(id);
        await b.closeRound(o);

        final picked = await b.api.offer(id);
        expect(picked.result, 'picked');
        final list = await b.api.offers();
        expect(list.offers.map((x) => x.id), isNot(contains(id)));
        expect(list.results.where((r) => r.offerId == id).map((r) => r.result), ['picked']);

        final week = await b.api.week(start: o.startDate);
        final mine = week.rides.where((r) => r.seriesId == o.seriesId).toList();
        expect(mine, isNotEmpty);
        expect(mine.every((r) => r.isNew), isTrue);
        expect(mine.map((r) => r.weekday).toSet(), o.slots.map((s) => s.weekday).toSet());
      });

      test('a late tap is 409 offer_closed with the offer as state', () async {
        final id = await b.newRequest();
        final o = await b.api.offer(id);
        await b.passWindow(o);
        await expectLater(
          b.api.markInterested(id),
          throwsA(isA<ApiException>().having((e) => e.code, 'code', 'offer_closed')),
        );
      });

      test('an offer the driver never received is not_found', () async {
        await expectLater(b.api.offer(b.unknownId), throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_found')));
        await expectLater(
            b.api.markInterested(b.unknownId), throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_found')));
      });
    });
  }
}

abstract class _Backend {
  DriverApi get api;

  /// A rider asks for a new regular ride; returns this driver's offer id.
  Future<String> newRequest();

  /// [o]'s window passes and the sweep runs the pick.
  Future<void> closeRound(Offer o);

  /// [o]'s window passes, without the sweep.
  Future<void> passWindow(Offer o);

  String get unknownId;
}

class _DemoBackend implements _Backend {
  _DemoBackend() {
    fake = FakeDriverApi(now: () => _now);
  }

  DateTime _now = DateTime(2026, 11, 19, 10);
  late FakeDriverApi fake;
  final Set<String> _used = {};

  @override
  DriverApi get api => fake;

  /// The demo backend has one seeded request per run of the seed; each test
  /// takes a fresh backend's request when the current one's is used.
  @override
  Future<String> newRequest() async {
    for (final o in (await fake.offers()).offers) {
      if (o.kind == OfferKind.series && _used.add(o.id)) return o.id;
    }
    // Every seeded request is used: start again with a fresh seed.
    _now = _now.add(const Duration(days: 1));
    fake = FakeDriverApi(now: () => _now);
    await fake.verifyOtp(FakeDriverApi.demoPhone, FakeDriverApi.demoOtp);
    return newRequest();
  }

  @override
  Future<void> closeRound(Offer o) async => _now = o.expiresAt.add(const Duration(minutes: 1));

  @override
  Future<void> passWindow(Offer o) async => _now = o.expiresAt.add(const Duration(seconds: 1));

  @override
  String get unknownId => 'of_nope';
}

class _RealBackend implements _Backend {
  _RealBackend(this.url, this.sweepToken) : api = HttpDriverApi(baseUrl: url, store: MemorySessionStore());

  final String url;
  final String sweepToken;
  final _rnd = Random();

  @override
  final DriverApi api;

  @override
  String get unknownId => '00000000-0000-4000-8000-000000000000';

  Future<Map<String, dynamic>?> _post(String path, {Object? body, String? token, Map<String, String>? headers}) async {
    final res = await http.post(
      Uri.parse('$url$path'),
      headers: {'content-type': 'application/json', if (token != null) 'authorization': 'Bearer $token', ...?headers},
      body: jsonEncode(body ?? {}),
    );
    expect(res.statusCode, inInclusiveRange(200, 299), reason: '$path: ${res.body}');
    return res.body.isEmpty ? null : jsonDecode(res.body) as Map<String, dynamic>;
  }

  /// A fresh rider asks for an auto, one random weekday at a random time,
  /// so repeated runs don't clash with this driver's earlier wins.
  @override
  Future<String> newRequest() async {
    final phone = '9${_rnd.nextInt(900000000) + 100000000}';
    await _post('/v1/auth/otp', body: {'phone': phone});
    final token = (await _post('/v1/auth/verify', body: {'phone': phone, 'code': '123456'}))!['token'] as String;
    final minutes = 360 + 5 * _rnd.nextInt(150); // 6:00–18:25
    final time = '${(minutes ~/ 60).toString().padLeft(2, '0')}:${(minutes % 60).toString().padLeft(2, '0')}';
    final series = (await _post('/v1/series', token: token, body: {
      'pickup': {'lat': 12.9352, 'lng': 77.6245, 'label': 'Koramangala'},
      'drop': {'lat': 12.9698, 'lng': 77.7178, 'label': 'Whitefield'},
      'vehicle_type': 'auto',
      'slots': [
        {'weekday': 1 + _rnd.nextInt(5), 'direction': 'outbound', 'timing': 'pickup_at', 'time': time},
      ],
      'usual_payment': 'upi',
    }))!;
    expect(series['status'], 'finding_driver', reason: 'the seed needs live auto drivers');
    final offer = (await api.offers()).offers.firstWhere((o) => o.seriesId == series['id']);
    return offer.id;
  }

  Future<void> _advance(Offer o) async {
    // The server clock may differ from ours; move it a whole window on.
    await _post('/dev/clock/advance', body: {'minutes': 16});
  }

  @override
  Future<void> closeRound(Offer o) async {
    await _advance(o);
    await _post('/internal/sweep', headers: {if (sweepToken.isNotEmpty) 'authorization': 'Bearer $sweepToken'});
  }

  @override
  Future<void> passWindow(Offer o) => _advance(o);
}
