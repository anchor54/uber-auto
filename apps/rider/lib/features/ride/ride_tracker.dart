import 'dart:async';

import 'package:flutter/widgets.dart';

import '../../core/api.dart';
import '../../core/clock.dart';
import '../../core/models.dart';

/// Follows one ride while its screen is open: asks the backend for the live
/// view every [every], and the full ride again whenever the state changes.
///
/// * Stops asking while the app is in the background, and asks straight
///   away when it comes back.
/// * Stops for good once the ride is over (completed, no-show, …).
/// * [updating] turns on when the driver's position is more than
///   [staleAfter] old: the screen keeps the last position and says
///   "Updating…" instead of showing a frozen ETA.
/// * Countdowns use [serverNow], so a phone with a wrong clock still shows
///   the right time left.
class RideTracker extends ChangeNotifier with WidgetsBindingObserver {
  RideTracker({
    required this.api,
    required this.rideId,
    required this.clock,
    this.every = const Duration(seconds: 5),
    this.staleAfter = const Duration(seconds: 60),
  });

  final RiderApi api;
  final String rideId;
  final Clock clock;
  final Duration every;
  final Duration staleAfter;

  Ride? ride;
  LiveRide? live;
  Object? error;
  Timer? _timer;
  bool _started = false;
  bool _disposed = false;
  bool _inFlight = false;
  Duration _serverOffset = Duration.zero;

  /// The state the screen should show: the live view's when we have one.
  RideState? get state => live?.state ?? ride?.state;

  bool get polling => _timer != null;

  /// The backend's clock, estimated from its last answer.
  DateTime serverNow() => clock.now().add(_serverOffset);

  /// The driver is moving but their position is old.
  bool get updating {
    final l = live;
    if (l == null || !(l.state == RideState.onTheWay || l.state == RideState.inRide)) return false;
    final at = l.positionAt;
    return at == null || l.serverTime.difference(at) > staleAfter;
  }

  Future<void> start() async {
    if (_started) return;
    _started = true;
    WidgetsBinding.instance.addObserver(this);
    try {
      ride = await api.ride(rideId);
      _notify();
    } on ApiException catch (e) {
      error = e;
      _notify();
      return;
    }
    await poll();
    _schedule();
  }

  /// Asks for the live view now. Safe to call any time.
  Future<void> poll() async {
    if (_inFlight || _disposed) return;
    _inFlight = true;
    try {
      final next = await api.liveRide(rideId);
      if (_disposed) return;
      _serverOffset = next.serverTime.difference(clock.now());
      final changed = next.state != ride?.state || next.late != (ride?.late ?? false);
      live = next;
      error = null;
      // A new state brings new details: the PIN, the payment marks, …
      if (changed) ride = await api.ride(rideId);
      if (_disposed) return;
      if (state?.isOver ?? false) _stop();
    } on ApiException catch (e) {
      error = e; // Keep showing the last view; try again on the next tick.
    } finally {
      _inFlight = false;
    }
    _notify();
  }

  /// Reloads the ride itself, e.g. after the rider said they're running late.
  Future<void> refreshRide() async {
    ride = await api.ride(rideId);
    _notify();
  }

  void _schedule() {
    _timer?.cancel();
    if (_disposed || (state?.isOver ?? false)) return;
    _timer = Timer.periodic(every, (_) => poll());
  }

  void _stop() {
    _timer?.cancel();
    _timer = null;
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (!_started || _disposed) return;
    if (state == AppLifecycleState.resumed) {
      poll();
      _schedule();
    } else if (state == AppLifecycleState.paused || state == AppLifecycleState.hidden) {
      _stop();
    }
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _stop();
    if (_started) WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }
}
