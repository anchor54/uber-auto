@Tags(['contract'])
library;

import 'dart:io';
import 'dart:math';

import 'package:http/http.dart' as http;

import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/api.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/http_api.dart';
import 'package:rider/core/models.dart';
import 'package:rider/core/session_store.dart';

/// One script of RiderApi calls, run against the demo backend and, when
/// RIDER_API_URL is set, against the real backend too. If the two drift
/// apart (a renamed field, a changed rule), this fails before a screen does.
///
///   RIDER_API_URL=http://localhost:8080 flutter test --tags contract
///
/// The backend must run with FIXED_OTP=123456 and the demo seed.
void main() {
  // flutter_test answers every real HTTP request with 400; this test needs
  // the network.
  HttpOverrides.global = null;
  final url = Platform.environment['RIDER_API_URL'] ?? '';
  final backends = <String, RiderApi Function()>{
    'demo backend': () => FakeRiderApi(),
    if (url.isNotEmpty) 'real backend ($url)': () => HttpRiderApi(baseUrl: url, store: MemorySessionStore()),
  };

  for (final MapEntry(key: name, value: make) in backends.entries) {
    group(name, () {
      late RiderApi api;
      final phone = '9${Random().nextInt(900000000) + 100000000}';

      setUpAll(() async {
        api = make();
        await api.sendOtp(phone);
        await api.verifyOtp(phone, FakeRiderApi.demoOtp);
      });

      Future<Place> place(String query, String id) async =>
          (await api.searchPlaces(query)).firstWhere((p) => p.id == id);

      // Spread runs over the day so repeated runs against one database don't
      // use up the three seeded sedan drivers' 8:40 slots.
      final rnd = Random();
      final morning = 420 + 5 * rnd.nextInt(48); // 7:00–10:55
      final evening = 900 + 5 * rnd.nextInt(60); // 15:00–19:55

      CommuteRequest request(Place pickup, Place drop, {Set<RidePreference> prefs = const {RidePreference.acOn}}) =>
          CommuteRequest(
            pickup: pickup,
            drop: drop,
            repeat: CommuteRepeat.everyWeekday,
            weekdays: {1, 2, 3, 4, 5},
            timing: TimingMode.pickupAt,
            time: morning,
            pickupTime: morning,
            returnTrip: true,
            returnTime: evening,
            startDate: DateTime(2030, 1, 1), // ignored: the server sets it
            rideType: RideType.cabSedan,
            preferences: prefs,
          );

      test('rejects a non-mobile number with invalid_phone', () async {
        await expectLater(
          api.sendOtp('5123456789'),
          throwsA(isA<ApiException>().having((e) => e.code, 'code', 'invalid_phone')),
        );
      });

      test('a wrong code is invalid_code', () async {
        final other = make();
        final p = '9${Random().nextInt(900000000) + 100000000}';
        await other.sendOtp(p);
        await expectLater(
          other.verifyOtp(p, '000000'),
          throwsA(isA<ApiException>().having((e) => e.code, 'code', 'invalid_code')),
        );
      });

      test('place search finds the demo places by area', () async {
        final hsr = await api.searchPlaces('HSR');
        expect(hsr.map((p) => p.id), contains('home'));
        expect((await api.searchPlaces('')).length, greaterThanOrEqualTo(7));
      });

      test('trip estimate and fares agree between app and backend', () async {
        final home = await place('HSR', 'home');
        final etv = await place('Embassy', 'etv');
        final trip = await api.estimateTrip(home, etv);
        expect(trip.distanceKm, 11.9);
        expect(trip.minutes, 38);
        final o = await api.bookingOptions(flow: BookingFlow.recurringWeekday, pickup: home, drop: etv, pickupTime: 520);
        expect(o.defaultType, RideType.cabSedan);
        expect(o.options.map((x) => x.type), RideType.values);
        expect(o.optionFor(RideType.cabSedan)!.fare, 250);
        expect(o.optionFor(RideType.bike)!.fare, 90);
      });

      test('a woman-driver request finds nobody; the waitlist takes it', () async {
        final r = request(await place('HSR', 'home'), await place('Embassy', 'etv'), prefs: {RidePreference.womanDriver});
        expect(await api.findDrivers(r), isEmpty);
        await api.joinWaitlist(r);
      });

      test('match, confirm, and Home shows the commute', () async {
        final r = request(await place('HSR', 'home'), await place('Embassy', 'etv'));
        final matches = await api.findDrivers(r);
        expect(matches, isNotEmpty);
        expect(matches.length, lessThanOrEqualTo(3));
        expect(matches.first.fare, 250);
        expect(matches.first.reasons, isNotEmpty);

        final c = await api.confirmCommute(request: r, match: matches.first, usualPayment: PaymentMethod.cash);
        expect(c.driver.id, matches.first.driver.id);
        expect(c.usualPayment, PaymentMethod.cash);
        expect(c.request.pickupTime, morning);
        expect(c.request.weekdays, {1, 2, 3, 4, 5});
        expect([CommuteStatus.active, CommuteStatus.awaitingDriver], contains(c.status));

        final h = await api.home();
        expect(h.profile.phone, phone);
        expect(h.commute?.id, c.id);
      });

      test('R2: the active commute has rides; a scheduled ride shows no PIN and takes no payment', () async {
        var h = await api.home();
        if (h.commute!.status == CommuteStatus.awaitingDriver) {
          // The real backend waits for the driver (D2); the simulator accepts.
          final res = await http.post(Uri.parse('$url/dev/sim/commutes/${h.commute!.id}/accept'));
          expect(res.statusCode, 200, reason: 'run the backend with ENABLE_DRIVER_SIM=true: ${res.body}');
          h = await api.home();
        }
        expect(h.commute!.status, CommuteStatus.active);
        final rides = await api.upcomingRides(days: 14);
        expect(rides, isNotEmpty);
        final first = rides.first;
        expect(first.state, RideState.scheduled);
        expect(first.pin, isNull);
        expect(first.fare, 250);
        expect(first.driver.id, h.commute!.driver.id);
        expect(first.windowEnd.difference(first.windowStart), const Duration(minutes: 10));
        expect(rides.where((r) => r.direction == RideDirection.returnLeg), isNotEmpty);

        expect((await api.ride(first.id)).id, first.id);
        final live = await api.liveRide(first.id);
        expect(live.state, RideState.scheduled);
        expect(live.driverAt, isNull);

        await expectLater(
          api.markPayment(first.id, PaymentMethod.upi),
          throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_completed')),
        );
        await expectLater(
          api.rateRide(first.id, const RideRating(stars: 5)),
          throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_completed')),
        );
        expect(await api.unsettledRides(), isEmpty);
        final running = await api.runningLate(first.id);
        expect(running.riderLateUsed, isTrue);
        await expectLater(
          api.runningLate(first.id),
          throwsA(isA<ApiException>().having((e) => e.code, 'code', 'already_used')),
        );
        await api.registerDevice('contract-test-token-${Random().nextInt(1 << 30)}', 'android');
      });
    });
  }
}
