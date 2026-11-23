import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';
import '../helpers/series_fixtures.dart';

/// A city whose config allows only bikes and autos for regular rides.
class BikeAndAutoOnly extends FakeRiderApi {
  @override
  Future<List<RideTypeOption>> rideTypeOptions(CommuteRequest request) async => [
        for (final o in await super.rideTypeOptions(request))
          if (o.type == RideType.bike || o.type == RideType.auto) o,
      ];
}

/// The demo backend, failing series.create the way a test asks.
class FailingCreate extends FakeRiderApi {
  FailingCreate({super.latency});

  ApiException? failNext;
  int created = 0;

  @override
  Future<Series> createSeries(CommuteRequest request) async {
    final e = failNext;
    if (e != null) {
      failNext = null;
      throw e;
    }
    created++;
    return super.createSeries(request);
  }
}

/// R1.3 Choose a ride type and R1.4 Review and request (spec "Screen designs").
void main() {
  Future<R1Robot> openRideTypes(WidgetTester tester, {FakeRiderApi? api}) async {
    await pumpRiderApp(tester, api: api);
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.fillSetup();
    await robot.tap('setup.continue');
    return robot;
  }

  Future<R1Robot> openReview(WidgetTester tester, {FakeRiderApi? api, RideType type = RideType.cabSedan}) async {
    await pumpRiderApp(tester, api: api);
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.fillSetup();
    await robot.chooseRideType(type);
    return robot;
  }

  /// The demo backend's own fare for the demo commute.
  Future<int> fareFor(FakeRiderApi api, RideType type) async =>
      (await api.previewSeries(commuteRequest(rideType: type))).suggestedFare;

  group('R1.3 Choose a ride type', () {
    testWidgets('R1.3 header and copy', (tester) async {
      await openRideTypes(tester);
      expect(find.text('Step 2 of 3'), findsOneWidget);
      expect(find.text('Choose your ride type'), findsOneWidget);
      expect(find.text("You'll get the same type on every ride. Fares are per ride, paid to your driver."), findsOneWidget);
      expect(find.text('Ride types shown depend on your city and on the kind of booking.'), findsOneWidget);
      expect(find.text('AC on'), findsOneWidget);
      expect(find.text('Quiet ride'), findsOneWidget);
      expect(find.text('Woman driver'), findsOneWidget);
      expect(find.text('Review request'), findsOneWidget);
    });

    testWidgets('R1.3 lists every allowed type with its description and fare', (tester) async {
      final api = FakeRiderApi();
      await openRideTypes(tester, api: api);
      expect(find.text('Bike'), findsOneWidget);
      expect(find.text('1 rider · helmet provided'), findsOneWidget);
      expect(find.text('Auto'), findsOneWidget);
      expect(find.text('Up to 3 riders · open sides'), findsOneWidget);
      expect(find.text('CAB'), findsOneWidget);
      expect(find.text('Hatchback'), findsOneWidget);
      expect(find.text('4 seats · AC'), findsOneWidget);
      expect(find.text('Sedan'), findsOneWidget);
      expect(find.text('4 seats · AC · laptop bag fits'), findsOneWidget);
      expect(find.text('SUV'), findsOneWidget);
      expect(find.text('6 seats · AC · big boot'), findsOneWidget);
      expect(find.text('₹${await fareFor(api, RideType.bike)}'), findsWidgets);
      expect(find.text('₹${await fareFor(api, RideType.auto)}'), findsWidgets);
    });

    testWidgets('R1.3 says the request goes to every driver of the selected type', (tester) async {
      final robot = await openRideTypes(tester);
      await robot.tap('rideType.auto');
      expect(find.textContaining('Your request goes to every'), findsOneWidget);
      expect(find.textContaining('auto driver in', findRichText: true), findsOneWidget);
      // The old count of drivers on the route is gone.
      expect(find.textContaining('run your route'), findsNothing);
    });

    testWidgets('R1.3 the footer shows "{ride type} · per ride" and the fare', (tester) async {
      final api = FakeRiderApi();
      final robot = await openRideTypes(tester, api: api);
      await robot.tap('rideType.auto');
      expect(find.text('Auto · per ride'), findsOneWidget);
      expect(find.text('₹${await fareFor(api, RideType.auto)}'), findsWidgets);
    });

    testWidgets('R1.3 only shows the types the config allows for regular rides', (tester) async {
      await openRideTypes(tester, api: BikeAndAutoOnly());
      expect(find.text('Bike'), findsOneWidget);
      expect(find.text('Auto'), findsOneWidget);
      expect(find.text('CAB'), findsNothing);
      expect(find.byKey(const Key('rideType.cab_sedan')), findsNothing);
    });
  });

  group('R1.4 Review and request', () {
    testWidgets('R1.4 header, route, ride type, first ride and fare', (tester) async {
      final api = FakeRiderApi();
      await openReview(tester, api: api);
      expect(find.text('Step 3 of 3'), findsOneWidget);
      expect(find.text('Review and request'), findsOneWidget);
      expect(find.text("Check your regular ride. We'll find the driver after you send it."), findsOneWidget);
      expect(find.text('Your regular ride'), findsOneWidget);
      expect(find.textContaining('Home ⇄ Embassy Tech Village'), findsOneWidget);
      expect(find.textContaining('Cab · Sedan'), findsWidgets);
      expect(find.textContaining('22 Oct'), findsWidgets); // first ride: Thu 22 Oct
      expect(find.text('Fare per ride'), findsOneWidget);
      expect(find.text('₹${await fareFor(api, RideType.cabSedan)}'), findsWidgets);
      final p = await api.previewSeries(commuteRequest());
      expect(find.textContaining('About ${p.travelMinutes} min'), findsOneWidget);
    });

    testWidgets('R1.4 the schedule table groups days with the same times', (tester) async {
      await openReview(tester);
      expect(find.text('Days'), findsOneWidget);
      expect(find.text('Pickup'), findsOneWidget);
      expect(find.text('Return'), findsOneWidget);
      expect(find.text('Mon–Fri'), findsOneWidget);
      expect(find.text('8:40 AM'), findsOneWidget);
      expect(find.text('6:30 PM'), findsOneWidget);
    });

    testWidgets('R1.4 a one-way ride shows → instead of ⇄', (tester) async {
      await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      await robot.signIn();
      await robot.fillSetup();
      await robot.tap('setup.return');
      await robot.chooseRideType(RideType.cabSedan);
      expect(find.textContaining('Home → Embassy Tech Village'), findsOneWidget);
      expect(find.textContaining('⇄'), findsNothing);
    });

    testWidgets('R1.4 arrive-by is converted to a pickup time: arrival − trip − 10 min, down to 5 min', (tester) async {
      final api = FakeRiderApi();
      await pumpRiderApp(tester, api: api);
      final robot = R1Robot(tester);
      await robot.signIn();
      await robot.fillSetup();
      await robot.tap('segment.Get me there by'); // arrive by 9:30
      await robot.chooseRideType(RideType.cabSedan);
      final p = await api.previewSeries(commuteRequest(timing: TimingMode.arriveBy, time: 570));
      final raw = 570 - p.travelMinutes - 10;
      final pickup = raw - raw % 5;
      expect(p.slots.firstWhere((s) => s.direction == RideDirection.outbound).pickupTime, pickup);
      final h = pickup ~/ 60, m = pickup % 60;
      expect(find.text('$h:${m.toString().padLeft(2, '0')} AM'), findsOneWidget);
    });

    testWidgets('R1.4 "What happens next" panel', (tester) async {
      await openReview(tester);
      expect(find.text('What happens next'), findsOneWidget);
      expect(find.textContaining("see your request and say if they're interested."), findsOneWidget);
      expect(
        find.text("We'll notify you who your driver is, usually within 15 minutes. No need to keep the app open."),
        findsOneWidget,
      );
      expect(find.text('Nothing to pay now. You pay your driver after each ride.'), findsOneWidget);
    });

    testWidgets('R1.4 usual payment: UPI or Cash, UPI selected', (tester) async {
      await openReview(tester);
      expect(find.text("How you'll usually pay your driver"), findsOneWidget);
      expect(find.text('UPI'), findsOneWidget);
      expect(find.text('Cash'), findsOneWidget);
      expect(find.text('Request a regular driver'), findsOneWidget);
      expect(isEnabled(tester, 'review.request'), isTrue);
    });

    testWidgets('R1.4 no driver is promised: no names, no matches', (tester) async {
      await openReview(tester);
      expect(find.textContaining('Ravi'), findsNothing);
      expect(find.text('We found your driver'), findsNothing);
      expect(find.byKey(const Key('match.found')), findsNothing);
    });

    testWidgets('R1.4 requesting goes Home with the toast and sends the choices', (tester) async {
      final api = FakeRiderApi();
      final robot = await openReview(tester, api: api);
      await robot.request(pay: PaymentMethod.cash);
      expect(find.byKey(const Key('home.finding')), findsOneWidget);
      expect(find.text("Request sent. We'll let you know who your driver is."), findsOneWidget);
      final s = api.series!;
      expect(s.status, SeriesStatus.findingDriver);
      expect(s.usualPayment, PaymentMethod.cash);
      expect(s.rideType, RideType.cabSedan);
      expect(s.weekdays, {1, 2, 3, 4, 5});
      expect(s.returnTrip, isTrue);
      expect(s.pickup.id, 'home');
      expect(s.drop.id, 'etv');
      expect(s.slots.every((x) => x.driver == null), isTrue);
    });

    testWidgets('R1.4 while the request runs the button spins and cannot be tapped again', (tester) async {
      final api = FailingCreate(latency: const Duration(milliseconds: 500));
      final robot = await openReview(tester, api: api);
      await tester.ensureVisible(find.byKey(const Key('review.request')));
      await tester.tap(find.byKey(const Key('review.request')));
      await tester.pump(const Duration(milliseconds: 100));
      expect(isLoading(tester, 'review.request'), isTrue);
      expect(isEnabled(tester, 'review.request'), isFalse);
      await tester.tap(find.byKey(const Key('review.request')), warnIfMissed: false);
      await tester.pumpAndSettle();
      expect(api.created, 1);
      expect(robot.key('home.finding'), findsOneWidget);
    });

    testWidgets('R1.4 a network error keeps the screen and offers to retry', (tester) async {
      final api = FailingCreate()
        ..failNext = const ApiException('network', "Can't reach the server. Check your connection and try again.");
      final robot = await openReview(tester, api: api);
      await robot.tap('review.request');
      expect(find.text('Review and request'), findsOneWidget);
      expect(find.text("Can't reach the server. Check your connection and try again."), findsOneWidget);
      await robot.tap('review.request');
      expect(find.byKey(const Key('home.finding')), findsOneWidget);
      expect(api.created, 1);
    });

    testWidgets('R1.4 a 422 shows its message', (tester) async {
      final api = FailingCreate()
        ..failNext = const ApiException('start_too_soon', 'The first ride must be tomorrow or later.');
      final robot = await openReview(tester, api: api);
      await robot.tap('review.request');
      expect(find.text('The first ride must be tomorrow or later.'), findsOneWidget);
      expect(find.byKey(const Key('home.finding')), findsNothing);
    });

    testWidgets('R1.4 back keeps everything chosen so far', (tester) async {
      final robot = await openReview(tester);
      await robot.tap('back');
      expect(find.text('Choose your ride type'), findsOneWidget);
      await robot.tap('back');
      expect(find.text('New commute'), findsOneWidget);
      expect(find.text('Home · 14th Main, HSR Layout'), findsOneWidget);
      expect(find.text('Embassy Tech Village, Gate 3'), findsOneWidget);
    });
  });
}
