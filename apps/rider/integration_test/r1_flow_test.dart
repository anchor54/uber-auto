import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:rider/app.dart';
import 'package:rider/core/clock.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

import '../test/helpers/r1_robot.dart';

/// Flow R1 on a real device or emulator, with the demo backend's network
/// delay switched on and a clock the test moves. Run with:
///   flutter test integration_test/r1_flow_test.dart -d <device>
void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  Future<(FakeRiderApi, R1Robot)> launch(WidgetTester tester) async {
    final clock = ManualClock(DateTime(2026, 10, 21, 9));
    final api = FakeRiderApi(latency: const Duration(milliseconds: 300));
    api.syncClock(clock.now);
    await tester.pumpWidget(RiderApp(api: api, clock: clock));
    await tester.pumpAndSettle();
    return (api, R1Robot(tester, clock: clock));
  }

  testWidgets('R1 happy path: request, finding, the driver after 15 minutes', (tester) async {
    final (api, robot) = await launch(tester);
    await robot.requestR1();
    expect(find.text('Finding your driver'), findsOneWidget);
    await robot.advance();
    expect(find.text('Driver confirmed'), findsOneWidget);
    expect(api.series!.status, SeriesStatus.active);
  });

  testWidgets('R1 no-driver path: "No driver yet" and Try again', (tester) async {
    final (api, robot) = await launch(tester);
    await robot.requestR1(toggle: {RidePreference.womanDriver});
    await robot.advance(const Duration(minutes: 45));
    expect(find.text('No driver yet'), findsOneWidget);
    await robot.tap('noDriver.retry');
    expect(find.text('Finding your driver'), findsOneWidget);
    expect(api.series!.status, SeriesStatus.findingDriver);
  });

  testWidgets('R1 cancel the request', (tester) async {
    final (api, robot) = await launch(tester);
    await robot.requestR1();
    await robot.cancelRequest();
    expect(api.series!.status, SeriesStatus.ended);
    expect(find.byKey(const Key('home.setup')), findsOneWidget);
  });
}
