import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

import '../helpers/app_harness.dart';
import '../helpers/r1_robot.dart';

/// The demo backend, failing the calls a test asks it to (real mode on a bad connection).
class FlakyApi extends FakeRiderApi {
  bool failSearch = false;
  bool failHome = false;
  ApiException? failPreview;
  int failCancel = 0;

  static const offline = ApiException('network', "Can't reach the server. Check your connection and try again.");

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

  @override
  Future<SeriesPreview> previewSeries(CommuteRequest request) async {
    final e = failPreview;
    if (e != null) throw e;
    return super.previewSeries(request);
  }

  @override
  Future<void> cancelRequest(String id) async {
    if (failCancel > 0) {
      failCancel--;
      throw offline;
    }
    return super.cancelRequest(id);
  }
}

void main() {
  testWidgets('R1.2 a start_too_soon error shows under "Starts"', (tester) async {
    final api = FlakyApi()..failPreview = const ApiException('start_too_soon', 'The first ride must be tomorrow or later.');
    await pumpRiderApp(tester, api: api);
    final robot = R1Robot(tester);
    await robot.signIn();
    await robot.fillSetup();
    expect(find.text('The first ride must be tomorrow or later.'), findsOneWidget);
    expect(find.text('Starts'), findsOneWidget);
  });

  testWidgets('R1.2 a failed place search shows the reason instead of crashing', (tester) async {
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

  testWidgets('R1.5 a failed cancel keeps the request and says why', (tester) async {
    final api = FlakyApi()..failCancel = 1;
    await pumpRiderApp(tester, api: api);
    final robot = R1Robot(tester);
    await robot.requestR1();
    await robot.cancelRequest();
    expect(find.text(FlakyApi.offline.message), findsOneWidget);
    expect(find.byKey(const Key('home.finding')), findsOneWidget);
    expect(api.series!.status, SeriesStatus.findingDriver);
  });

  testWidgets('R1.6b Home offline shows Try again, which reloads', (tester) async {
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
