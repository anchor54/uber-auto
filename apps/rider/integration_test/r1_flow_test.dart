import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:rider/app.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

import '../test/helpers/r1_robot.dart';

/// Flow R1 on a real device or emulator, with the demo backend's network
/// delay switched on. Run with: flutter test integration_test -d <device>
void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  Future<FakeRiderApi> launch(WidgetTester tester) async {
    final api = FakeRiderApi(latency: const Duration(milliseconds: 300));
    await tester.pumpWidget(RiderApp(api: api));
    await tester.pumpAndSettle();
    return api;
  }

  testWidgets('R1 happy path: sign in, set up a weekday commute, confirm', (tester) async {
    final api = await launch(tester);
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.fillSetup();
    await robot.chooseRideType(RideType.cabSedan);
    expect(find.byKey(const Key('match.found')), findsOneWidget);
    await robot.confirm(pay: PaymentMethod.upi);
    expect(find.byKey(const Key('home.commute')), findsOneWidget);
    expect(api.commute!.driver.name, 'Ravi Kumar');
  });

  testWidgets('R1 no-driver branch: relax the preference, then waitlist path', (tester) async {
    final api = await launch(tester);
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.fillSetup();
    await robot.chooseRideType(RideType.cabSedan, toggle: {RidePreference.womanDriver});
    expect(find.byKey(const Key('match.none')), findsOneWidget);
    await robot.tap('match.waitlist');
    expect(find.byKey(const Key('home.empty')), findsOneWidget);
    expect(api.waitlist, hasLength(1));
  });
}
