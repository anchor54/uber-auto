import 'package:driver/core/fake_api.dart';
import 'package:driver/core/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'driver_harness.dart';

/// Drives flow D2 (spec "R1 and D2: Create a Series and Get a Driver") the
/// way a driver would. Shared by widget, flow and on-device tests.
///
/// What the tests rely on in the driver app (package `driver`, structured
/// like apps/rider), agreed with the implementer:
///
/// App and screens: DriverApp({required DriverApi api, Clock clock}),
///   RequestsScreen (D2.1), RequestDetailScreen (D2.2), WeekScreen (D2.3).
/// core/clock.dart: Clock, SystemClock, ManualClock(value)..advance(d).
/// core/api.dart DriverApi:
///   Future<DriverProfile?> restoreSession(); Future<void> sendOtp(String phone);
///   Future<DriverProfile> verifyOtp(String phone, String code);
///   Future<OfferList> offers();                // offers.list
///   Future<Offer> offer(String id);            // offer.get
///   Future<void> markInterested(String id);    // offer.mark_interested
///   Future<void> withdrawInterest(String id);  // offer.withdraw_interest
///   Future<void> dismissOffer(String id);      // offer.dismiss ("Not for me")
///   Future<DriverWeek> week({DateTime? start}); // rides.week
/// core/models.dart:
///   OfferList{offers: List<Offer>, results: List<OfferResult>}
///   Offer{id, kind: OfferKind(series|slot|ride), seriesId, round, status: OfferStatus(open|closing|closed),
///     openedAt, expiresAt, canRespond, interested, dismissed, result: String? (picked|not_picked|clashed|withdrew|ineligible),
///     rider: OfferRider{firstName, name, rating}, pickup/drop: OfferPlace{label}, startDate, travelMinutes,
///     farePerRide, ridesPerMonth, farePerMonth, usualPayment: PaymentMethod(upi|cash), slots: List<OfferSlot>}
///   OfferSlot{weekday, direction: RideDirection(outbound|returnLeg), pickupTime (minutes), estimatedDropTime (minutes?)}
///   OfferResult{offerId, seriesId, result: String (picked|went_to_another_driver|rider_withdrew), message}
///   DriverWeek{weekStart, weekEnd, rides: List<WeekRide>}
///   WeekRide{id, seriesId, date, weekday, direction, pickupTime (minutes), riderName, fare, isNew}
///   ApiException(code, message, {state})
/// core/fake_api.dart FakeDriverApi({Duration latency, DateTime Function()? now, bool seed = true}):
///   demoOtp '123456', demoPhone '9000000201' (Ravi Kumar, auto).
///   Seed (opened at the clock's now): a series offer from Priya S. (rating 4.8),
///   Koramangala → Whitefield, Mon/Wed/Fri 08:30, no return, 15-minute window;
///   and a one-ride offer (5-minute window). An offer the driver is
///   interested in is won when its window passes (the demo pick); one they
///   aren't interested in leaves the list. Late mark/withdraw: 409 offer_closed.
///   Test hooks: goesToAnotherDriver(offerId), riderWithdraws(offerId).
/// core/http_api.dart HttpDriverApi({required String baseUrl, required SessionStore store, http.Client? client}).
/// core/session_store.dart SessionStore, MemorySessionStore{token}.
///
/// Keys (buttons only; everything else is found by the spec's text):
///   welcome.phone, welcome.sendOtp, otp.code        sign-in, as in the rider app
///   tab.today, tab.requests, tab.earnings, tab.account   bottom bar
///   requests.card.{offerId}         the "{rider} · details" link: opens D2.2 (or D2.4)
///   requests.interested.{offerId}   "I'm interested" / "Interested" on a card
///   requests.notForMe.{offerId}     "Not for me" on a card
///   requests.winBanner              the green win banner on D2.1 (opens D2.3)
///   detail.interested               "I'm interested" / "Closed" on D2.2
///   detail.notForMe                 "Not for me" on D2.2
///   detail.withdraw                 "Not interested any more" on D2.2
///   back                            the header's back arrow
class DriverRobot {
  DriverRobot(this.tester, this.h);

  final WidgetTester tester;
  final DriverHarness h;

  FakeDriverApi get api => h.api;

  Finder key(String k) => find.byKey(Key(k));

  Future<void> tap(String k) => tapFinder(key(k));

  Future<void> tapText(String text) => tapFinder(find.text(text).last);

  Future<void> tapFinder(Finder f) async {
    for (var i = 0; i < 50 && f.evaluate().isEmpty; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }
    if (f.evaluate().isEmpty) {
      await tester.scrollUntilVisible(f, 200, scrollable: find.byType(Scrollable).first);
    }
    await tester.ensureVisible(f);
    await tester.pumpAndSettle();
    await tester.tap(f);
    await tester.pumpAndSettle();
  }

  Future<void> type(String k, String text) async {
    await tester.enterText(key(k), text);
    await tester.pumpAndSettle();
  }

  Future<void> signIn({String phone = FakeDriverApi.demoPhone}) async {
    await type('welcome.phone', phone);
    await tap('welcome.sendOtp');
    await type('otp.code', FakeDriverApi.demoOtp);
    expect(key('tab.requests'), findsOneWidget);
  }

  /// D2.1: the Requests tab (read again each time it opens).
  Future<void> openRequests() async {
    await tap('tab.today');
    await tap('tab.requests');
    expect(find.text('Requests'), findsWidgets);
  }

  Future<void> signInToRequests() async {
    await signIn();
    await openRequests();
  }

  /// The seeded series request (the newest series offer).
  Future<Offer> seriesOffer() async => (await _offers()).offers.firstWhere((o) => o.kind == OfferKind.series);

  /// The seeded one-ride offer.
  Future<Offer> rideOffer() async => (await _offers()).offers.firstWhere((o) => o.kind == OfferKind.ride);

  /// Reads the offers outside the test's fake clock. With the demo latency
  /// on, the backend waits on a timer, and awaiting it directly in the test
  /// body would hang: nothing advances fake time while the test awaits.
  Future<OfferList> _offers() async => (await tester.runAsync(() => api.offers()))!;

  Future<void> interested(Offer o) => tap('requests.interested.${o.id}');

  Future<void> openDetail(Offer o) async {
    await tap('requests.card.${o.id}');
    expect(find.text("${o.rider.firstName}'s regular ride"), findsOneWidget);
  }

  /// Moves the shared clock and lets the screens tick.
  Future<void> advance(Duration d) async {
    h.clock.advance(d);
    await tester.pump(const Duration(seconds: 1));
    await tester.pumpAndSettle();
  }

  /// Moves the clock past [o]'s window (the sweep closes it within a
  /// minute) and opens Requests again.
  Future<void> closeWindow(Offer o) async {
    final left = o.expiresAt.difference(h.clock.now());
    await advance(left + const Duration(minutes: 1));
    await openRequests();
  }
}
