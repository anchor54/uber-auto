import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';

String textOf(WidgetTester tester, String key) => tester.widget<Text>(find.byKey(Key(key))).data!;

void main() {
  Future<R1Robot> openRideTypes(WidgetTester tester, {FakeRiderApi? api}) async {
    await pumpRiderApp(tester, api: api);
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.fillSetup();
    await robot.tap('setup.continue');
    return robot;
  }

  group('R1.3 Choose a ride type', () {
    testWidgets('lists every allowed type with its fare, sedan picked by default', (tester) async {
      await openRideTypes(tester);
      expect(find.text('Choose your ride type'), findsOneWidget);
      expect(find.text('Bike'), findsOneWidget);
      expect(find.text('Auto'), findsOneWidget);
      expect(find.text('CAB'), findsOneWidget);
      expect(find.text('₹90'), findsOneWidget);
      expect(find.text('₹160'), findsOneWidget);
      expect(textOf(tester, 'rideType.note'), '5 sedan drivers run your route at 8:40 AM');
      expect(textOf(tester, 'rideType.fare'), '₹250');
      expect(find.text('Cab · Sedan · per ride'), findsOneWidget);
    });

    testWidgets('picking Auto updates the fare and hides the AC preference', (tester) async {
      final robot = await openRideTypes(tester);
      expect(find.byKey(const Key('pref.acOn')), findsOneWidget);
      await robot.tap('rideType.auto');
      expect(textOf(tester, 'rideType.fare'), '₹160');
      expect(textOf(tester, 'rideType.note'), '4 auto drivers run your route at 8:40 AM');
      expect(find.byKey(const Key('pref.acOn')), findsNothing);
    });

    testWidgets('only shows the types the backend config allows', (tester) async {
      final api = FakeRiderApi(config: {
        ...defaultRideTypeConfig,
        BookingFlow.recurringWeekday: const RideTypeConfig(
          allowed: [RideType.bike, RideType.auto],
          defaultType: RideType.auto,
        ),
      });
      await openRideTypes(tester, api: api);
      expect(find.text('Bike'), findsOneWidget);
      expect(find.text('Auto'), findsOneWidget);
      expect(find.text('CAB'), findsNothing);
      expect(find.byKey(const Key('rideType.cab_sedan')), findsNothing);
      expect(textOf(tester, 'rideType.fare'), '₹160');
    });

    testWidgets('a city with no regular ride types says so', (tester) async {
      final api = FakeRiderApi(config: {
        ...defaultRideTypeConfig,
        BookingFlow.recurringWeekday: const RideTypeConfig(allowed: [], defaultType: null),
      });
      await openRideTypes(tester, api: api);
      expect(find.byKey(const Key('rideType.none')), findsOneWidget);
      expect(isEnabled(tester, 'rideType.continue'), isFalse);
    });
  });

  group('R1.4 Driver match', () {
    testWidgets('proposes Ravi with the route, reasons and schedule', (tester) async {
      final robot = await openRideTypes(tester);
      await robot.tap('rideType.continue');
      expect(find.text('We found your driver'), findsOneWidget);
      expect(textOf(tester, 'match.driverName'), 'Ravi Kumar');
      expect(textOf(tester, 'match.trip'), 'About 38 min · 12 km');
      expect(find.text('4.9 rating'), findsOneWidget);
      expect(find.text('Same driver every day, both directions'), findsOneWidget);
      expect(find.text('Morning'), findsOneWidget);
      expect(find.text('Evening'), findsOneWidget);
      expect(find.text('Mon–Fri'), findsNWidgets(2));
      expect(find.text('Fare per ride, paid to Ravi'), findsOneWidget);
      expect(textOf(tester, 'match.fare'), '₹250');
    });

    testWidgets('See other drivers lets the rider pick someone else', (tester) async {
      final robot = await openRideTypes(tester);
      await robot.tap('rideType.continue');
      await robot.tap('match.others');
      expect(find.text('Other drivers on your route'), findsOneWidget);
      await robot.tap('others.d_suresh');
      expect(textOf(tester, 'match.driverName'), 'Suresh M');
      expect(find.text('Fare per ride, paid to Suresh'), findsOneWidget);
    });

    testWidgets('no match offers to relax the preference, and that finds a driver', (tester) async {
      final robot = await openRideTypes(tester);
      await robot.tap('pref.womanDriver');
      await robot.tap('rideType.continue');
      expect(find.byKey(const Key('match.none')), findsOneWidget);
      expect(find.text('No driver yet'), findsOneWidget);
      expect(find.text('Try 8:55 AM instead'), findsOneWidget);
      await robot.tap('match.relax');
      expect(find.byKey(const Key('match.found')), findsOneWidget);
    });

    testWidgets('joining the waitlist returns Home and says so', (tester) async {
      final api = FakeRiderApi();
      final robot = await openRideTypes(tester, api: api);
      await robot.tap('pref.womanDriver');
      await robot.tap('rideType.continue');
      await robot.tap('match.waitlist');
      expect(find.byKey(const Key('home.empty')), findsOneWidget);
      expect(find.textContaining("You're on the waitlist"), findsOneWidget);
      expect(api.waitlist, hasLength(1));
    });
  });

  group('R1.5 Confirm the commute', () {
    Future<R1Robot> openConfirm(WidgetTester tester, {FakeRiderApi? api}) async {
      final robot = await openRideTypes(tester, api: api);
      await robot.tap('rideType.continue');
      await robot.tap('match.continue');
      return robot;
    }

    testWidgets('summarises the commute and explains payment', (tester) async {
      await openConfirm(tester);
      expect(find.text('Confirm your commute'), findsOneWidget);
      expect(textOf(tester, 'confirm.route'), 'Home ⇄ Embassy Tech Village');
      expect(textOf(tester, 'confirm.schedule'), 'Mon–Fri · 8:40 AM, 6:30 PM');
      expect(textOf(tester, 'confirm.driver'), 'Cab · Sedan · Ravi Kumar');
      expect(textOf(tester, 'confirm.firstRide'), 'Thu 22 Oct, 8:40 AM');
      expect(textOf(tester, 'confirm.fare'), '₹250');
      expect(find.text('Nothing to pay now. You pay Ravi directly after each ride.'), findsOneWidget);
      expect(find.text("How you'll usually pay Ravi"), findsOneWidget);
    });

    testWidgets('confirming saves the usual payment method and opens Home', (tester) async {
      final api = FakeRiderApi();
      final robot = await openConfirm(tester, api: api);
      await robot.tap('confirm.pay.cash');
      await robot.tap('confirm.submit');
      expect(find.byKey(const Key('home.commute')), findsOneWidget);
      expect(api.commute!.usualPayment, PaymentMethod.cash);
      expect(api.commute!.fare, 250);
      expect(api.commute!.request.weekdays, {1, 2, 3, 4, 5});
      expect(api.commute!.request.pickup.id, 'home');
      expect(api.commute!.request.drop.id, 'etv');
    });

    testWidgets('back from Confirm keeps everything chosen so far', (tester) async {
      final robot = await openConfirm(tester);
      await robot.tap('back');
      expect(textOf(tester, 'match.driverName'), 'Ravi Kumar');
      await robot.tap('back');
      await robot.tap('back');
      expect(find.text('Home · 14th Main, HSR Layout'), findsOneWidget);
      expect(find.text('Embassy Tech Village, Gate 3'), findsOneWidget);
    });
  });
}
