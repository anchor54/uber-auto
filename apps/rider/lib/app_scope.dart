import 'package:flutter/widgets.dart';

import 'core/api.dart';
import 'core/clock.dart';
import 'core/push.dart';

/// Gives every screen the backend, the clock and whether we're in demo mode.
class AppScope extends InheritedWidget {
  const AppScope({
    super.key,
    required this.api,
    required this.clock,
    required this.demoMode,
    this.push = const NoPush(),
    required super.child,
  });

  final RiderApi api;
  final Clock clock;
  final bool demoMode;
  final PushService push;

  static AppScope of(BuildContext context) {
    final scope = context.dependOnInheritedWidgetOfExactType<AppScope>();
    assert(scope != null, 'AppScope missing above $context');
    return scope!;
  }

  @override
  bool updateShouldNotify(AppScope oldWidget) =>
      api != oldWidget.api || clock != oldWidget.clock || demoMode != oldWidget.demoMode || push != oldWidget.push;
}
