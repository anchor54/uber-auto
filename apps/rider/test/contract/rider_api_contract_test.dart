@Tags(['contract'])
library;

import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:http/http.dart' as http;

import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/b.api.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/http_api.dart';
import 'package:rider/core/models.dart';
import 'package:rider/core/session_store.dart';

import '../helpers/series_fixtures.dart';

/// One script of RiderApi calls for R1 (series.preview, create, get,
/// cancel_request, retry), run against the demo backend and, when
/// RIDER_API_URL is set, against the real backend too. If the two drift
/// apart (a renamed field, a changed rule), this fails before a screen does.
///
///   RIDER_API_URL=http://localhost:8080 flutter test --tags contract
///
/// The backend must run with FIXED_OTP=123456, ENABLE_DEV_ROUTES=true (so the
/// test can move its clock) and the spec seed (live auto drivers
/// 9000000201–204). Set SWEEP_TOKEN if the sweeper needs one.
void main() {
  // flutter_test answers every real HTTP request with 400; this test needs
  // the network.
  HttpOverrides.global = null;
  final url = Platform.environment['RIDER_API_URL'] ?? '';
  final sweepToken = Platform.environment['SWEEP_TOKEN'] ?? '';
  final driverPhones = (Platform.environment['DRIVER_PHONES'] ?? '9000000201,9000000202,9000000203,9000000204').split(',');

  final backends = <String, _Backend Function()>{
    'demo backend': _DemoBackend.new,
    if (url.isNotEmpty) 'real backend ($url)': () => _RealBackend(url, sweepToken, driverPhones),
  };

  for (final MapEntry(key: name, value: make) in backends.entries) {
    group(name, () {
      late _Backend b;
      final phone = '9${Random().nextInt(900000000) + 100000000}';

      setUpAll(() async {
        b = make();
        await b.api.sendOtp(phone);
        await b.api.verifyOtp(phone, FakeRiderApi.demoOtp);
      });

      Future<Place> place(String query, String id) async => (await b.api.searchPlaces(query)).firstWhere((p) => p.id == id);

      // Spread runs over the day and the week so repeated runs against one
      // database don't clash with the drivers' earlier wins.
      final rnd = Random();
      final morning = 360 + 5 * rnd.nextInt(60); // 6:00–10:55
      final day = 1 + rnd.nextInt(5);

      Future<CommuteRequest> request({Set<RidePreference> prefs = const {}, int? time}) async => commuteRequest(
            pickup: await place('HSR', 'home'),
            drop: await place('Embassy', 'etv'),
            weekdays: {day},
            time: time ?? morning,
            returnTrip: false,
            rideType: RideType.auto,
            preferences: prefs,
          );

      test('rejects a non-mobile number with invalid_phone', () async {
        await expectLater(
          b.api.sendOtp('5123456789'),
          throwsA(isA<ApiException>().having((e) => e.code, 'code', 'invalid_phone')),
        );
      });

      test('a wrong code is invalid_code', () async {
        final other = make().api;
        final p = '9${Random().nextInt(900000000) + 100000000}';
        await other.sendOtp(p);
        await expectLater(
          other.verifyOtp(p, '000000'),
          throwsA(isA<ApiException>().having((e) => e.code, 'code', 'invalid_code')),
        );
      });

      test('place search finds the demo places by area', () async {
        expect((await b.api.searchPlaces('HSR')).map((p) => p.id), contains('home'));
      });

      test('R1.3 regular-ride types include auto, each with a fare', () async {
        final o = await b.api.rideTypeOptions(await request());
        expect(o.map((x) => x.type), contains(RideType.auto));
        expect(o.every((x) => x.fare > 0), isTrue);
      });

      test('series.preview: start date after today, fare, pickup times, nothing saved', () async {
        final before = (await b.api.home()).series.length;
        final p = await b.api.previewSeries(await request());
        expect(p.rideType, RideType.auto);
        expect(p.travelMinutes, greaterThan(0));
        expect(p.distanceKm, greaterThan(0));
        expect(p.suggestedFare, greaterThan(0));
        expect(p.startDate.weekday, day);
        expect(p.slots.single.pickupTime, morning);
        expect((await b.api.home()).series.length, before);
      });

      test('series.preview: arrive-by = arrival − trip − 10 min, rounded down to 5', () async {
        final r = await request();
        final p = await b.api.previewSeries(commuteRequest(
          pickup: r.pickup, drop: r.drop, weekdays: {day}, timing: TimingMode.arriveBy, time: 600, returnTrip: false,
          rideType: RideType.auto,
        ));
        final s = p.slots.single;
        final raw = 600 - p.travelMinutes - 10;
        expect(s.timing, TimingMode.arriveBy);
        expect(s.arriveByTime, 600);
        expect(s.pickupTime, raw - raw % 5);
      });

      test('series.preview: a start date of today is start_too_soon', () async {
        final r = await request();
        await expectLater(
          b.api.previewSeries(commuteRequest(
            pickup: r.pickup, drop: r.drop, weekdays: {day}, time: morning, returnTrip: false, rideType: RideType.auto,
            startDate: DateTime.now().subtract(const Duration(days: 1)),
          )),
          throwsA(isA<ApiException>().having((e) => e.code, 'code', 'start_too_soon')),
        );
      });

      test('create → finding_driver, round 1 → a driver is picked → active', () async {
        final s = await b.api.createSeries(await request());
        expect(s.status, SeriesStatus.findingDriver);
        expect(s.offer!.round, 1);
        expect(s.offer!.status, OfferStatus.open);
        expect(s.offer!.expiresAt.difference(s.offer!.openedAt), const Duration(minutes: 15));
        expect(s.slots.every((x) => x.driver == null), isTrue);
        expect((await b.api.getSeries(s.id)).id, s.id);
        expect((await b.api.home()).series.map((x) => x.id), contains(s.id));

        await b.driversInterested(s.id);
        await b.closeRound();
        final active = await b.api.getSeries(s.id);
        expect(active.status, SeriesStatus.active);
        expect(active.offer!.outcome, OfferOutcome.assigned);
        final driver = active.slots.single.driver!;
        expect(driver.name, isNotEmpty);
        expect(driver.plate, isNotEmpty);

        // Too late to cancel: already_assigned, with the series as state.
        await expectLater(
          b.api.cancelRequest(s.id),
          throwsA(isA<ApiException>()
              .having((e) => e.code, 'code', 'already_assigned')
              .having((e) => (e.state as Series).slots.single.driver?.id, 'driver', driver.id)),
        );
        // And not retryable.
        await expectLater(
          b.api.retrySeries(s.id),
          throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_retryable')),
        );
      });

      test('cancel during round 1 → ended, round cancelled; cancelling again is fine', () async {
        final s = await b.api.createSeries(await request(time: morning + 5));
        await b.api.cancelRequest(s.id);
        final ended = await b.api.getSeries(s.id);
        expect(ended.status, SeriesStatus.ended);
        expect(ended.offer!.outcome, OfferOutcome.cancelled);
        await b.api.cancelRequest(s.id);
        expect((await b.api.home()).series.map((x) => x.id), isNot(contains(s.id)));
      });

      test('nobody interested: 3 rounds → no_driver_found → retry with a new time → back to finding_driver', () async {
        // The demo backend's demo driver is never interested in a "Woman
        // driver" request; on the real backend nobody is marked interested.
        final s = await b.api.createSeries(await request(prefs: {RidePreference.womanDriver}, time: morning + 10));
        for (var round = 1; round <= 3; round++) {
          final now = await b.api.getSeries(s.id);
          expect(now.status, SeriesStatus.findingDriver, reason: 'round $round');
          expect(now.offer!.round, round);
          await b.closeRound();
        }
        final none = await b.api.getSeries(s.id);
        expect(none.status, SeriesStatus.noDriverFound);
        expect(none.offer!.outcome, OfferOutcome.noFit);

        final r = await request(time: morning + 15);
        final retried = await b.api.retrySeries(s.id, changes: r);
        expect(retried.id, s.id);
        expect(retried.status, SeriesStatus.findingDriver);
        expect(retried.offer!.round, 1);
        expect(retried.slots.single.pickupTime, morning + 15);
        expect(retried.preferences, isEmpty);

        await b.api.cancelRequest(s.id); // leave nothing open behind
        expect((await b.api.getSeries(s.id)).status, SeriesStatus.ended);
      });

      test('an unknown series is not_found', () async {
        await expectLater(
          b.api.getSeries(b.unknownId),
          throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_found')),
        );
      });
    });
  }
}

