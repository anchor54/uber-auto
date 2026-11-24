import 'package:driver/app.dart';
import 'package:driver/core/clock.dart';
import 'package:driver/core/fake_api.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import '../test/helpers/d1_robot.dart';

/// Flow D1 on a real device or emulator, on the demo backend with its
/// network delay switched on and a clock the test holds at Sat 21 Nov 2026,
/// 10:00. The admin is played by the demo backend's hooks. Run with:
///   flutter test integration_test/d1_flow_test.dart -d <device>
void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  Future<D1Robot> launch(WidgetTester tester) async {
    final clock = ManualClock(d1Now);
    final api = FakeDriverApi(now: clock.now, seed: false, latency: const Duration(milliseconds: 300));
    final h = D1Harness(api, clock, TestImageSource());
    await tester.pumpWidget(DriverApp(api: api, clock: clock, imageSource: h.images));
    final robot = D1Robot(tester, h);
    await robot.settle();
    return robot;
  }

  testWidgets('D1 happy path: sign up to "You\'re live"', (tester) async {
    final robot = await launch(tester);
    await d1HappyPath(
      robot,
      approve: () async => robot.api.adminApprove(),
      confirmPayment: () async => robot.api.adminConfirmPayment(),
    );
    expect(find.text("No open requests right now. We'll notify you when a new one comes in."), findsOneWidget);
  });

  testWidgets('D1 changes needed, fixed, submitted again', (tester) async {
    final robot = await launch(tester);
    await robot.signUp();
    await robot.addAllDocuments();
    await robot.tap('continue_to_vehicle');
    await robot.fillVehicle();
    await robot.tap('continue');
    await robot.pickDayOff(7);
    await robot.tap('submit_for_review');
    expect(find.text("We're checking your details"), findsOneWidget);

    robot.api.adminRequestChanges('licence', 'photo blurred');
    await robot.reopenApp();
    expect(find.text('A few things need fixing'), findsOneWidget);
    await robot.tap('fix_now');
    await robot.uploadDoc('licence');
    await robot.tap('back');
    await robot.tap('submit_again');
    expect(find.text("We're checking your details"), findsOneWidget);
  });
}
