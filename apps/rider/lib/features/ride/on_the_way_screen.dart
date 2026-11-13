import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/dialer.dart';
import '../../core/models.dart';
import '../../ui/live_map.dart';
import '../../ui/ride_map.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'ride_screen.dart';
import 'ride_tracker.dart';

/// R2.1 Driver on the way (and the "arrived" banner with the wait countdown).
class OnTheWayView extends StatefulWidget {
  const OnTheWayView({super.key, required this.tracker});

  final RideTracker tracker;

  @override
  State<OnTheWayView> createState() => _OnTheWayViewState();
}

class _OnTheWayViewState extends State<OnTheWayView> {
  bool _sendingLate = false;
  List<Ride>? _tomorrow;

  RideTracker get t => widget.tracker;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_tomorrow == null) _loadTomorrow();
  }

  Future<void> _loadTomorrow() async {
    final scope = AppScope.of(context);
    try {
      final rides = await scope.api.upcomingRides(days: 2);
      final today = scope.clock.now();
      final next = DateTime(today.year, today.month, today.day + 1);
      if (mounted) {
        setState(() => _tomorrow = [for (final r in rides) if (r.date == next) r]);
      }
    } on ApiException {
      if (mounted) setState(() => _tomorrow = const []);
    }
  }

  Future<void> _runningLate() async {
    setState(() => _sendingLate = true);
    final messenger = ScaffoldMessenger.of(context);
    try {
      await AppScope.of(context).api.runningLate(t.rideId);
      await t.refreshRide();
      messenger.hideCurrentSnackBar();
      messenger.showSnackBar(
        SnackBar(content: Text('We told ${t.ride!.driver.firstName}. They\'ll wait 10 minutes instead of 5.')),
      );
    } on ApiException catch (e) {
      messenger.showSnackBar(SnackBar(content: Text(e.message)));
    } finally {
      if (mounted) setState(() => _sendingLate = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final ride = t.ride!;
    final live = t.live;
    final state = t.state ?? ride.state;
    final driver = ride.driver;
    final first = driver.firstName;
    final arrived = state == RideState.arrived;
    final late = live?.late ?? ride.late;
    final pinLocked = live?.pinLocked ?? ride.pinLocked;
    final eta = live?.etaMinutes;
    final etaAt = live?.etaAt;
    final waitEndsAt = live?.waitEndsAt ?? ride.waitEndsAt;

    final String headline;
    if (state == RideState.scheduled) {
      headline = '$first picks you up at ${clockTime(ride.pickupAt)}';
    } else if (arrived) {
      headline = '$first has arrived';
    } else if (t.updating) {
      headline = '$first is on the way';
    } else if (eta != null && etaAt != null) {
      headline = eta <= 1 ? '$first is almost there' : '$first arrives in $eta min';
    } else {
      headline = '$first is on the way';
    }

    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            const FlowHeader(label: 'Your ride'),
            Expanded(
              child: ListView(
                key: const Key('ride.onTheWay'),
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
                children: [
                  Text(headline, key: const Key('ride.eta'), style: AppText.display(26)),
                  const SizedBox(height: 4),
                  Text(
                    t.updating
                        ? 'Updating…'
                        : arrived
                            ? 'At ${ride.pickup.title}'
                            : etaAt != null && state == RideState.onTheWay
                                ? 'Around ${clockTime(etaAt)} · pickup window ${clockTime(ride.windowStart)}–${clockTime(ride.windowEnd)}'
                                : 'Pickup window ${clockTime(ride.windowStart)}–${clockTime(ride.windowEnd)}',
                    key: Key(t.updating ? 'ride.updating' : 'ride.etaDetail'),
                    style: AppText.body(14, color: AppColors.muted),
                  ),
                  const SizedBox(height: 12),
                  if (late && !arrived) ...[
                    _Banner(
                      key: const Key('ride.late'),
                      color: AppColors.orangeTint,
                      textColor: AppColors.orangeText,
                      text: '$first is running late. Call to check where they are.',
                    ),
                    const SizedBox(height: 10),
                  ],
                  if (arrived && waitEndsAt != null) ...[
                    Countdown(
                      key: const Key('ride.countdown'),
                      endsAt: waitEndsAt,
                      now: t.serverNow,
                      builder: (context, left) => _Banner(
                        key: const Key('ride.arrivedBanner'),
                        color: AppColors.greenTint,
                        textColor: AppColors.greenDark,
                        text: left == Duration.zero
                            ? '$first is waiting at ${ride.pickup.title}. Please head out now.'
                            : '$first is waiting at ${ride.pickup.title} until ${clockTime(waitEndsAt)} '
                                '(${formatCountdown(left)} left).',
                      ),
                    ),
                    const SizedBox(height: 10),
                  ],
                  if (pinLocked) ...[
                    const _Banner(
                      key: Key('ride.support'),
                      color: AppColors.orangeTint,
                      textColor: AppColors.orangeText,
                      text: "The PIN was entered wrong three times, so the ride can't start. Call support.",
                    ),
                    const SizedBox(height: 10),
                  ],
                  RideMap(
                    pickup: ride.pickup,
                    drop: ride.drop,
                    phase: MapPhase.approach,
                    approachRoute: live?.approachRoute,
                    driver: live?.driverAt,
                    label: t.updating ? 'Last known position' : null,
                    fallback: LiveRideMap(
                      pickup: ride.pickup,
                      drop: ride.drop,
                      driver: live?.driverAt,
                      label: t.updating ? 'Last known position' : null,
                    ),
                  ),
                  const SizedBox(height: 12),
                  CardBox(
                    child: Column(
                      children: [
                        Row(
                          children: [
                            Avatar(driver.initials, size: 44),
                            const SizedBox(width: 12),
                            Expanded(
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text(driver.name, style: AppText.body(16, weight: FontWeight.w600)),
                                  const SizedBox(height: 2),
                                  Text('${driver.car} · ${driver.plate}', key: const Key('ride.car'), style: AppText.body(13, color: AppColors.muted)),
                                ],
                              ),
                            ),
                            if (ride.pin != null)
                              Column(
                                children: [
                                  Text('PIN', style: AppText.body(11, color: AppColors.muted, weight: FontWeight.w600)),
                                  Text(ride.pin!, key: const Key('ride.pin'), style: AppText.display(26, letterSpacing: 2)),
                                ],
                              ),
                          ],
                        ),
                        if (ride.pin != null) ...[
                          const SizedBox(height: 8),
                          Text(
                            'Tell $first this PIN when you get in. Never share it before.',
                            style: AppText.body(12, color: AppColors.muted),
                          ),
                        ],
                        const SizedBox(height: 12),
                        Row(
                          children: [
                            Expanded(
                              child: OutlineButtonBox(
                                key: const Key('ride.call'),
                                label: 'Call $first',
                                onPressed: driver.phone == null ? null : () => callNumber(context, driver.phone!),
                              ),
                            ),
                            if (!ride.riderLateUsed && state != RideState.inRide) ...[
                              const SizedBox(width: 10),
                              Expanded(
                                child: OutlineButtonBox(
                                  key: const Key('ride.runningLate'),
                                  label: _sendingLate ? 'Telling $first…' : "I'm 5 min late",
                                  onPressed: _sendingLate ? null : _runningLate,
                                ),
                              ),
                            ],
                          ],
                        ),
                      ],
                    ),
                  ),
                  if (_tomorrow?.isNotEmpty ?? false) ...[
                    const SizedBox(height: 14),
                    Text('Tomorrow', style: AppText.body(15, weight: FontWeight.w600)),
                    const SizedBox(height: 6),
                    for (final r in _tomorrow!)
                      Padding(
                        padding: const EdgeInsets.symmetric(vertical: 4),
                        child: Text(
                          '${clockTime(r.pickupAt)} · ${r.pickup.title} → ${r.drop.title}',
                          key: Key('ride.tomorrow.${r.direction.name}'),
                          style: AppText.body(14, color: AppColors.muted),
                        ),
                      ),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Banner extends StatelessWidget {
  const _Banner({super.key, required this.color, required this.textColor, required this.text});
  final Color color;
  final Color textColor;
  final String text;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(color: color, borderRadius: BorderRadius.circular(12)),
      child: Text(text, style: AppText.body(14, color: textColor, weight: FontWeight.w600, height: 1.4)),
    );
  }
}
