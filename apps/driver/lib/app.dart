import 'package:flutter/material.dart';

import 'app_scope.dart';
import 'core/api.dart';
import 'core/clock.dart';
import 'core/image_source.dart';
import 'features/auth/start_screen.dart';
import 'ui/tokens.dart';

class DriverApp extends StatelessWidget {
  const DriverApp({
    super.key,
    required this.api,
    this.clock = const SystemClock(),
    this.demoMode = true,
    this.imageSource,
    this.refreshEvery,
  });

  final DriverApi api;
  final Clock clock;
  final bool demoMode;

  /// Camera and gallery for documents and vehicle photos; null: the
  /// device's ([DeviceImageSource]). Tests pass their own.
  final ImageSource? imageSource;

  /// How often the verification and payment screens read the driver again
  /// while waiting on the admin. Null (tests): no timers.
  final Duration? refreshEvery;

  @override
  Widget build(BuildContext context) {
    return AppScope(
      api: api,
      clock: clock,
      demoMode: demoMode,
      imageSource: imageSource,
      refreshEvery: refreshEvery,
      child: MaterialApp(
        title: 'Commute Driver',
        debugShowCheckedModeBanner: false,
        theme: buildDriverTheme(),
        home: const StartScreen(),
      ),
    );
  }
}
