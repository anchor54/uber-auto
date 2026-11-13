import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../core/models.dart';

import '../../core/dialer.dart';
import '../../ui/live_map.dart';
import '../../ui/ride_map.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'ride_screen.dart';
import 'ride_tracker.dart';

/// R2.2 In the ride: arrival time on top, the map filling the rest of the
/// screen, and the driver and SOS in a drawer over the map.
class InRideView extends StatelessWidget {
  const InRideView({super.key, required this.tracker});

  final RideTracker tracker;

  Future<void> _sos(BuildContext context) async {
    final call = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Call 112?'),
        content: const Text("This calls India's emergency number."),
        actions: [
          TextButton(onPressed: () => Navigator.of(context).pop(false), child: const Text('Cancel')),
          TextButton(
            key: const Key('ride.sos.confirm'),
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Call 112'),
          ),
        ],
      ),
    );
    if (call == true && context.mounted) await callNumber(context, emergencyNumber);
  }

  /// The drawer's height above the bottom inset: handle, driver, SOS.
  static const _drawerHeight = 168.0;

  @override
  Widget build(BuildContext context) {
    final ride = tracker.ride!;
    final live = tracker.live;
    final eta = live?.etaMinutes;
    final etaAt = live?.etaAt;
    final insets = MediaQuery.paddingOf(context);
    final overMap = EdgeInsets.only(bottom: _drawerHeight + insets.bottom);
    // Edge to edge: the header runs up under a transparent status bar and the
    // map down under the navigation bar; the drawer sits over the map.
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: SystemUiOverlayStyle.dark.copyWith(
        statusBarColor: Colors.transparent,
        systemNavigationBarColor: AppColors.surface,
      ),
      child: Scaffold(
        backgroundColor: AppColors.surface,
        body: Column(
          key: const Key('ride.inRide'),
          children: [
            Padding(
              padding: EdgeInsets.only(top: insets.top),
              child: const FlowHeader(label: 'In the ride'),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 14),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    tracker.updating || etaAt == null ? 'On the way to ${ride.drop.title}' : 'Arriving around ${clockTime(etaAt)}',
                    key: const Key('ride.eta'),
                    style: AppText.display(26),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    tracker.updating ? 'Updating…' : (eta == null ? ride.drop.display : '$eta min to ${ride.drop.title}'),
                    key: Key(tracker.updating ? 'ride.updating' : 'ride.etaDetail'),
                    style: AppText.body(14, color: AppColors.muted),
                  ),
                ],
              ),
            ),
            Expanded(
              child: Stack(
                children: [
                  Positioned.fill(
                    child: RideMap(
                      pickup: ride.pickup,
                      drop: ride.drop,
                      phase: MapPhase.ride,
                      route: ride.route,
                      driver: live?.driverAt,
                      height: double.infinity,
                      rounded: false,
                      overlap: overMap,
                      fallback: LiveRideMap(
                        pickup: ride.pickup,
                        drop: ride.drop,
                        driver: live?.driverAt,
                        towardsDrop: true,
                        height: double.infinity,
                        rounded: false,
                        overlap: overMap,
                      ),
                    ),
                  ),
                  Align(alignment: Alignment.bottomCenter, child: _drawer(context, ride.driver)),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  /// The driver and SOS, in a sheet over the bottom of the map.
  Widget _drawer(BuildContext context, Driver driver) {
    return Container(
      key: const Key('ride.drawer'),
      decoration: const BoxDecoration(
        color: AppColors.surface,
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
        boxShadow: [BoxShadow(color: Color(0x29000000), blurRadius: 16, offset: Offset(0, -2))],
      ),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 10, 16, 16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Container(
                width: 40,
                height: 4,
                decoration: BoxDecoration(color: AppColors.line, borderRadius: BorderRadius.circular(2)),
              ),
              const SizedBox(height: 14),
              Row(
                children: [
                  Avatar(driver.initials, size: 44),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(driver.name, style: AppText.body(16, weight: FontWeight.w600)),
                        Text('${driver.car} · ${driver.plate}', style: AppText.body(13, color: AppColors.muted)),
                      ],
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 16),
              SizedBox(
                width: double.infinity,
                height: 52,
                child: FilledButton(
                  key: const Key('ride.sos'),
                  style: FilledButton.styleFrom(
                    backgroundColor: AppColors.red,
                    shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
                  ),
                  onPressed: () => _sos(context),
                  child: Text('SOS · Call 112', style: AppText.body(16, weight: FontWeight.w700, color: Colors.white)),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
