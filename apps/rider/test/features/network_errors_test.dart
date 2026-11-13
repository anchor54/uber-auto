import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';

/// The demo backend, failing the calls a test asks it to (real mode on a bad connection).
class FlakyApi extends FakeRiderApi {
  int failFindDrivers = 0;
  bool failSearch = false;
  bool failHome = false;

  static const offline = ApiException('network', "Can't reach the server. Check your connection and try again.");

  @override
  Future<List<DriverMatch>> findDrivers(CommuteRequest request) async {
    if (failFindDrivers > 0) {
      failFindDrivers--;
      throw offline;
    }
    return super.findDrivers(request);
  }

  @override
  Future<List<Place>> searchPlaces(String query) async {
    if (failSearch) throw offline;
    return super.searchPlaces(query);
  }

  @override
  Future<HomeData> home() async {
    if (failHome) throw offline;
    return super.home();
  }
}

void main() {
  testWidgets('a failed driver search says so and can be retried', (tester) async {
    final api = FlakyApi();
    await pumpRiderApp(tester, api: api);
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.fillSetup();
    api.failFindDrivers = 1;
    await robot.chooseRideType(RideType.cabSedan);
    expect(find.byKey(const Key('match.error')), findsOneWidget);
    expect(find.text(FlakyApi.offline.message), findsOneWidget);
    await robot.tap('match.retry');
    expect(find.byKey(const Key('match.found')), findsOneWidget);
  });

  testWidgets('a failed place search shows the reason instead of crashing', (tester) async {
    final api = FlakyApi();
    await pumpRiderApp(tester, api: api);
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.tap('home.setup');
    await robot.tap('setup.pickup');
    api.failSearch = true;
    await robot.type('place.query', 'HSR');
    expect(find.text(FlakyApi.offline.message), findsOneWidget);
    api.failSearch = false;
    await robot.type('place.query', 'HSRL');
    await robot.type('place.query', 'HSR');
    expect(find.byKey(const Key('place.home')), findsOneWidget);
  });

  testWidgets('Home offline shows Try again, which reloads', (tester) async {
    final api = FlakyApi();
    await pumpRiderApp(tester, api: api);
    final robot = R1Robot(tester);
    await robot.type('welcome.phone', '9886054321');
    await robot.tap('welcome.sendOtp');
    api.failHome = true;
    await robot.type('otp.code', FakeRiderApi.demoOtp);
    expect(find.byKey(const Key('home.error')), findsOneWidget);
    api.failHome = false;
    await robot.tap('home.retry');
    expect(find.byKey(const Key('home.empty')), findsOneWidget);
  });
}
