import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/http_api.dart';
import 'package:rider/core/models.dart';
import 'package:rider/core/session_store.dart';

/// The HTTP client against canned backend responses. The same flow runs
/// against the real backend in integration_test/r1_e2e_test.dart.
void main() {
  late List<http.Request> sent;
  late MemorySessionStore store;

  HttpRiderApi apiWith(http.Response Function(http.Request r) respond) {
    sent = [];
    return HttpRiderApi(
      baseUrl: 'http://api.test/',
      store: store,
      client: MockClient((r) async {
        sent.add(r);
        return respond(r);
      }),
    );
  }

  // UTF-8 bytes, as the backend sends them ("·" in vehicle names).
  http.Response json(Object body, [int status = 200]) =>
      http.Response.bytes(utf8.encode(jsonEncode(body)), status, headers: {'content-type': 'application/json; charset=utf-8'});

  final home = demoPlaces.firstWhere((p) => p.id == 'home');
  final etv = demoPlaces.firstWhere((p) => p.id == 'etv');

  final request = CommuteRequest(
    pickup: home,
    drop: etv,
    repeat: CommuteRepeat.everyWeekday,
    weekdays: {5, 1, 2, 3, 4},
    timing: TimingMode.pickupAt,
    time: 520,
    pickupTime: 520,
    returnTrip: true,
    returnTime: 1110,
    startDate: DateTime(2026, 10, 22),
    rideType: RideType.cabSedan,
    preferences: {RidePreference.acOn},
  );

  const driverJson = {
    'id': 'd_ravi',
    'name': 'Ravi Kumar',
    'vehicle': 'White Dzire · AC · 4 seats',
    'plate': 'KA 01 AB 1234',
    'rating': 4.9,
    'upiId': 'ravi.kumar@okbank',
  };

  Map<String, Object?> commuteJson(String status) => {
        'id': 'c1',
        'status': status,
        'pickup': {'id': 'home', 'title': 'Home', 'detail': '14th Main, HSR Layout', 'lat': 12.9121, 'lng': 77.6446},
        'drop': {'id': 'etv', 'title': 'Embassy Tech Village', 'detail': 'Gate 3', 'lat': 12.9406, 'lng': 77.6964},
        'repeat': 'every_weekday',
        'weekdays': [1, 2, 3, 4, 5],
        'timing': 'pickup_at',
        'time': 520,
        'pickupTime': 520,
        'returnTrip': true,
        'returnTime': 1110,
        'startDate': '2026-10-22',
        'rideType': 'cab_sedan',
        'preferences': ['ac_on'],
        'fare': 250,
        'usualPayment': 'cash',
        'trip': {'minutes': 38, 'distanceKm': 11.9},
        'driver': driverJson,
      };

  setUp(() => store = MemorySessionStore());

  test('verifying the code saves the session and later calls send it', () async {
    final api = apiWith((r) => switch (r.url.path) {
          '/v1/auth/verify' => json({'token': 'tok1', 'rider': {'id': 'r1', 'phone': '9886054321', 'name': null}}),
          _ => json({'places': []}),
        });
    final rider = await api.verifyOtp('9886054321', '123456');
    expect(rider.phone, '9886054321');
    expect(store.token, 'tok1');
    expect(jsonDecode(sent.first.body), {'phone': '9886054321', 'code': '123456'});

    await api.searchPlaces('hsr');
    expect(sent.last.url.toString(), 'http://api.test/v1/places?q=hsr');
    expect(sent.last.headers['authorization'], 'Bearer tok1');
  });

  test('a saved session is restored at startup', () async {
    store.token = 'saved';
    final api = apiWith((_) => json({'rider': {'id': 'r1', 'phone': '9886054321', 'name': 'Asha'}, 'commute': null, 'recentRides': []}));
    final rider = await api.restoreSession();
    expect(rider?.name, 'Asha');
    expect(sent.single.headers['authorization'], 'Bearer saved');
  });

  test('no saved session means no request and Welcome', () async {
    final api = apiWith((_) => json({}));
    expect(await api.restoreSession(), isNull);
    expect(sent, isEmpty);
  });

  test('a rejected session is cleared so the next launch shows Welcome', () async {
    store.token = 'expired';
    final api = apiWith((_) => json({'error': 'unauthorized', 'message': 'Your session has ended. Sign in again.'}, 401));
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

  test('backend errors become ApiExceptions with the server message', () async {
    final api = apiWith((_) => json({'error': 'invalid_code', 'message': "That code isn't right."}, 401));
    await expectLater(
      api.verifyOtp('9886054321', '000000'),
      throwsA(isA<ApiException>()
          .having((e) => e.code, 'code', 'invalid_code')
          .having((e) => e.message, 'message', "That code isn't right.")),
    );
  });

  test('the commute request is sent in the backend shape, without pickup time or start date', () async {
    final api = apiWith((_) => json({'pickupTime': 520, 'startDate': '2026-10-22', 'proposals': []}));
    await api.findDrivers(request);
    expect(jsonDecode(sent.single.body), {
      'request': {
        'pickup': {'id': 'home', 'title': 'Home', 'detail': '14th Main, HSR Layout', 'lat': 12.9121, 'lng': 77.6446},
        'drop': {'id': 'etv', 'title': 'Embassy Tech Village', 'detail': 'Gate 3', 'lat': 12.9406, 'lng': 77.6964},
        'repeat': 'every_weekday',
        'weekdays': [1, 2, 3, 4, 5],
        'timing': 'pickup_at',
        'time': 520,
        'returnTrip': true,
        'returnTime': 1110,
        'rideType': 'cab_sedan',
        'preferences': ['ac_on'],
      },
    });
  });

  test('proposals keep their id, and confirming sends it back', () async {
    final api = apiWith((r) => switch (r.url.path) {
          '/v1/commutes/proposals' => json({
              'pickupTime': 520,
              'startDate': '2026-10-22',
              'proposals': [
                {
                  'id': 'p-1',
                  'driver': driverJson,
                  'fare': 250,
                  'trip': {'distanceKm': 11.9, 'minutes': 38},
                  'expiresAt': '2026-10-21T03:45:00.000Z',
                  'reasons': ['Same driver on every ride'],
                },
              ],
            }),
          _ => json(commuteJson('awaiting_driver'), 201),
        });
    final matches = await api.findDrivers(request);
    expect(matches.single.proposalId, 'p-1');
    expect(matches.single.driver.name, 'Ravi Kumar');
    expect(matches.single.expiresAt!.toUtc(), DateTime.utc(2026, 10, 21, 3, 45));

    final c = await api.confirmCommute(request: request, match: matches.single, usualPayment: PaymentMethod.cash);
    expect(jsonDecode(sent.last.body), {'proposalId': 'p-1', 'usualPayment': 'cash'});
    expect(c.status, CommuteStatus.awaitingDriver);
    expect(c.request.startDate, DateTime(2026, 10, 22));
    expect(c.request.weekdays, {1, 2, 3, 4, 5});
    expect(c.request.preferences, {RidePreference.acOn});
    expect(c.usualPayment, PaymentMethod.cash);
  });

  test('booking options skip ride types this app does not know', () async {
    final api = apiWith((_) => json({
          'flow': 'recurring_weekday',
          'defaultType': 'cab_sedan',
          'options': [
            {'type': 'cab_sedan', 'fare': 250, 'driversOnRoute': 3},
            {'type': 'e_rickshaw', 'fare': 40, 'driversOnRoute': 1},
          ],
        }));
    final o = await api.bookingOptions(flow: BookingFlow.recurringWeekday, pickup: home, drop: etv, pickupTime: 520);
    expect(o.options.map((x) => x.type), [RideType.cabSedan]);
    expect(o.defaultType, RideType.cabSedan);
  });

  test('home maps the commute and its status', () async {
    final api = apiWith((_) => json({
          'rider': {'id': 'r1', 'phone': '9886054321', 'name': null},
          'commute': commuteJson('active'),
          'recentRides': [],
        }));
    final h = await api.home();
    expect(h.commute!.status, CommuteStatus.active);
    expect(h.commute!.driver.upiId, 'ravi.kumar@okbank');
  });
}
