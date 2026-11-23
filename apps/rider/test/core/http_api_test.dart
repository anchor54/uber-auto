import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rider/core/http_api.dart';
import 'package:rider/core/models.dart';
import 'package:rider/core/session_store.dart';

import '../helpers/series_fixtures.dart';

/// The HTTP client against canned responses in the shapes of
/// backend/API.md. The same calls run against the real backend in
/// test/contract/rider_api_contract_test.dart.
void main() {
  late List<http.Request> sent;
  late MemorySessionStore store;

  HttpRiderApi apiWith(http.Response Function(http.Request r) respond) {
    sent = [];
    return HttpRiderApi(
      baseUrl: 'http://api.test',
      store: store,
      client: MockClient((r) async {
        sent.add(r);
        return respond(r);
      }),
    );
  }

  // UTF-8 bytes, as the backend sends them ("·" in vehicle names).
  http.Response json(Object? body, [int status = 200]) => http.Response.bytes(
      body == null ? const [] : utf8.encode(jsonEncode(body)), status,
      headers: {'content-type': 'application/json; charset=utf-8'});

  Map<String, Object?> bodyOf(http.Request r) => jsonDecode(r.body) as Map<String, Object?>;

  const raviJson = {
    'id': 'd-ravi',
    'name': 'Ravi Kumar',
    'vehicle_type': 'auto',
    'vehicle_number': 'KA 05 AB 1234',
    'vehicle_desc': 'Green auto · 3 seats',
    'rating': 4.8,
  };

  Map<String, Object?> slotJson(int weekday, String day, {String direction = 'outbound', String time = '08:30', Object? driver}) => {
        'id': 'sl-$weekday-$direction',
        'weekday': weekday,
        'day': day,
        'direction': direction,
        'timing': 'pickup_at',
        'arrive_by_time': null,
        'local_pickup_time': time,
        'estimated_drop_time': '09:21',
        'driver_id': driver == null ? null : 'd-ravi',
        'driver': driver,
        'active': true,
      };

  Map<String, Object?> seriesJson(String status, {Object? driver, Map<String, Object?>? offer}) => {
        'id': 's1',
        'rider_id': 'r1',
        'status': status,
        'city': 'blr',
        'vehicle_type': 'auto',
        'ride_type': 'auto',
        'preferences': ['ac'],
        'pickup': {'lat': 12.9352, 'lng': 77.6245, 'label': 'Koramangala'},
        'drop': {'lat': 12.9698, 'lng': 77.7178, 'label': 'Whitefield'},
        'time_zone': 'Asia/Kolkata',
        'start_date': '2026-11-23',
        'distance_km': 15.1,
        'travel_minutes': 51,
        'suggested_fare': 197,
        'usual_payment': 'cash',
        'requested_at': '2026-11-19T04:30:00Z',
        'version': 1,
        'slots': [
          slotJson(1, 'MON', driver: driver),
          slotJson(3, 'WED', driver: driver),
          slotJson(5, 'FRI', driver: driver),
        ],
        'offer': offer ??
            {
              'id': 'o1',
              'kind': 'series',
              'round': 1,
              'status': 'open',
              'opened_at': '2026-11-19T04:30:00Z',
              'expires_at': '2026-11-19T04:45:00Z',
              'outcome': null,
            },
        'round': status == 'finding_driver' ? 1 : null,
        'expires_at': status == 'finding_driver' ? '2026-11-19T04:45:00Z' : null,
      };

  setUp(() => store = MemorySessionStore());

  group('sign-in', () {
    test('verifying the code saves the session and later calls send it', () async {
      final api = apiWith((r) => switch (r.url.path) {
            '/v1/auth/verify' => json({
                'token': 'tok1',
                'user': {'id': 'r1', 'phone': '9000000101', 'role': 'rider', 'name': 'Priya Sharma'},
              }),
            _ => json({'places': []}),
          });
      final rider = await api.verifyOtp('9000000101', '123456');
      expect(rider.phone, '9000000101');
      expect(rider.name, 'Priya Sharma');
      expect(store.token, 'tok1');
      expect(bodyOf(sent.first), {'phone': '9000000101', 'code': '123456'});

      await api.searchPlaces('hsr');
      expect(sent.last.url.toString(), 'http://api.test/v1/places?q=hsr');
      expect(sent.last.headers['authorization'], 'Bearer tok1');
    });

    test('a saved session is restored at startup from /v1/me', () async {
      store.token = 'saved';
      final api = apiWith((_) => json({'id': 'r1', 'phone': '9000000101', 'role': 'rider', 'name': 'Priya Sharma'}));
      final rider = await api.restoreSession();
      expect(rider?.name, 'Priya Sharma');
      expect(sent.single.url.path, '/v1/me');
      expect(sent.single.headers['authorization'], 'Bearer saved');
    });

    test('no saved session means no request and Welcome', () async {
      final api = apiWith((_) => json({}));
      expect(await api.restoreSession(), isNull);
      expect(sent, isEmpty);
    });

    test('a rejected session is cleared so the next launch shows Welcome', () async {
      store.token = 'expired';
      final api = apiWith((_) => json({'code': 'unauthorized', 'message': 'Sign in again.', 'state': null}, 401));
      expect(await api.restoreSession(), isNull);
      expect(store.token, isNull);
    });

    test('offline at startup keeps the session and reports a network error', () async {
      store.token = 'saved';
      final api = HttpRiderApi(
        baseUrl: 'http://api.test',
        store: store,
        client: MockClient((_) async => throw http.ClientException('no route')),
      );
      await expectLater(api.restoreSession(), throwsA(isA<ApiException>().having((e) => e.code, 'code', 'network')));
      expect(store.token, 'saved');
    });

    test('errors are {code, message, state} and become ApiExceptions', () async {
      final api = apiWith((_) => json({'code': 'invalid_code', 'message': "That code isn't right.", 'state': null}, 401));
      await expectLater(
        api.verifyOtp('9000000101', '000000'),
        throwsA(isA<ApiException>()
            .having((e) => e.code, 'code', 'invalid_code')
            .having((e) => e.message, 'message', "That code isn't right.")),
      );
    });
  });

  group('series.preview', () {
    test('POST /v1/series/preview with one slot per weekday and direction', () async {
      final api = apiWith((_) => json({
            'vehicle_type': 'cab_sedan',
            'distance_km': 11.9,
            'travel_minutes': 36,
            'start_date': '2026-10-22',
            'suggested_fare': 250,
            'rides_per_month': 43,
            'time_zone': 'Asia/Kolkata',
            'slots': [
              {
                'weekday': 5, 'day': 'FRI', 'direction': 'outbound', 'timing': 'arrive_by',
                'arrive_by_time': '09:30', 'local_pickup_time': '08:40', 'estimated_drop_time': '09:16',
              },
            ],
          }));
      final p = await api.previewSeries(commuteRequest(
        weekdays: {1, 5},
        dayTimes: {5: 570},
        preferences: {RidePreference.acOn, RidePreference.womanDriver},
      ));

      expect(sent.single.method, 'POST');
      expect(sent.single.url.path, '/v1/series/preview');
      final b = bodyOf(sent.single);
      expect(b['pickup'], {'lat': 12.9121, 'lng': 77.6446, 'label': 'Home'});
      expect(b['drop'], {'lat': 12.9406, 'lng': 77.6964, 'label': 'Embassy Tech Village'});
      expect(b['vehicle_type'], 'cab_sedan');
      expect((b['preferences'] as List).toSet(), {'ac', 'woman_driver'});
      expect(b['slots'], [
        {'weekday': 1, 'direction': 'outbound', 'timing': 'pickup_at', 'time': '08:40'},
        {'weekday': 5, 'direction': 'outbound', 'timing': 'pickup_at', 'time': '09:30'},
        {'weekday': 1, 'direction': 'return', 'timing': 'pickup_at', 'time': '18:30'},
        {'weekday': 5, 'direction': 'return', 'timing': 'pickup_at', 'time': '18:30'},
      ]);
      expect(b.containsKey('start_date'), isFalse, reason: 'the server works out the start date');

      expect(p.startDate, DateTime(2026, 10, 22));
      expect(p.suggestedFare, 250);
      expect(p.travelMinutes, 36);
      expect(p.ridesPerMonth, 43);
      expect(p.rideType, RideType.cabSedan);
      final fri = p.slots.single;
      expect(fri.weekday, 5);
      expect(fri.timing, TimingMode.arriveBy);
      expect(fri.arriveByTime, 570);
      expect(fri.pickupTime, 520);
      expect(fri.estimatedDropTime, 556);
    });

    test('arrive-by slots are sent as arrive_by', () async {
      final api = apiWith((_) => json({
            'vehicle_type': 'auto', 'distance_km': 1.0, 'travel_minutes': 4, 'start_date': '2026-10-22',
            'suggested_fare': 40, 'rides_per_month': 4, 'slots': [],
          }));
      await api.previewSeries(commuteRequest(weekdays: {6}, timing: TimingMode.arriveBy, time: 570, returnTrip: false));
      expect(bodyOf(sent.single)['slots'], [
        {'weekday': 6, 'direction': 'outbound', 'timing': 'arrive_by', 'time': '09:30'},
      ]);
    });

    test('422 start_too_soon keeps its message', () async {
      final api = apiWith((_) => json({'code': 'start_too_soon', 'message': 'Start tomorrow or later.', 'state': null}, 422));
      await expectLater(
        api.previewSeries(commuteRequest(startDate: DateTime(2026, 10, 21))),
        throwsA(isA<ApiException>()
            .having((e) => e.code, 'code', 'start_too_soon')
            .having((e) => e.message, 'message', 'Start tomorrow or later.')),
      );
      expect(bodyOf(sent.single)['start_date'], '2026-10-21');
    });
  });

  group('series.create', () {
    test('POST /v1/series sends the usual payment and reads the finding_driver series', () async {
      final api = apiWith((_) => json(seriesJson('finding_driver'), 201));
      final s = await api.createSeries(commuteRequest(rideType: RideType.auto, usualPayment: PaymentMethod.cash));
      expect(sent.single.url.path, '/v1/series');
      expect(bodyOf(sent.single)['usual_payment'], 'cash');
      expect(bodyOf(sent.single)['vehicle_type'], 'auto');

      expect(s.id, 's1');
      expect(s.status, SeriesStatus.findingDriver);
      expect(s.rideType, RideType.auto);
      expect(s.preferences, {RidePreference.acOn});
      expect(s.pickup.title, 'Koramangala');
      expect(s.drop.title, 'Whitefield');
      expect(s.startDate, DateTime(2026, 11, 23));
      expect(s.suggestedFare, 197);
      expect(s.travelMinutes, 51);
      expect(s.usualPayment, PaymentMethod.cash);
      expect(s.requestedAt.toUtc(), DateTime.utc(2026, 11, 19, 4, 30));
      expect(s.weekdays, {1, 3, 5});
      expect(s.slots.every((x) => x.driver == null), isTrue);
      expect(s.slotFor(1, RideDirection.outbound)!.pickupTime, 510);
      expect(s.offer!.round, 1);
      expect(s.offer!.status, OfferStatus.open);
      expect(s.offer!.expiresAt.toUtc(), DateTime.utc(2026, 11, 19, 4, 45));
    });

    test('no eligible driver: no_driver_found with no offer', () async {
      final api = apiWith((_) => json({...seriesJson('no_driver_found'), 'offer': null}, 201));
      final s = await api.createSeries(commuteRequest(rideType: RideType.auto));
      expect(s.status, SeriesStatus.noDriverFound);
      expect(s.offer, isNull);
    });

    test('422 ride_type_not_allowed', () async {
      final api = apiWith((_) => json({'code': 'ride_type_not_allowed', 'message': 'Not here.', 'state': null}, 422));
      await expectLater(
        api.createSeries(commuteRequest(rideType: RideType.bike)),
        throwsA(isA<ApiException>().having((e) => e.code, 'code', 'ride_type_not_allowed')),
      );
    });
  });

  group('series.get', () {
    test('GET /v1/series/{id}: an active series with its driver per slot', () async {
      final api = apiWith((_) => json(seriesJson('active', driver: raviJson, offer: {
            'id': 'o1', 'kind': 'series', 'round': 1, 'status': 'closed', 'opened_at': '2026-11-19T04:30:00Z',
            'expires_at': '2026-11-19T04:45:00Z', 'outcome': 'assigned',
          })));
      final s = await api.getSeries('s1');
      expect(sent.single.method, 'GET');
      expect(sent.single.url.path, '/v1/series/s1');
      expect(s.status, SeriesStatus.active);
      expect(s.offer!.outcome, OfferOutcome.assigned);
      final d = s.slotFor(3, RideDirection.outbound)!.driver!;
      expect(d.name, 'Ravi Kumar');
      expect(d.plate, 'KA 05 AB 1234');
      expect(d.vehicle, 'Green auto · 3 seats');
      expect(d.rating, 4.8);
      expect(d.rideType, RideType.auto);
    });

    test('403 forbidden and 404 not_found', () async {
      var status = 403;
      final api = apiWith((_) => json({'code': status == 403 ? 'forbidden' : 'not_found', 'message': 'No.', 'state': null}, status));
      await expectLater(api.getSeries('x'), throwsA(isA<ApiException>().having((e) => e.code, 'code', 'forbidden')));
      status = 404;
      await expectLater(api.getSeries('x'), throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_found')));
    });

    test('home reads the rider and their series that are not ended', () async {
      final api = apiWith((r) => switch (r.url.path) {
            '/v1/me' => json({'id': 'r1', 'phone': '9000000101', 'role': 'rider', 'name': 'Priya Sharma'}),
            _ => json({
                'series': [
                  seriesJson('finding_driver'),
                  {...seriesJson('ended'), 'id': 's0'},
                ],
              }),
          });
      final h = await api.home();
      expect(h.profile.name, 'Priya Sharma');
      expect(h.series.map((s) => s.id), ['s1']);
    });
  });

  group('series.cancel_request', () {
    test('POST /v1/series/{id}/cancel-request → 204', () async {
      final api = apiWith((_) => json(null, 204));
      await api.cancelRequest('s1');
      expect(sent.single.method, 'POST');
      expect(sent.single.url.path, '/v1/series/s1/cancel-request');
    });

    test('409 already_assigned carries the series with its driver', () async {
      final api = apiWith((_) => json({
            'code': 'already_assigned',
            'message': 'Ravi was just assigned.',
            'state': seriesJson('active', driver: raviJson),
          }, 409));
      await expectLater(
        api.cancelRequest('s1'),
        throwsA(isA<ApiException>()
            .having((e) => e.code, 'code', 'already_assigned')
            .having((e) => e.state, 'state', isA<Series>())
            .having((e) => (e.state as Series).slots.first.driver!.firstName, 'driver', 'Ravi')),
      );
    });
  });

  group('series.retry', () {
    test('POST /v1/series/{id}/retry with no body tries the same request again', () async {
      final api = apiWith((_) => json(seriesJson('finding_driver')));
      final s = await api.retrySeries('s1');
      expect(sent.single.url.path, '/v1/series/s1/retry');
      expect(sent.single.body.isEmpty || bodyOf(sent.single).isEmpty, isTrue);
      expect(s.status, SeriesStatus.findingDriver);
    });

    test('with changes, the new slots and ride type are sent', () async {
      final api = apiWith((_) => json(seriesJson('finding_driver')));
      await api.retrySeries('s1', changes: commuteRequest(time: 540, rideType: RideType.auto, returnTrip: false, weekdays: {1}));
      final b = bodyOf(sent.single);
      expect(b['vehicle_type'], 'auto');
      expect(b['slots'], [
        {'weekday': 1, 'direction': 'outbound', 'timing': 'pickup_at', 'time': '09:00'},
      ]);
    });

    test('409 not_retryable', () async {
      final api = apiWith((_) => json({'code': 'not_retryable', 'message': 'No.', 'state': seriesJson('active', driver: raviJson)}, 409));
      await expectLater(api.retrySeries('s1'), throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_retryable')));
    });
  });

  test('R1.3 ride types come from /v1/ride-types?flow=recurring; unknown types are skipped', () async {
    final api = apiWith((r) => switch (r.url.path) {
          '/v1/ride-types' => json({
              'city': 'blr',
              'flow': 'recurring',
              'ride_types': [
                {'ride_type': 'auto', 'label': 'Auto', 'description': 'Up to 3 riders · open sides', 'seats': 3},
                {'ride_type': 'e_rickshaw', 'label': 'E-rickshaw', 'description': '', 'seats': 3},
              ],
            }),
          _ => json({
              'vehicle_type': 'auto', 'distance_km': 11.9, 'travel_minutes': 40, 'start_date': '2026-10-22',
              'suggested_fare': 161, 'rides_per_month': 43, 'slots': [],
            }),
        });
    final o = await api.rideTypeOptions(commuteRequest());
    expect(sent.first.url.queryParameters['flow'], 'recurring');
    expect(o.map((x) => x.type), [RideType.auto]);
    expect(o.single.fare, 161);
  });
}
