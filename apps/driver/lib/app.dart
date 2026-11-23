import 'package:flutter/material.dart';

import 'app_scope.dart';
import 'core/api.dart';
import 'core/clock.dart';
import 'features/auth/start_screen.dart';
import 'ui/tokens.dart';

class DriverApp extends StatelessWidget {
  const DriverApp({super.key, required this.api, this.clock = const SystemClock(), this.demoMode = true});

  final DriverApi api;
  final Clock clock;
  final bool demoMode;

  @override
  Widget build(BuildContext context) {
    return AppScope(
      api: api,
      clock: clock,
      demoMode: demoMode,
      child: MaterialApp(
        title: 'Commute Driver',
        debugShowCheckedModeBanner: false,
        theme: buildDriverTheme(),
        home: const StartScreen(),
      ),
    );
  }
}
