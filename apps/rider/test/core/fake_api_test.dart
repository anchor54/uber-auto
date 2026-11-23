import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

import '../helpers/series_fixtures.dart';

/// The demo backend plays the series story of backend/API.md on its clock.
void main() {
  late DateTime now;
  late FakeRiderApi api;
  setUp(() {
    now = DateTime(2026, 10, 21, 9); // Wed
    api = FakeRiderApi(now: () => now);
  });

  void advance([Duration d = const Duration(minutes: 15)]) => now = now.add(d);

  group('OTP', () {
    test('rejects numbers that are not Indian mobiles', () {
      expect(() => api.sendOtp('12345'), throwsA(isA<ApiException>()));
      expect(() => api.sendOtp('1886054321'), throwsA(isA<ApiException>()));
    });

    test('accepts only the demo code', () async {
      await api.sendOtp('9886054321');
      expect(() => api.verifyOtp('9886054321', '000000'), throwsA(isA<ApiException>()));
      final profile = await api.verifyOtp('9886054321', FakeRiderApi.demoOtp);
      expect(profile.phone, '9886054321');
    });
  });

  test('place search matches title or detail, case-insensitive', () async {
    expect((await api.searchPlaces('hsr')).map((p) => p.id), contains('home'));
    expect((await api.searchPlaces('EMBASSY')).map((p) => p.id), ['etv']);
    expect(await api.searchPlaces('nowhere'), isEmpty);
  });

  group('series.preview', () {
    test('start date is the first chosen weekday from tomorrow on; nothing is saved', () async {
      final p = await api.previewSeries(commuteRequest());
      expect(p.startDate, DateTime(2026, 10, 22));
      expect(p.suggestedFare, greaterThan(0));
      expect(p.travelMinutes, greaterThan(0));
      expect(p.slots, hasLength(10));
      expect(api.series, isNull);
    });

    test('a Saturday-only ride starts on the coming Saturday', () async {
      final p = await api.previewSeries(commuteRequest(weekdays: {6}));
      expect(p.startDate, DateTime(2026, 10, 24));
    });

    test('per-day times are kept per weekday', () async {
      final p = await api.previewSeries(commuteRequest(dayTimes: {5: 570}));
      int pickup(int day) => p.slots.firstWhere((s) => s.weekday == day && s.direction == RideDirection.outbound).pickupTime;
      expect(pickup(1), 520);
      expect(pickup(5), 570);
    });

    test('arrive-by: pickup = arrival − trip − 10 min, rounded down to 5 min', () async {
      final p = await api.previewSeries(commuteRequest(timing: TimingMode.arriveBy, time: 570));
      final raw = 570 - p.travelMinutes - 10;
      final s = p.slots.firstWhere((s) => s.direction == RideDirection.outbound);
      expect(s.timing, TimingMode.arriveBy);
      expect(s.arriveByTime, 570);
      expect(s.pickupTime, raw - raw % 5);
      expect(s.pickupTime % 5, 0);
    });

    test('each ride type has its own fare', () async {
      final bike = await api.previewSeries(commuteRequest(rideType: RideType.bike));
      final suv = await api.previewSeries(commuteRequest(rideType: RideType.cabSuv));
      expect(bike.suggestedFare, lessThan(suv.suggestedFare));
    });
  });

  group('series.create and the rounds', () {
    test('create opens round 1 for 15 minutes with no driver', () async {
      final s = await api.createSeries(commuteRequest(usualPayment: PaymentMethod.cash));
      expect(s.status, SeriesStatus.findingDriver);
      expect(s.offer!.round, 1);
      expect(s.offer!.status, OfferStatus.open);
      expect(s.offer!.expiresAt, now.add(const Duration(minutes: 15)));
      expect(s.slots.every((x) => x.driver == null), isTrue);
      expect(s.usualPayment, PaymentMethod.cash);
      expect(s.weekdays, {1, 2, 3, 4, 5});
      expect(s.returnTrip, isTrue);
    });

    test('round 1 closes 15 minutes later and the demo driver takes every slot', () async {
      final s = await api.createSeries(commuteRequest());
      advance(const Duration(minutes: 14));
      expect((await api.getSeries(s.id)).status, SeriesStatus.findingDriver);
      advance(const Duration(minutes: 1));
      final active = await api.getSeries(s.id);
      expect(active.status, SeriesStatus.active);
      expect(active.offer!.outcome, OfferOutcome.assigned);
      expect(active.slots.map((x) => x.driver?.name).toSet(), {'Ravi Kumar'});
      expect(await api.upcomingRides(days: 14), isNotEmpty);
    });

    test('"Woman driver" finds nobody: three rounds, then no_driver_found', () async {
      final s = await api.createSeries(commuteRequest(preferences: {RidePreference.womanDriver}));
      advance();
      var now1 = await api.getSeries(s.id);
      expect(now1.status, SeriesStatus.findingDriver);
      expect(now1.offer!.round, 2);
      advance();
      now1 = await api.getSeries(s.id);
      expect(now1.offer!.round, 3);
      advance();
      now1 = await api.getSeries(s.id);
      expect(now1.status, SeriesStatus.noDriverFound);
      expect(now1.offer!.outcome, OfferOutcome.noFit);
      expect(now1.slots.every((x) => x.driver == null), isTrue);
    });

    test('a type with no eligible driver is no_driver_found straight away, no rounds', () async {
      final none = FakeRiderApi(now: () => now, noDriverTypes: {RideType.bike});
      final s = await none.createSeries(commuteRequest(rideType: RideType.bike));
      expect(s.status, SeriesStatus.noDriverFound);
      expect(s.offer, isNull);
    });

    test('closeRound closes the open round now', () async {
      final s = await api.createSeries(commuteRequest());
      api.closeRound();
      expect((await api.getSeries(s.id)).status, SeriesStatus.active);
    });

    test('home lists series that are not ended', () async {
      final s = await api.createSeries(commuteRequest());
      expect((await api.home()).series.map((x) => x.id), [s.id]);
      await api.cancelRequest(s.id);
      expect((await api.home()).series, isEmpty);
    });

    test('an unknown series is not_found', () async {
      await expectLater(api.getSeries('nope'), throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_found')));
    });
  });

  group('series.cancel_request', () {
    test('during finding_driver: ended, round cancelled', () async {
      final s = await api.createSeries(commuteRequest());
      await api.cancelRequest(s.id);
      final ended = await api.getSeries(s.id);
      expect(ended.status, SeriesStatus.ended);
      expect(ended.offer!.outcome, OfferOutcome.cancelled);
      advance();
      expect((await api.getSeries(s.id)).status, SeriesStatus.ended, reason: 'the pick sees ended and stops');
    });

    test('from no_driver_found: ended', () async {
      final s = await api.createSeries(commuteRequest(preferences: {RidePreference.womanDriver}));
      advance(const Duration(minutes: 45));
      await api.cancelRequest(s.id);
      expect((await api.getSeries(s.id)).status, SeriesStatus.ended);
    });

    test('already ended: no error', () async {
      final s = await api.createSeries(commuteRequest());
      await api.cancelRequest(s.id);
      await api.cancelRequest(s.id);
    });

    test('after the pick: already_assigned with the series and its driver', () async {
      final s = await api.createSeries(commuteRequest());
      advance();
      await expectLater(
        api.cancelRequest(s.id),
        throwsA(isA<ApiException>()
            .having((e) => e.code, 'code', 'already_assigned')
            .having((e) => (e.state as Series).slots.first.driver!.name, 'driver', 'Ravi Kumar')),
      );
      expect((await api.getSeries(s.id)).status, SeriesStatus.active);
    });
  });

  group('series.retry', () {
    Future<Series> noDriver() async {
      final s = await api.createSeries(commuteRequest(preferences: {RidePreference.womanDriver}));
      advance(const Duration(minutes: 45));
      expect((await api.getSeries(s.id)).status, SeriesStatus.noDriverFound);
      return s;
    }

    test('back to finding_driver with a fresh round 1', () async {
      final s = await noDriver();
      final r = await api.retrySeries(s.id);
      expect(r.id, s.id);
      expect(r.status, SeriesStatus.findingDriver);
      expect(r.offer!.round, 1);
      expect(r.offer!.status, OfferStatus.open);
    });

    test('with changes: slots and preferences are rewritten, then a driver is found', () async {
      final s = await noDriver();
      final r = await api.retrySeries(s.id, changes: commuteRequest(time: 540, rideType: RideType.auto));
      expect(r.rideType, RideType.auto);
      expect(r.preferences, isEmpty);
      expect(r.slotFor(1, RideDirection.outbound)!.pickupTime, 540);
      advance();
      expect((await api.getSeries(s.id)).status, SeriesStatus.active);
    });

    test('not_retryable unless no_driver_found', () async {
      final s = await api.createSeries(commuteRequest());
      await expectLater(
        api.retrySeries(s.id),
        throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_retryable')),
      );
      advance();
      await expectLater(
        api.retrySeries(s.id),
        throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_retryable')),
      );
    });
  });
}
