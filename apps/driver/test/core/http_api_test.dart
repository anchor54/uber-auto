import 'dart:convert';

import 'package:driver/core/http_api.dart';
import 'package:driver/core/models.dart';
import 'package:driver/core/session_store.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

/// HttpDriverApi against canned responses in the shapes of backend/API.md
/// (Driver, D2). The real backend runs in test/contract/driver_api_contract_test.dart.
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

  Map<String, Object?> offerJson({bool interested = false, String status = 'open', bool canRespond = true}) => {
        'id': 'o1', 'kind': 'series', 'series_id': 's1', 'round': 1,
        'status': status, 'outcome': null,
        'opened_at': '2026-11-19T04:30:00Z', 'expires_at': '2026-11-19T04:45:00Z',
        'seconds_left': 840, 'can_respond': canRespond,
        'interested': interested, 'interested_at': interested ? '2026-11-19T04:31:00Z' : null,
        'dismissed': false, 'result': null,
        'rider': {'first_name': 'Priya', 'name': 'Priya S.', 'rating': 4.8},
        'vehicle_type': 'auto', 'preferences': [],
        'pickup': {'lat': 12.9352, 'lng': 77.6245, 'label': 'Koramangala'},
        'drop': {'lat': 12.9698, 'lng': 77.7178, 'label': 'Whitefield'},
        'start_date': '2026-11-23', 'distance_km': 15.1, 'travel_minutes': 51,
        'fare_per_ride': 197, 'rides_per_month': 13, 'fare_per_month': 2561, 'usual_payment': 'cash',
        'slots': [
          for (final (d, day) in [(1, 'MON'), (3, 'WED'), (5, 'FRI')])
            {
              'weekday': d, 'day': day, 'direction': 'outbound', 'timing': 'pickup_at',
              'arrive_by_time': null, 'local_pickup_time': '08:30', 'estimated_drop_time': '09:21',
            },
        ],
      };

  setUp(() => store = MemorySessionStore()..token = 'tok');

  test('driver sign-in uses /v1/driver/auth/* and saves the token', () async {
    store = MemorySessionStore();
    final api = apiWith((r) => switch (r.url.path) {
          '/v1/driver/auth/verify' => json({
              'token': 'drv1',
              'user': {'id': 'u1', 'phone': '9000000201', 'role': 'driver', 'name': 'Ravi Kumar'},
            }),
          _ => json(null, 204),
        });
    await api.sendOtp('9000000201');
    expect(sent.first.url.path, '/v1/driver/auth/otp');
    final me = await api.verifyOtp('9000000201', '123456');
    expect(sent.last.url.path, '/v1/driver/auth/verify');
    expect(me.name, 'Ravi Kumar');
    expect(store.token, 'drv1');
  });

  test('403 not_a_driver on sign-in', () async {
    store = MemorySessionStore();
    final api = apiWith((_) => json({'code': 'not_a_driver', 'message': 'Not a driver.', 'state': null}, 403));
    await expectLater(api.verifyOtp('9000000101', '123456'),
        throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_a_driver')));
  });

  test('offers.list: GET /v1/offers maps offers and result cards', () async {
    final api = apiWith((_) => json({
          'offers': [offerJson(interested: true)],
          'results': [
            {'offer_id': 'o0', 'series_id': 's0', 'result': 'went_to_another_driver', 'message': 'Went to another driver', 'closed_at': '2026-11-19T04:00:00Z'},
            {'offer_id': 'o9', 'series_id': 's9', 'result': 'rider_withdrew', 'message': 'The rider withdrew this request', 'closed_at': '2026-11-19T04:00:00Z'},
          ],
        }));
    final list = await api.offers();
    expect(sent.single.method, 'GET');
    expect(sent.single.url.path, '/v1/offers');
    expect(sent.single.headers['authorization'], 'Bearer tok');

    final o = list.offers.single;
    expect(o.id, 'o1');
    expect(o.kind, OfferKind.series);
    expect(o.seriesId, 's1');
    expect(o.round, 1);
    expect(o.status, OfferStatus.open);
    expect(o.canRespond, isTrue);
    expect(o.interested, isTrue);
    expect(o.expiresAt.toUtc(), DateTime.utc(2026, 11, 19, 4, 45));
    expect(o.openedAt.toUtc(), DateTime.utc(2026, 11, 19, 4, 30));
    expect(o.rider.firstName, 'Priya');
    expect(o.rider.name, 'Priya S.');
    expect(o.rider.rating, 4.8);
    expect(o.pickup.label, 'Koramangala');
    expect(o.drop.label, 'Whitefield');
    expect(o.startDate, DateTime(2026, 11, 23));
    expect(o.travelMinutes, 51);
    expect(o.farePerRide, 197);
    expect(o.ridesPerMonth, 13);
    expect(o.farePerMonth, 2561);
    expect(o.usualPayment, PaymentMethod.cash);
    expect(o.slots.map((s) => s.weekday), [1, 3, 5]);
    expect(o.slots.first.direction, RideDirection.outbound);
    expect(o.slots.first.pickupTime, 510);
    expect(o.slots.first.estimatedDropTime, 561);

    expect(list.results.map((r) => r.result), ['went_to_another_driver', 'rider_withdrew']);
    expect(list.results.first.offerId, 'o0');
    expect(list.results.last.message, 'The rider withdrew this request');
  });

  test('offers.list: 403 not_eligible', () async {
    final api = apiWith((_) => json({'code': 'not_eligible', 'message': 'Your plan has lapsed.', 'state': null}, 403));
    await expectLater(api.offers(), throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_eligible')));
  });

  test('offer.get: GET /v1/offers/{id}; a closing offer cannot be answered', () async {
    final api = apiWith((_) => json(offerJson(status: 'closing', canRespond: false)));
    final o = await api.offer('o1');
    expect(sent.single.url.path, '/v1/offers/o1');
    expect(o.status, OfferStatus.closing);
    expect(o.canRespond, isFalse);
  });

  test('mark-interested, withdraw-interest and dismiss are POSTs answered 204', () async {
    final api = apiWith((_) => json(null, 204));
    await api.markInterested('o1');
    await api.withdrawInterest('o1');
    await api.dismissOffer('o1');
    expect(sent.map((r) => '${r.method} ${r.url.path}'), [
      'POST /v1/offers/o1/mark-interested',
      'POST /v1/offers/o1/withdraw-interest',
      'POST /v1/offers/o1/dismiss',
    ]);
  });

  test('a late tap: 409 offer_closed carries the offer as state', () async {
    final api = apiWith((_) => json({
          'code': 'offer_closed',
          'message': 'This request has closed.',
          'state': offerJson(status: 'closing', canRespond: false),
        }, 409));
    await expectLater(
      api.markInterested('o1'),
      throwsA(isA<ApiException>()
          .having((e) => e.code, 'code', 'offer_closed')
          .having((e) => e.message, 'message', 'This request has closed.')
          .having((e) => (e.state as Offer).status, 'state', OfferStatus.closing)),
    );
  });

  test('rides.week: GET /v1/rides/week?start=… maps rides and the new flag', () async {
    final api = apiWith((_) => json({
          'week_start': '2026-11-23',
          'week_end': '2026-11-29',
          'rides': [
            {
              'id': 'r1', 'series_id': 's1', 'slot_id': 'sl1', 'local_date': '2026-11-23', 'weekday': 1,
              'direction': 'outbound', 'pickup_at': '2026-11-23T03:00:00Z', 'drop_at': '2026-11-23T03:51:00Z',
              'busy_until': '2026-11-23T04:01:00Z', 'local_pickup_time': '08:30', 'status': 'assigned', 'fare': 197,
              'rider_id': 'u9', 'rider_name': 'Priya Sharma',
              'pickup': {'lat': 12.9352, 'lng': 77.6245, 'label': 'Koramangala'},
              'drop': {'lat': 12.9698, 'lng': 77.7178, 'label': 'Whitefield'},
              'new': true,
            },
          ],
        }));
    final w = await api.week(start: DateTime(2026, 11, 23));
    expect(sent.single.url.path, '/v1/rides/week');
    expect(sent.single.url.queryParameters['start'], '2026-11-23');
    expect(w.weekStart, DateTime(2026, 11, 23));
    expect(w.weekEnd, DateTime(2026, 11, 29));
    final r = w.rides.single;
    expect(r.id, 'r1');
    expect(r.seriesId, 's1');
    expect(r.date, DateTime(2026, 11, 23));
    expect(r.weekday, 1);
    expect(r.direction, RideDirection.outbound);
    expect(r.pickupTime, 510);
    expect(r.riderName, 'Priya Sharma');
    expect(r.fare, 197);
    expect(r.isNew, isTrue);
  });

  test('rides.week without a start lets the server pick this week', () async {
    final api = apiWith((_) => json({'week_start': '2026-11-16', 'week_end': '2026-11-22', 'rides': []}));
    final w = await api.week();
    expect(sent.single.url.queryParameters.containsKey('start'), isFalse);
    expect(w.rides, isEmpty);
  });
}
