import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

import 'app_harness.dart';

/// Drives flow R1 (spec "R1 and D2: Create a Series and Get a Driver") the
/// way a person would. Shared by the widget tests and the on-device
/// integration tests so both exercise the same steps.
///
/// What the tests rely on in the app (agreed with the implementer):
///
/// Screens: NewCommuteScreen (R1.2), RideTypeScreen (R1.3),
/// ReviewRequestScreen (R1.4), HomeScreen (R1.5 / R1.6 / R1.6b / R1.7).
/// RiderApi: previewSeries, createSeries, getSeries, cancelRequest,
/// retrySeries; FakeRiderApi.series (the latest series, for tests).
///
/// Keys from the spec:
///   setup.sameTime                  "Same time every day" switch (R1.2)
///   setup.time.MON … setup.time.SUN per-day time row; tapping opens the picker
///   review.request                  "Request a regular driver" (R1.4)
///   home.finding                    the "Finding your driver" card (R1.5)
///   home.cancelRequest              "Cancel request" on the R1.5 / R1.7 card
///   noDriver.retry, noDriver.change "Try again", "Change times or ride type" (R1.7)
///   home.slot.MON … home.slot.SUN   the driver line of each weekday (R1.6)
/// Keys kept from the built app:
///   welcome.phone, welcome.sendOtp, otp.code, home.empty, home.setup,
///   setup.pickup, setup.drop, place.query, place.{id}, setup.day.{1-7},
///   setup.return ("Add return trip" switch), setup.starts, setup.ridesPerMonth,
///   setup.continue ("Choose ride type"), rideType.{configKey}, pref.{name},
///   rideType.continue ("Review request"), segment.{label}, back,
///   tab.home / tab.schedule / tab.account, home.nextTime, home.commute.
/// New button keys (not in the spec):
///   time.HH:MM                      an option in the R1.2 time picker (24 h,
///                                   5-minute steps); tapping it picks and closes
///   cancelRequest.confirm           "Cancel request" in the "Cancel this request?" dialog
///   cancelRequest.keep              the dialog's button that keeps the request
/// Everything else is found by its visible text from the spec.
class R1Robot {
  R1Robot(this.tester, {TestClock? clock}) : _clock = clock;

  final WidgetTester tester;
  final TestClock? _clock;

  TestClock get clock => _clock ?? harnessClock;

  Finder key(String k) => find.byKey(Key(k));

  Future<void> tap(String k) => tapFinder(key(k));

  Future<void> tapText(String text) => tapFinder(find.text(text).last);

  Future<void> tapFinder(Finder f) async {
    // Against a real backend, results arrive a little later (network, and
    // the 300 ms wait after typing in place search): give them 5 seconds.
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

  Future<void> signIn({String phone = '9886054321'}) async {
    await type('welcome.phone', phone);
    await tap('welcome.sendOtp');
    expect(key('otp.code'), findsOneWidget);
    await type('otp.code', FakeRiderApi.demoOtp);
    expect(key('home.empty'), findsOneWidget);
  }

  Future<void> choosePlace(String field, String query, String placeId) async {
    await tap(field);
    await type('place.query', query);
    await tap('place.$placeId');
  }

  /// R1.2 with the demo commute: HSR Layout to Embassy Tech Village,
  /// every weekday, pick up 8:40 AM, back at 6:30 PM.
  Future<void> fillSetup() async {
    await tap('home.setup');
    await choosePlace('setup.pickup', 'HSR', 'home');
    await choosePlace('setup.drop', 'Embassy', 'etv');
  }

  /// Turns "Same time every day" off, so each chosen weekday gets its row.
  Future<void> perDayTimes() => tap('setup.sameTime');

  /// Sets [day]'s time ("FRI") to [hhmm] ("09:30") in the 5-minute picker.
  Future<void> setDayTime(String day, String hhmm) async {
    await tap('setup.time.$day');
    await tap('time.$hhmm');
  }

  /// R1.2 → R1.3 → R1.4.
  Future<void> chooseRideType(RideType type, {Set<RidePreference> toggle = const {}}) async {
    await tap('setup.continue');
    await tap('rideType.${type.configKey}');
    for (final p in toggle) {
      await tap('pref.${p.name}');
    }
    await tap('rideType.continue');
    expect(find.text('Review and request'), findsOneWidget);
  }

  /// R1.4: picks the usual payment and taps "Request a regular driver".
  Future<void> request({PaymentMethod pay = PaymentMethod.upi}) async {
    await tapText(pay.label);
    await tap('review.request');
  }

  /// "Cancel request" on the Home card, then confirms in the dialog.
  Future<void> cancelRequest() async {
    await tap('home.cancelRequest');
    expect(find.text('Cancel this request?'), findsOneWidget);
    await tap('cancelRequest.confirm');
  }

  /// Reads Home again the way the rider does: leave Home and come back
  /// ("read again whenever Home opens"). Also pulls down to refresh when a
  /// request card is showing, in case Home was never left.
  Future<void> refreshHome() => reopenHome();

  Future<void> reopenHome() async {
    await tap('tab.schedule');
    await tap('tab.home');
    final card = key('home.finding');
    if (card.evaluate().isNotEmpty) {
      await tester.fling(card, const Offset(0, 400), 1000);
      await tester.pumpAndSettle();
    }
  }

  /// Moves the shared clock (one 15-minute round by default) and lets Home
  /// read the series again.
  Future<void> advance([Duration d = const Duration(minutes: 15)]) async {
    clock.advance(d);
    await reopenHome();
  }

  /// Sign in, set up the demo commute, request it; Home shows R1.5.
  Future<void> requestR1({
    RideType type = RideType.cabSedan,
    PaymentMethod pay = PaymentMethod.upi,
    Set<RidePreference> toggle = const {},
  }) async {
    await signIn();
    await fillSetup();
    await chooseRideType(type, toggle: toggle);
    await request(pay: pay);
    expect(key('home.finding'), findsOneWidget);
  }

  /// The whole happy path: request, the demo backend's first round closes
  /// 15 minutes later with a driver, Home shows R1.6.
  Future<void> completeR1({RideType type = RideType.cabSedan, PaymentMethod pay = PaymentMethod.upi}) async {
    await requestR1(type: type, pay: pay);
    await advance();
    expect(find.text('Driver confirmed'), findsOneWidget);
  }
}
