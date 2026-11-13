import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

/// Drives flow R1 the way a person would. Shared by the widget tests and
/// the on-device integration test so both exercise the same steps.
class R1Robot {
  R1Robot(this.tester);

  final WidgetTester tester;

  Finder key(String k) => find.byKey(Key(k));

  Future<void> tap(String k) async {
    final f = key(k);
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

  /// R1.2 with the demo commute: HSR Layout to Embassy Tech Village.
  Future<void> fillSetup() async {
    await tap('home.setup');
    await choosePlace('setup.pickup', 'HSR', 'home');
    await choosePlace('setup.drop', 'Embassy', 'etv');
  }

  Future<void> chooseRideType(RideType type, {Set<RidePreference> toggle = const {}}) async {
    await tap('setup.continue');
    await tap('rideType.${type.configKey}');
    for (final p in toggle) {
      await tap('pref.${p.name}');
    }
    await tap('rideType.continue');
  }

  Future<void> confirm({PaymentMethod pay = PaymentMethod.upi}) async {
    await tap('match.continue');
    await tap('confirm.pay.${pay.name}');
    await tap('confirm.submit');
  }

  /// The whole happy path from Welcome to Home with a commute.
  Future<void> completeR1({RideType type = RideType.cabSedan, PaymentMethod pay = PaymentMethod.upi}) async {
    await signIn();
    await fillSetup();
    await chooseRideType(type);
    expect(key('match.found'), findsOneWidget);
    await confirm(pay: pay);
    expect(key('home.commute'), findsOneWidget);
  }
}