/// What the script needs beyond RiderApi: drivers saying "I'm interested"
/// and a round's window passing.
abstract class _Backend {
  RiderApi get api;
  Future<void> driversInterested(String seriesId);
  Future<void> closeRound();
  String get unknownId;
}

class _DemoBackend implements _Backend {
  _DemoBackend() {
    fake = FakeRiderApi(now: () => _now);
  }

  DateTime _now = DateTime.now();
  late final FakeRiderApi fake;

  @override
  RiderApi get api => fake;

  /// The demo driver is always interested (unless "Woman driver").
  @override
  Future<void> driversInterested(String seriesId) async {}

  @override
  Future<void> closeRound() async => _now = _now.add(const Duration(minutes: 15));

  @override
  String get unknownId => 'sr_nope';
}

class _RealBackend implements _Backend {
  _RealBackend(this.url, this.sweepToken, this.driverPhones)
      : api = HttpRiderApi(baseUrl: url, store: MemorySessionStore());

  final String url;
  final String sweepToken;
  final List<String> driverPhones;

  @override
  final RiderApi api;

  @override
  String get unknownId => '00000000-0000-4000-8000-000000000000';

  Future<Map<String, dynamic>?> _post(String path, {Object? body, String? token}) async {
    final res = await http.post(
      Uri.parse('$url$path'),
      headers: {'content-type': 'application/json', if (token != null) 'authorization': 'Bearer $token'},
      body: jsonEncode(body ?? {}),
    );
    expect(res.statusCode, inInclusiveRange(200, 299), reason: '$path: ${res.body}');
    return res.body.isEmpty ? null : jsonDecode(res.body) as Map<String, dynamic>;
  }

