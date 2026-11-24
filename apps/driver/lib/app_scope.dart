import 'package:flutter/widgets.dart';

import 'core/api.dart';
import 'core/clock.dart';
import 'core/image_source.dart';

/// Gives every screen the backend, the clock, the photo picker and whether
/// we're in demo mode.
class AppScope extends InheritedWidget {
  const AppScope({
    super.key,
    required this.api,
    required this.clock,
    required this.demoMode,
    this.imageSource,
    this.refreshEvery,
    required super.child,
  });

  final DriverApi api;
  final Clock clock;
  final bool demoMode;

  /// Camera and gallery; null: the device's.
  final ImageSource? imageSource;

  static final ImageSource _device = DeviceImageSource();

  /// Where photos come from: [imageSource], else the device's camera and gallery.
  ImageSource get photos => imageSource ?? _device;

  /// How often waiting screens (Verification, Pay) read the driver again.
  /// Null: only when opened, on resume, on pull-down and on a push.
  final Duration? refreshEvery;

  static AppScope of(BuildContext context) {
    final scope = context.dependOnInheritedWidgetOfExactType<AppScope>();
    assert(scope != null, 'AppScope missing above $context');
    return scope!;
  }

  @override
  bool updateShouldNotify(AppScope oldWidget) =>
      api != oldWidget.api ||
      clock != oldWidget.clock ||
      demoMode != oldWidget.demoMode ||
      imageSource != oldWidget.imageSource ||
      refreshEvery != oldWidget.refreshEvery;
}
