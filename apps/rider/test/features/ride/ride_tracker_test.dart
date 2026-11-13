import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/clock.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';
import 'package:rider/features/ride/ride_tracker.dart';

import '../../helpers/app_harness.dart';

/// The demo backend, counting live-view requests, with a way to make the
/// driver's position go stale.
class CountingApi extends FakeRiderApi {
  CountingApi({required super.now});
  int liveCalls = 0;
  Duration? positionAge;

  @override
  Future<LiveRide> liveRide(String id) async {
    liveCalls++;
    final l = await super.liveRide(id);
    final age = positionAge;
    if (age == null || l.positionAt == null) return l;
    return LiveRide(
      state: l.state,
      serverTime: l.serverTime,
      driverAt: l.driverAt,
      positionAt: l.serverTime.subtract(age),
      etaMinutes: l.etaMinutes,
      etaAt: l.etaAt,
      waitEndsAt: l.waitEndsAt,
      late: l.late,
      pinLocked: l.pinLocked,
    );
  }
}

void main() {
  late DateTime now;
  late CountingApi api;
  late String rideId;

  /// An active commute with the next ride departing now.
  Future<void> setUpRide() async {
    now = testNow;
    api = CountingApi(now: () => now);
    final places = await api.searchPlaces('');
    final request = CommuteRequest(
      pickup: places.firstWhere((p) => p.id == 'home'),
      drop: places.firstWhere((p) => p.id == 'etv'),
      repeat: CommuteRepeat.everyWeekday,
      weekdays: {1, 2, 3, 4, 5},
      timing: TimingMode.pickupAt,
      time: 520,
      pickupTime: 520,
      returnTrip: true,
      returnTime: 1110,
      startDate: DateTime(2026, 10, 22),
      rideType: RideType.cabSedan,
      preferences: const {},
    );
    final match = (await api.findDrivers(request)).first;
    await api.confirmCommute(request: request, match: match, usualPayment: PaymentMethod.upi);
    api.playNextRide(at: now);
    rideId = (await api.upcomingRides()).first.id;
  }

  testWidgets('asks every 5 seconds and follows the state', (tester) async {
    await setUpRide();
    final t = RideTracker(api: api, rideId: rideId, clock: FixedClock(testNow));
    await t.start();
    expect(t.state, RideState.onTheWay);
    expect(t.ride!.pin, isNotNull, reason: 'the PIN shows once the driver has departed');
    expect(api.liveCalls, 1);

    await tester.pump(const Duration(seconds: 5));
    expect(api.liveCalls, 2);

    now = now.add(const Duration(minutes: 6));
    await tester.pump(const Duration(seconds: 5));
    expect(t.state, RideState.arrived);
    expect(t.live!.etaMinutes, 0);
    t.dispose();
  });

  testWidgets('stops in the background and asks straight away when back', (tester) async {
    await setUpRide();
    final t = RideTracker(api: api, rideId: rideId, clock: FixedClock(testNow));
    await t.start();
    t.didChangeAppLifecycleState(AppLifecycleState.paused);
    expect(t.polling, isFalse);
    await tester.pump(const Duration(seconds: 30));
    expect(api.liveCalls, 1);
    t.didChangeAppLifecycleState(AppLifecycleState.resumed);
    await tester.pump();
    expect(api.liveCalls, 2);
    expect(t.polling, isTrue);
    t.dispose();
  });

  testWidgets('says "Updating…" when the position is over 60 seconds old', (tester) async {
    await setUpRide();
    final t = RideTracker(api: api, rideId: rideId, clock: FixedClock(testNow));
    api.positionAge = const Duration(seconds: 59);
    await t.start();
    expect(t.updating, isFalse);
    api.positionAge = const Duration(seconds: 61);
    await t.poll();
    expect(t.updating, isTrue);
    expect(t.live!.driverAt, isNotNull, reason: 'keeps the last position');
    t.dispose();
  });

  testWidgets('stops for good once the ride is over', (tester) async {
    await setUpRide();
    final t = RideTracker(api: api, rideId: rideId, clock: FixedClock(testNow));
    await t.start();
    now = now.add(const Duration(minutes: 60));
    await tester.pump(const Duration(seconds: 5));
    expect(t.state, RideState.completed);
    expect(t.polling, isFalse);
    final calls = api.liveCalls;
    await tester.pump(const Duration(seconds: 30));
    expect(api.liveCalls, calls);
    t.dispose();
  });

  testWidgets("countdowns use the server's clock, not the phone's", (tester) async {
    await setUpRide();
    // The phone thinks it's an hour earlier than the server.
    final t = RideTracker(api: api, rideId: rideId, clock: FixedClock(testNow.subtract(const Duration(hours: 1))));
    await t.start();
    expect(t.serverNow(), now);
    t.dispose();
  });
}
