import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/fake_ride_script.dart';

import 'app_harness.dart';
import 'r1_robot.dart';

/// Drives flow R2 on the demo backend: an R1 commute, then a scripted ride
/// whose clock the test moves forward instead of waiting.
class R2Robot extends R1Robot {
  R2Robot._(super.tester, this.api, this._clock);

  final FakeRiderApi api;
  final _MutableNow _clock;

  DateTime get now => _clock.value;

  /// Signs in, books the demo commute (driver accepts at once), lands on Home.
  static Future<R2Robot> start(
    WidgetTester tester, {
    Size size = phoneSize,
    FakeRiderApi Function(DateTime Function() now)? backend,
  }) async {
    final clock = _MutableNow(testNow);
    DateTime now() => clock.value;
    final api = backend?.call(now) ?? FakeRiderApi(now: now);
    await pumpRiderApp(tester, api: api, size: size);
    final robot = R2Robot._(tester, api, clock);
    await robot.completeR1();
    return robot;
  }

  /// The next ride departs [inMinutes] from now and plays out as scripted.
  Future<void> playRide({int inMinutes = 0, FakeRideOutcome outcome = FakeRideOutcome.completed, int lateMinutes = 0}) async {
    api.playNextRide(at: now.add(Duration(minutes: inMinutes)), outcome: outcome, lateMinutes: lateMinutes);
    await refreshHome();
  }

  Future<void> refreshHome() async {
    await tester.fling(find.byKey(const Key('home.commute')), const Offset(0, 400), 1000);
    await tester.pumpAndSettle();
  }

  /// Moves the backend's clock and lets the screen's next 5-second check see it.
  Future<void> advance(int minutes) async {
    _clock.value = now.add(Duration(minutes: minutes));
    await tester.pump(const Duration(seconds: 5));
    await tester.pump();
    await tester.pump();
  }

  Future<void> trackRide() async {
    await tap('home.track');
    expect(key('ride.onTheWay'), findsOneWidget);
  }
}

class _MutableNow {
  _MutableNow(this.value);
  DateTime value;
}
