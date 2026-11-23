import 'package:driver/core/fake_api.dart';
import 'package:driver/core/models.dart';
import 'package:flutter_test/flutter_test.dart';

/// The driver demo backend plays D2 of backend/API.md on its clock.
void main() {
  late DateTime now;
  late FakeDriverApi api;
  setUp(() {
    now = DateTime(2026, 11, 19, 10);
    api = FakeDriverApi(now: () => now);
  });

  Future<Offer> series() async => (await api.offers()).offers.firstWhere((o) => o.kind == OfferKind.series);

  test('sign-in takes only the demo code', () async {
    await api.sendOtp(FakeDriverApi.demoPhone);
    await expectLater(api.verifyOtp(FakeDriverApi.demoPhone, '000000'),
        throwsA(isA<ApiException>().having((e) => e.code, 'code', 'invalid_code')));
    await api.verifyOtp(FakeDriverApi.demoPhone, FakeDriverApi.demoOtp);
  });

  test('the seed: a series offer with a 15-minute window and a one-ride offer with 5', () async {
    final list = await api.offers();
    final s = list.offers.firstWhere((o) => o.kind == OfferKind.series);
    final r = list.offers.firstWhere((o) => o.kind == OfferKind.ride);
    expect(s.expiresAt.difference(s.openedAt), const Duration(minutes: 15));
    expect(r.expiresAt.difference(r.openedAt), const Duration(minutes: 5));
    expect(s.status, OfferStatus.open);
    expect(s.canRespond, isTrue);
    expect(s.interested, isFalse);
    expect(s.round, 1);
    expect(s.rider.firstName, 'Priya');
    expect(s.slots, isNotEmpty);
    expect(s.farePerRide, greaterThan(0));
    expect(s.farePerMonth, s.farePerRide * s.ridesPerMonth);
    expect(list.results, isEmpty);
  });

  test('empty when not seeded', () async {
    final list = await FakeDriverApi(now: () => now, seed: false).offers();
    expect(list.offers, isEmpty);
    expect(list.results, isEmpty);
  });

  test('mark and withdraw interest; marking twice or withdrawing without interest is a no-op', () async {
    final o = await series();
    await api.markInterested(o.id);
    await api.markInterested(o.id);
    expect((await api.offer(o.id)).interested, isTrue);
    await api.withdrawInterest(o.id);
    await api.withdrawInterest(o.id);
    expect((await api.offer(o.id)).interested, isFalse);
  });

  test('after expires_at: 409 offer_closed with the offer as state; canRespond false', () async {
    final o = await series();
    now = o.expiresAt.add(const Duration(seconds: 1));
    await expectLater(
      api.markInterested(o.id),
      throwsA(isA<ApiException>()
          .having((e) => e.code, 'code', 'offer_closed')
          .having((e) => e.state, 'state', isA<Offer>())),
    );
    await expectLater(api.withdrawInterest(o.id), throwsA(isA<ApiException>().having((e) => e.code, 'code', 'offer_closed')));
  });

  test('dismiss: gone from the list for good; offer.get on an unknown id is not_found', () async {
    final o = await series();
    await api.dismissOffer(o.id);
    expect((await api.offers()).offers.map((x) => x.id), isNot(contains(o.id)));
    await expectLater(api.offer('nope'), throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_found')));
    await expectLater(api.dismissOffer('nope'), throwsA(isA<ApiException>().having((e) => e.code, 'code', 'not_found')));
  });

  test('interested when the window passes: picked, a result card and rides flagged new', () async {
    final o = await series();
    await api.markInterested(o.id);
    now = o.expiresAt.add(const Duration(minutes: 1));
    final list = await api.offers();
    expect(list.offers.map((x) => x.id), isNot(contains(o.id)));
    final res = list.results.singleWhere((r) => r.offerId == o.id);
    expect(res.result, 'picked');
    expect((await api.offer(o.id)).result, 'picked');
    final week = await api.week(start: o.startDate);
    expect(week.weekStart, o.startDate);
    expect(week.rides, isNotEmpty);
    expect(week.rides.every((r) => r.isNew && r.seriesId == o.seriesId), isTrue);
    expect(week.rides.map((r) => r.weekday).toSet(), o.slots.map((s) => s.weekday).toSet());
  });

  test('goesToAnotherDriver: "Went to another driver", kept 24 hours', () async {
    final o = await series();
    await api.markInterested(o.id);
    api.goesToAnotherDriver(o.id);
    final res = (await api.offers()).results.singleWhere((r) => r.offerId == o.id);
    expect(res.result, 'went_to_another_driver');
    expect(res.message, 'Went to another driver');
    now = now.add(const Duration(hours: 23));
    expect((await api.offers()).results.where((r) => r.offerId == o.id), hasLength(1));
    now = now.add(const Duration(hours: 2));
    expect((await api.offers()).results.where((r) => r.offerId == o.id), isEmpty);
  });

  test('riderWithdraws: "The rider withdrew this request"', () async {
    final o = await series();
    await api.markInterested(o.id);
    api.riderWithdraws(o.id);
    final res = (await api.offers()).results.singleWhere((r) => r.offerId == o.id);
    expect(res.result, 'rider_withdrew');
    expect(res.message, 'The rider withdrew this request');
  });

  test('a request the driver ignored leaves no result card', () async {
    final o = await series();
    now = o.expiresAt.add(const Duration(minutes: 1));
    final list = await api.offers();
    expect(list.offers.map((x) => x.id), isNot(contains(o.id)));
    expect(list.results, isEmpty);
  });
}
