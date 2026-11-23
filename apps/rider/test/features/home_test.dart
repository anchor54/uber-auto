import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';
import 'package:rider/core/push.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';

/// Home in each series state (spec R1.5, R1.6, R1.6b, R1.7 and "ended").
/// The clock starts Wed 21 Oct 2026, 9:00 AM; the demo commute is HSR Layout
/// (Home) to Embassy Tech Village, Mon–Fri 8:40 AM, back at 6:30 PM, sedan.
void main() {
  group('R1.6b Home, no ride', () {
    testWidgets('R1.6b greets the rider and offers to set up a ride', (tester) async {
      await pumpRiderApp(tester);
      await R1Robot(tester).signIn();
      expect(find.textContaining('Good morning'), findsOneWidget);
      expect(find.text('Where to?'), findsOneWidget);
      expect(find.text('Now'), findsOneWidget);
      expect(find.byKey(const Key('home.setup')), findsOneWidget);
      expect(find.byKey(const Key('home.finding')), findsNothing);
    });
  });

  group('R1.5 Home: finding your driver', () {
    testWidgets('R1.5 the request card, with no rounds, names or counts', (tester) async {
      await pumpRiderApp(tester);
      await R1Robot(tester).requestR1();

      expect(find.text('Your commute'), findsOneWidget);
      final card = find.byKey(const Key('home.finding'));
      expect(card, findsOneWidget);
      Finder inCard(String text) => find.descendant(of: card, matching: find.text(text));
      Finder inCardContaining(String text) => find.descendant(of: card, matching: find.textContaining(text));

      expect(inCard('Requested today, 9:00 AM'), findsOneWidget);
      expect(inCard('Finding your driver'), findsOneWidget);
      expect(inCard('Mon–Fri · Home ⇄ Embassy Tech Village'), findsOneWidget);
      expect(inCard('8:40 AM · back at 6:30 PM · Cab · Sedan'), findsOneWidget);
      expect(inCardContaining("We've sent your request to "), findsOneWidget);
      expect(
        inCardContaining("drivers. We'll notify you as soon as your driver is confirmed, usually within 15 minutes."),
        findsOneWidget,
      );
      expect(inCard('View request'), findsOneWidget);
      expect(inCard('Cancel request'), findsOneWidget);
      expect(find.byKey(const Key('home.cancelRequest')), findsOneWidget);

      expect(find.text('This week'), findsOneWidget);
      expect(find.textContaining('Your rides start '), findsOneWidget);
      expect(find.textContaining('22 Oct, once your driver is confirmed.'), findsOneWidget);

      // Open question 6: no rounds, driver names, counts or countdowns.
      expect(find.textContaining('Round'), findsNothing);
      expect(find.textContaining('round'), findsNothing);
      expect(find.textContaining('Ravi'), findsNothing);
      expect(find.textContaining('interested'), findsNothing);
      expect(find.textContaining('Closes in'), findsNothing);
      expect(find.text('Driver confirmed'), findsNothing);
    });

    testWidgets('R1.5 per-day exceptions show in the time line', (tester) async {
      await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      await robot.signIn();
      await robot.fillSetup();
      await robot.perDayTimes();
      await robot.setDayTime('FRI', '09:30');
      await robot.chooseRideType(RideType.cabSedan);
      await robot.request();
      expect(find.text('8:40 AM (Fri 9:30 AM) · back at 6:30 PM · Cab · Sedan'), findsOneWidget);
    });

    testWidgets('R1.5 is still showing before the round closes', (tester) async {
      await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      await robot.requestR1();
      await robot.advance(const Duration(minutes: 14));
      expect(find.byKey(const Key('home.finding')), findsOneWidget);
      expect(find.text('Finding your driver'), findsOneWidget);
    });

    testWidgets('R1.5 "View request" opens R1.4 read-only', (tester) async {
      await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      await robot.requestR1();
      await robot.tapText('View request');
      expect(find.text('Review and request'), findsOneWidget);
      expect(find.textContaining('Home ⇄ Embassy Tech Village'), findsOneWidget);
      expect(find.byKey(const Key('review.request')), findsNothing);
      await robot.tap('back');
      expect(find.byKey(const Key('home.finding')), findsOneWidget);
    });

    testWidgets('R1.5 "Cancel request" asks first; keeping it changes nothing', (tester) async {
      final api = await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      await robot.requestR1();
      await robot.tap('home.cancelRequest');
      expect(find.text('Cancel this request?'), findsOneWidget);
      await robot.tap('cancelRequest.keep');
      expect(find.byKey(const Key('home.finding')), findsOneWidget);
      expect(api.series!.status, SeriesStatus.findingDriver);
    });

    testWidgets('R1.5 cancelling ends the series and Home offers set-up again', (tester) async {
      final api = await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      await robot.requestR1();
      await robot.cancelRequest();
      expect(api.series!.status, SeriesStatus.ended);
      expect(api.series!.offer!.outcome, OfferOutcome.cancelled);
      expect(find.byKey(const Key('home.finding')), findsNothing);
      expect(find.byKey(const Key('home.setup')), findsOneWidget);
      // Ended stays ended when Home opens again.
      await robot.advance();
      expect(find.byKey(const Key('home.setup')), findsOneWidget);
      expect(find.text('Driver confirmed'), findsNothing);
    });

    testWidgets('R1.5 a cancel that loses the race to the pick says who was assigned', (tester) async {
      final api = await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      await robot.requestR1();
      // The round closes and Ravi is picked, but Home hasn't read it yet.
      robot.clock.advance(const Duration(minutes: 15));
      await robot.cancelRequest();
      expect(find.text('Ravi was just assigned. Keep him, or end the series?'), findsOneWidget);
      expect(api.series!.status, SeriesStatus.active);
    });

    testWidgets('R1.5 a result push makes Home read the series again', (tester) async {
      final push = FakePush();
      final api = await pumpRiderApp(tester, push: push);
      final robot = R1Robot(tester);
      await robot.requestR1();
      robot.clock.advance(const Duration(minutes: 15));
      push.deliver(PushEvent(kind: 'series_active', seriesId: api.series!.id, opened: false));
      await tester.pumpAndSettle();
      expect(find.text('Driver confirmed'), findsOneWidget);
    });
  });

  group('R1.6 Home: driver confirmed', () {
    testWidgets('R1.6 next ride card with the driver per weekday', (tester) async {
      await pumpRiderApp(tester);
      await R1Robot(tester).completeR1();

      expect(find.byKey(const Key('home.finding')), findsNothing);
      expect(find.text('Driver confirmed'), findsOneWidget);
      // Booked Wed 21 Oct; the first ride is tomorrow, Thu 22 Oct.
      expect(find.textContaining('Tomorrow'), findsWidgets);
      expect(find.textContaining('22 Oct'), findsWidgets);
      expect(tester.widget<Text>(find.byKey(const Key('home.nextTime'))).data, contains('8:40'));
      expect(find.textContaining('Home → Embassy Tech Village'), findsOneWidget);
      expect(find.text('RK'), findsOneWidget);
      expect(find.text('Ravi Kumar · Cab · Sedan'), findsOneWidget);
      expect(find.text('White Dzire · KA 01 AB 1234 · your driver Mon–Fri'), findsOneWidget);
      for (final d in ['MON', 'TUE', 'WED', 'THU', 'FRI']) {
        expect(find.byKey(Key('home.slot.$d')), findsOneWidget, reason: d);
      }
      // One line per weekday: "Mon: Ravi · … KA 01 AB 1234".
      expect(
        find.descendant(of: find.byKey(const Key('home.slot.MON')), matching: find.textContaining('Ravi', findRichText: true)),
        findsOneWidget,
      );
      expect(
        find.descendant(of: find.byKey(const Key('home.slot.FRI')), matching: find.textContaining('KA 01 AB 1234', findRichText: true)),
        findsOneWidget,
      );
      expect(find.byKey(const Key('home.slot.SAT')), findsNothing);
      expect(find.byKey(const Key('home.slot.SUN')), findsNothing);
      expect(find.text('This week'), findsOneWidget);
      expect(find.text('Full schedule'), findsOneWidget);
    });

    testWidgets('R1.6 an active series stays active when Home opens again', (tester) async {
      await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      await robot.completeR1();
      await robot.advance(const Duration(hours: 1));
      expect(find.text('Driver confirmed'), findsOneWidget);
    });
  });

  group('R1.7 Home: no driver yet', () {
    Future<(FakeRiderApi, R1Robot)> noDriver(WidgetTester tester) async {
      final api = await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      // "Woman driver": nobody is interested in the demo backend.
      await robot.requestR1(toggle: {RidePreference.womanDriver});
      for (var round = 1; round <= FakeRiderApi.maxRounds; round++) {
        await robot.advance();
      }
      return (api, robot);
    }

    testWidgets('R1.7 is still finding after one and two empty rounds', (tester) async {
      await pumpRiderApp(tester);
      final robot = R1Robot(tester);
      await robot.requestR1(toggle: {RidePreference.womanDriver});
      await robot.advance();
      expect(find.text('Finding your driver'), findsOneWidget);
      await robot.advance();
      expect(find.text('Finding your driver'), findsOneWidget);
      expect(find.text('No driver yet'), findsNothing);
    });

    testWidgets('R1.7 after three empty rounds: "No driver yet" with its three actions', (tester) async {
      final (api, _) = await noDriver(tester);
      expect(api.series!.status, SeriesStatus.noDriverFound);
      expect(find.text('No driver yet'), findsOneWidget);
      expect(find.text('Finding your driver'), findsNothing);
      expect(
        find.textContaining(
            'driver could take every one of these rides yet. Try again, or change the times or ride type to reach more drivers.'),
        findsOneWidget,
      );
      expect(find.textContaining(RegExp(r'^No (Sedan|sedan|Cab · Sedan) driver could')), findsOneWidget);
      expect(find.text('Try again'), findsOneWidget);
      expect(find.text('Change times or ride type'), findsOneWidget);
      expect(find.text('Cancel request'), findsOneWidget);
      expect(find.byKey(const Key('noDriver.retry')), findsOneWidget);
      expect(find.byKey(const Key('noDriver.change')), findsOneWidget);
    });

    testWidgets('R1.7 "Try again" starts a new set of rounds: back to R1.5', (tester) async {
      final (api, robot) = await noDriver(tester);
      await robot.tap('noDriver.retry');
      expect(api.series!.status, SeriesStatus.findingDriver);
      expect(api.series!.offer!.round, 1);
      expect(find.byKey(const Key('home.finding')), findsOneWidget);
      expect(find.text('Finding your driver'), findsOneWidget);
    });

    testWidgets('R1.7 "Change times or ride type" goes R1.2 → R1.3 → R1.4 and tries again with the changes', (tester) async {
      final (api, robot) = await noDriver(tester);
      final id = api.series!.id;
      await robot.tap('noDriver.change');
      // R1.2 filled in.
      expect(find.text('New commute'), findsOneWidget);
      expect(find.text('Home · 14th Main, HSR Layout'), findsOneWidget);
      expect(find.text('Embassy Tech Village, Gate 3'), findsOneWidget);
      await robot.tap('setup.continue');
      expect(find.text('Choose your ride type'), findsOneWidget);
      await robot.tap('pref.womanDriver'); // off
      await robot.tap('rideType.continue');
      expect(find.text('Review and request'), findsOneWidget);
      expect(find.text('Try again with these changes'), findsOneWidget);
      expect(find.text('Request a regular driver'), findsNothing);
      await robot.tap('review.request');

      expect(api.series!.id, id, reason: 'the same series is retried, not a new one');
      expect(api.allSeries, hasLength(1));
      expect(api.series!.status, SeriesStatus.findingDriver);
      expect(api.series!.preferences, isNot(contains(RidePreference.womanDriver)));
      expect(find.byKey(const Key('home.finding')), findsOneWidget);

      await robot.advance();
      expect(find.text('Driver confirmed'), findsOneWidget);
    });

    testWidgets('R1.7 "Cancel request" ends the series', (tester) async {
      final (api, robot) = await noDriver(tester);
      await robot.cancelRequest();
      expect(api.series!.status, SeriesStatus.ended);
      expect(find.byKey(const Key('home.setup')), findsOneWidget);
      expect(find.text('No driver yet'), findsNothing);
    });
  });

  testWidgets('a type with no eligible driver goes straight to R1.7, with no waiting', (tester) async {
    final api = await pumpRiderApp(tester, api: FakeRiderApi(noDriverTypes: {RideType.cabSuv}));
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.fillSetup();
    await robot.chooseRideType(RideType.cabSuv);
    await robot.request();
    expect(api.series!.status, SeriesStatus.noDriverFound);
    expect(find.text('No driver yet'), findsOneWidget);
  });
}
