import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app_scope.dart';
import '../../core/fares.dart';
import '../../core/models.dart';
import '../../ui/icons.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'ride_screen.dart';

/// R2.3 Arrived, pay and rate. Both are optional: Done goes Home either way,
/// and the rider can come back to it later.
class ArrivedView extends StatefulWidget {
  const ArrivedView({super.key, required this.ride});

  final Ride ride;

  @override
  State<ArrivedView> createState() => _ArrivedViewState();
}

class _ArrivedViewState extends State<ArrivedView> {
  static const tags = ['On time', 'Safe driving', 'Clean car', 'Polite', 'Knew the route'];

  late PaymentMethod? _paid = widget.ride.riderPayment;
  late PaymentStatus _status = widget.ride.paymentStatus;
  late int _stars = widget.ride.rating?.stars ?? 0;
  late final Set<String> _tags = {...?widget.ride.rating?.tags};
  bool _saving = false;
  String? _error;
  Ride? _next;

  Ride get ride => widget.ride;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_next == null) _loadNext();
  }

  Future<void> _loadNext() async {
    try {
      final rides = await AppScope.of(context).api.upcomingRides(days: 7);
      final next = rides.where((r) => r.id != ride.id && r.state == RideState.scheduled).firstOrNull;
      if (mounted && next != null) setState(() => _next = next);
    } on ApiException {
      // The next leg is a nicety; leave it out if it can't load.
    }
  }

  Future<void> _markPaid(PaymentMethod m) async {
    setState(() {
      _paid = m;
      _error = null;
    });
    try {
      final status = await AppScope.of(context).api.markPayment(ride.id, m);
      if (mounted) setState(() => _status = status);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    }
  }

  Future<void> _done() async {
    setState(() => _saving = true);
    if (_stars > 0) {
      try {
        await AppScope.of(context).api.rateRide(ride.id, RideRating(stars: _stars, tags: _tags.toList()));
      } on ApiException catch (e) {
        if (mounted) {
          setState(() {
            _saving = false;
            _error = e.message;
          });
        }
        return;
      }
    }
    if (mounted) Navigator.of(context).pop();
  }

  String get _driverMark => switch (_status) {
        PaymentStatus.paid => '${ride.driver.firstName} marked this received.',
        PaymentStatus.disputed => "${ride.driver.firstName} says it isn't paid yet. We'll help you sort it out.",
        PaymentStatus.unpaid => "${ride.driver.firstName} says it isn't paid yet.",
        _ => "${ride.driver.firstName} hasn't marked it yet.",
      };

  @override
  Widget build(BuildContext context) {
    final first = ride.driver.firstName;
    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            const FlowHeader(label: 'Ride complete'),
            Expanded(
              child: ListView(
                key: const Key('ride.arrived'),
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
                children: [
                  Text("You've arrived", style: AppText.display(26)),
                  const SizedBox(height: 4),
                  Text('${ride.drop.display}${ride.endedAt == null ? '' : ' · ${clockTime(ride.endedAt!)}'}',
                      style: AppText.body(14, color: AppColors.muted)),
                  const SizedBox(height: 14),
                  CardBox(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        PriceRow(
                          label: 'Pay $first',
                          labelStyle: AppText.body(15, weight: FontWeight.w600),
                          price: formatRupees(ride.fare),
                          priceKey: const Key('pay.fare'),
                        ),
                        const SizedBox(height: 10),
                        Row(
                          children: [
                            Expanded(
                              child: Text('UPI: ${ride.driver.upiId}', key: const Key('pay.upiId'), style: AppText.body(14)),
                            ),
                            LinkButton(
                              key: const Key('pay.copyUpi'),
                              label: 'Copy',
                              height: 32,
                              onPressed: () {
                                Clipboard.setData(ClipboardData(text: ride.driver.upiId));
                                ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('UPI ID copied')));
                              },
                            ),
                          ],
                        ),
                        const SizedBox(height: 10),
                        Row(
                          children: [
                            Expanded(child: _payButton('pay.upi', 'Paid by UPI', PaymentMethod.upi)),
                            const SizedBox(width: 8),
                            Expanded(child: _payButton('pay.cash', 'Paid in cash', PaymentMethod.cash)),
                          ],
                        ),
                        if (_paid != null) ...[
                          const SizedBox(height: 8),
                          Text(_driverMark, key: const Key('pay.status'), style: AppText.body(13, color: AppColors.muted)),
                        ],
                        if (_error != null) ...[
                          const SizedBox(height: 8),
                          Text(_error!, style: AppText.body(13, color: AppColors.red)),
                        ],
                      ],
                    ),
                  ),
                  const SizedBox(height: 14),
                  Text('How was your ride with $first?', style: AppText.body(15, weight: FontWeight.w600)),
                  const SizedBox(height: 6),
                  Row(
                    children: [
                      for (var i = 1; i <= 5; i++)
                        IconButton(
                          key: Key('rate.star$i'),
                          tooltip: '$i star${i == 1 ? '' : 's'}',
                          onPressed: () => setState(() => _stars = i),
                          icon: AppIcon(i <= _stars ? AppIcons.starFilled : AppIcons.star,
                              size: 34, color: i <= _stars ? AppColors.yellow : AppColors.outline),
                        ),
                    ],
                  ),
                  if (_stars > 0)
                    Wrap(
                      spacing: 8,
                      runSpacing: 8,
                      children: [
                        for (final tag in tags)
                          FilterChip(
                            key: Key('rate.tag.$tag'),
                            label: Text(tag),
                            selected: _tags.contains(tag),
                            onSelected: (on) => setState(() => on ? _tags.add(tag) : _tags.remove(tag)),
                          ),
                      ],
                    ),
                  if (_next != null) ...[
                    const SizedBox(height: 16),
                    Text(
                      'Next: ${formatDateLabel(_next!)} at ${clockTime(_next!.pickupAt)}, ${_next!.pickup.title} → ${_next!.drop.title}',
                      key: const Key('ride.nextLeg'),
                      style: AppText.body(14, color: AppColors.muted),
                    ),
                  ],
                ],
              ),
            ),
            BottomActions(
              children: [
                PrimaryButton(key: const Key('rate.done'), label: 'Done', loading: _saving, onPressed: _done),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Widget _payButton(String key, String label, PaymentMethod m) {
    final on = _paid == m;
    return Semantics(
      selected: on,
      button: true,
      label: label,
      excludeSemantics: true,
      child: GestureDetector(
        key: Key(key),
        onTap: () => _markPaid(m),
        child: Container(
          height: 48,
          decoration: BoxDecoration(
            color: on ? AppColors.greenTint : AppColors.surface,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: on ? AppColors.green : AppColors.line, width: on ? 2 : 1),
          ),
          alignment: Alignment.center,
          child: Text(label, style: AppText.body(14, weight: on ? FontWeight.w600 : FontWeight.w500)),
        ),
      ),
    );
  }

  String formatDateLabel(Ride r) {
    final today = AppScope.of(context).clock.now();
    final d = r.date;
    if (d.year == today.year && d.month == today.month && d.day == today.day) return 'today';
    final tomorrow = DateTime(today.year, today.month, today.day + 1);
    if (d == tomorrow) return 'tomorrow';
    return '${['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'][d.weekday - 1]} ${d.day}';
  }
}