  /// Every seeded auto driver says "I'm interested"; the pick takes one
  /// whose week fits.
  @override
  Future<void> driversInterested(String seriesId) async {
    for (final phone in driverPhones) {
      await _post('/v1/driver/auth/otp', body: {'phone': phone});
      final token = (await _post('/v1/driver/auth/verify', body: {'phone': phone, 'code': FakeRiderApi.demoOtp}))!['token'] as String;
      final res = await http.get(Uri.parse('$url/v1/offers'), headers: {'authorization': 'Bearer $token'});
      expect(res.statusCode, 200, reason: res.body);
      final offers = (jsonDecode(res.body) as Map<String, dynamic>)['offers'] as List;
      for (final o in offers.cast<Map<String, dynamic>>()) {
        if (o['series_id'] == seriesId) await _post('/v1/offers/${o['id']}/mark-interested', token: token);
      }
    }
  }

  /// Moves the server clock past the window and runs the sweep once.
  @override
  Future<void> closeRound() async {
    await _post('/dev/clock/advance', body: {'minutes': 15});
    final res = await http.post(
      Uri.parse('$url/internal/sweep'),
      headers: {if (sweepToken.isNotEmpty) 'authorization': 'Bearer $sweepToken'},
    );
    expect(res.statusCode, 200, reason: 'sweep: ${res.body}');
  }
}
