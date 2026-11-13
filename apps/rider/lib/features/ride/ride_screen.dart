import 'dart:async';

import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../core/schedule.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'arrived_screen.dart';
import 'in_ride_screen.dart';
import 'on_the_way_screen.dart';
import 'ride_tracker.dart';

/// Flow R2's live screens for one ride. The tracker's state picks which one
/// shows, so the rider can't get stuck on the wrong screen:
///   scheduled, on the way, arrived → R2.1 · in the ride → R2.2 ·
///   completed → R2.3 · no-show → the no-show note.
class RideScreen extends StatefulWidget {
  const RideScreen({super.key, required this.rideId, this.tracker});

  final String rideId;

  /// For tests; normally the screen makes its own.
  final RideTracker? tracker;

  @override
  State<RideScreen> createState() => _RideScreenState();
}

class _RideScreenState extends State<RideScreen> {
  RideTracker? _tracker;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_tracker == null) {
      final scope = AppScope.of(context);
      _tracker = widget.tracker ?? RideTracker(api: scope.api, rideId: widget.rideId, clock: scope.clock);
      _tracker!.start();
    }
  }

  @override
  void dispose() {
    if (widget.tracker == null) _tracker?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = _tracker!;
    return ListenableBuilder(
      listenable: t,
      builder: (context, _) {
        final ride = t.ride;
        if (ride == null) {
          return Scaffold(
            body: SafeArea(
              child: Column(
                children: [
                  const FlowHeader(),
                  Expanded(
                    child: Center(
                      child: t.error == null
                          ? const CircularProgressIndicator(color: AppColors.green)
                          : Padding(
                              padding: const EdgeInsets.all(24),
                              child: Text(
                                (t.error as ApiException?)?.message ?? 'Something went wrong.',
                                textAlign: TextAlign.center,
                                style: AppText.body(15, color: AppColors.muted),
                              ),
                            ),
                    ),
                  ),
                ],
              ),
            ),
          );
        }
        return switch (t.state ?? ride.state) {
          RideState.inRide => InRideView(tracker: t),
          RideState.completed => ArrivedView(ride: ride),
          RideState.noShow => _NoShowView(ride: ride),
          RideState.skipped || RideState.cancelled => _ClosedView(ride: ride),
          _ => OnTheWayView(tracker: t),
        };
      },
    );
  }
}

class _NoShowView extends StatelessWidget {
  const _NoShowView({required this.ride});
  final Ride ride;

  @override
  Widget build(BuildContext context) {
    return _MessageScaffold(
      key: const Key('ride.noShow'),
      title: 'Marked as a no-show',
      body: '${ride.driver.firstName} waited at ${ride.pickup.title} and called, but couldn\'t find you. '
          'There\'s nothing to pay for this ride. Your other rides are still booked.',
    );
  }
}

class _ClosedView extends StatelessWidget {
  const _ClosedView({required this.ride});
  final Ride ride;

  @override
  Widget build(BuildContext context) {
    return _MessageScaffold(
      key: const Key('ride.closed'),
      title: ride.state == RideState.skipped ? 'This ride is skipped' : 'This ride was cancelled',
      body: "There's nothing to pay for it. Your other rides are still booked.",
    );
  }
}

class _MessageScaffold extends StatelessWidget {
  const _MessageScaffold({super.key, required this.title, required this.body});
  final String title;
  final String body;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            const FlowHeader(),
            Expanded(
              child: ListView(
                padding: const EdgeInsets.fromLTRB(20, 0, 20, 16),
                children: [
                  Text(title, style: AppText.display(26)),
                  const SizedBox(height: 10),
                  Text(body, style: AppText.body(15, color: AppColors.muted, height: 1.45)),
                ],
              ),
            ),
            BottomActions(
              children: [
                PrimaryButton(key: const Key('ride.done'), label: 'Done', onPressed: () => Navigator.of(context).pop()),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

/// Seconds left until [endsAt] on the server's clock, ticking every second.
class Countdown extends StatefulWidget {
  const Countdown({super.key, required this.endsAt, required this.now, required this.builder});

  final DateTime endsAt;
  final DateTime Function() now;
  final Widget Function(BuildContext context, Duration left) builder;

  @override
  State<Countdown> createState() => _CountdownState();
}

class _CountdownState extends State<Countdown> {
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    _timer = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted) setState(() {});
    });
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final left = widget.endsAt.difference(widget.now());
    return widget.builder(context, left.isNegative ? Duration.zero : left);
  }
}

/// "2:05"
String formatCountdown(Duration d) => '${d.inMinutes}:${(d.inSeconds % 60).toString().padLeft(2, '0')}';

/// "8:43 AM"
String clockTime(DateTime t) => formatTime(t.hour * 60 + t.minute);
