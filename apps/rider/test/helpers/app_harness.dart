import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rider/app.dart';
import 'package:rider/core/clock.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/push.dart';
import 'package:rider/ui/widgets.dart';

/// Wednesday 21 Oct 2026, 9:00 AM. Every widget test runs at this time so
/// dates on screen are predictable.
final testNow = DateTime(2026, 10, 21, 9);

/// Phone-sized viewport (iPhone 14: 390 x 844 logical pixels).
const phoneSize = Size(390, 844);

Future<FakeRiderApi> pumpRiderApp(
  WidgetTester tester, {
  FakeRiderApi? api,
  Size size = phoneSize,
  DateTime? now,
  PushService push = const NoPush(),
}) async {
  tester.view.physicalSize = size * 3;
  tester.view.devicePixelRatio = 3;
  addTearDown(tester.view.reset);
  final fake = api ?? FakeRiderApi();
  final clock = FixedClock(now ?? testNow);
  fake.syncClock(clock.now);
  await tester.pumpWidget(RiderApp(api: fake, clock: clock, push: push));
  await tester.pumpAndSettle();
  return fake;
}

/// Whether the [PrimaryButton] with [key] can be tapped.
bool isEnabled(WidgetTester tester, String key) =>
    tester.widget<PrimaryButton>(find.byKey(Key(key))).onPressed != null;
