import 'package:flutter/material.dart';

import '../../core/dialer.dart';
import '../../ui/live_map.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'ride_screen.dart';
import 'ride_tracker.dart';

/// R2.2 In the ride: progress to the drop-off, arrival time, driver, SOS.
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

  @override
  Widget build(BuildContext context) {
    final ride = tracker.ride!;
    final live = tracker.live;
    final eta = live?.etaMinutes;
    final etaAt = live?.etaAt;
    final progress = eta == null || ride.tripMinutes == 0 ? 0.0 : (1 - eta / ride.tripMinutes).clamp(0.0, 1.0);
    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            const FlowHeader(label: 'In the ride'),
            Expanded(
              child: ListView(
                key: const Key('ride.inRide'),
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
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
                  const SizedBox(height: 12),
                  ClipRRect(
                    borderRadius: BorderRadius.circular(999),
                    child: LinearProgressIndicator(
                      key: const Key('ride.progress'),
                      value: progress,
                      minHeight: 8,
                      color: AppColors.green,
                      backgroundColor: AppColors.divider,
                    ),
                  ),
                  const SizedBox(height: 12),
                  LiveRideMap(pickup: ride.pickup, drop: ride.drop, driver: live?.driverAt, towardsDrop: true),
                  const SizedBox(height: 12),
                  CardBox(
                    child: Row(
                      children: [
                        Avatar(ride.driver.initials, size: 44),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(ride.driver.name, style: AppText.body(16, weight: FontWeight.w600)),
                              Text('${ride.driver.car} · ${ride.driver.plate}', style: AppText.body(13, color: AppColors.muted)),
                            ],
                          ),
                        ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
            BottomActions(
              children: [
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
          ],
        ),
      ),
    );
  }
}
