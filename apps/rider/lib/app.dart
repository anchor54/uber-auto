import 'package:flutter/material.dart';

import 'app_scope.dart';
import 'core/api.dart';
import 'core/clock.dart';
import 'features/auth/welcome_screen.dart';
import 'ui/tokens.dart';

class RiderApp extends StatelessWidget {
  const RiderApp({super.key, required this.api, this.clock = const SystemClock(), this.demoMode = true});

  final RiderApi api;
  final Clock clock;
  final bool demoMode;

  @override
  Widget build(BuildContext context) {
    return AppScope(
      api: api,
      clock: clock,
      demoMode: demoMode,
      child: MaterialApp(
        title: 'Commute',
        debugShowCheckedModeBanner: false,
        theme: buildRiderTheme(),
        home: const WelcomeScreen(),
      ),
    );
  }
}
