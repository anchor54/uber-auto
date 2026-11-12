import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/fares.dart';
import '../../core/models.dart';
import '../../core/schedule.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import '../home/home_screen.dart';
import 'setup_controller.dart';

/// R1.5 Confirm the commute. Nothing is paid here: the rider pays the
/// driver directly after each ride, and says how they usually pay.
class ConfirmScreen extends StatefulWidget {
  const ConfirmScreen({super.key, required this.setup});

  final CommuteSetup setup;

  @override
  State<ConfirmScreen> createState() => _ConfirmScreenState();
}

class _ConfirmScreenState extends State<ConfirmScreen> {
  CommuteSetup get setup => widget.setup;
  bool _saving = false;
  String? _error;

  Future<void> _confirm() async {
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      final commute = await AppScope.of(context).api.confirmCommute(
            request: setup.toRequest(),
            match: setup.match!,
            usualPayment: setup.usualPayment,
          );
      if (!mounted) return;
      final messenger = ScaffoldMessenger.of(context);
      Navigator.of(context).pushAndRemoveUntil(
        MaterialPageRoute<void>(builder: (_) => const HomeScreen()),
        (_) => false,
      );
      messenger.showSnackBar(SnackBar(
        content: Text(
          'Commute confirmed. ${commute.driver.firstName} picks you up '
          '${formatDateCompact(commute.request.startDate)} at ${formatTime(commute.request.pickupTime)}.',
        ),
      ));
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _saving = false;
        _error = e.message;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final r = setup.toRequest();
    final m = setup.match!;
    final first = m.driver.firstName;
    final schedule = r.returnTrip
        ? '${daysLabel(r.weekdays)} · ${formatTime(r.pickupTime)}, ${formatTime(r.returnTime)}'
        : '${daysLabel(r.weekdays)} · ${formatTime(r.pickupTime)}';
    final route = r.returnTrip ? '${r.pickup.title} ⇄ ${r.drop.title}' : '${r.pickup.title} → ${r.drop.title}';
    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            const FlowHeader(label: 'Review'),
            Expanded(
              child: ListView(
                padding: const EdgeInsets.only(bottom: 16),
                children: [
                  const ScreenTitle('Confirm your commute'),
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        CardBox(
                          child: Column(
                            children: [
                              SummaryRow('Commute', route, valueKey: const Key('confirm.route')),
                              const SizedBox(height: 10),
                              SummaryRow('Schedule', schedule, valueKey: const Key('confirm.schedule')),
                              const SizedBox(height: 10),
                              SummaryRow('Ride · driver', '${r.rideType.label} · ${m.driver.name}', valueKey: const Key('confirm.driver')),
                              const SizedBox(height: 10),
                              SummaryRow(
                                'First ride',
                                '${formatDateCompact(r.startDate)}, ${formatTime(r.pickupTime)}',
                                valueKey: const Key('confirm.firstRide'),
                              ),
                              const SizedBox(height: 10),
                              const Hairline(),
                              const SizedBox(height: 10),
                              PriceRow(
                                label: 'Fare per ride',
                                labelStyle: AppText.body(14, weight: FontWeight.w600),
                                price: formatRupees(m.fare),
                                priceKey: const Key('confirm.fare'),
                              ),
                            ],
                          ),
                        ),
                        const SizedBox(height: 14),
                        CardBox(
                          color: AppColors.greenTint,
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text('How payment works', style: AppText.body(15, weight: FontWeight.w600, color: AppColors.greenDark)),
                              const SizedBox(height: 10),
                              CheckLine('Nothing to pay now. You pay $first directly after each ride.', color: AppColors.greenDark, textColor: AppColors.greenDark),
                              const SizedBox(height: 10),
                              const CheckLine('Skipped or paused rides cost nothing.', color: AppColors.greenDark, textColor: AppColors.greenDark),
                              const SizedBox(height: 10),
                              const CheckLine("Cancel any time; there's no contract.", color: AppColors.greenDark, textColor: AppColors.greenDark),
                            ],
                          ),
                        ),
                        const SizedBox(height: 14),
                        Text("How you'll usually pay $first", style: AppText.body(15, weight: FontWeight.w600)),
                        const SizedBox(height: 8),
                        Row(
                          children: [
                            for (final method in PaymentMethod.values) ...[
                              if (method != PaymentMethod.values.first) const SizedBox(width: 8),
                              Expanded(child: _methodButton(method)),
                            ],
                          ],
                        ),
                        const SizedBox(height: 8),
                        Text(
                          '$first sees this before your first ride. You can change it any time.',
                          style: AppText.body(12, color: AppColors.muted),
                        ),
                        if (_error != null) ...[
                          const SizedBox(height: 12),
                          Text(_error!, style: AppText.body(14, color: AppColors.red)),
                        ],
                      ],
                    ),
                  ),
                ],
              ),
            ),
            BottomActions(
              children: [
                PrimaryButton(
                  key: const Key('confirm.submit'),
                  label: 'Confirm commute',
                  loading: _saving,
                  onPressed: _confirm,
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Widget _methodButton(PaymentMethod method) {
    final on = setup.usualPayment == method;
    return Semantics(
      selected: on,
      button: true,
      label: method.label,
      excludeSemantics: true,
      child: GestureDetector(
        key: Key('confirm.pay.${method.name}'),
        onTap: () => setState(() => setup.setUsualPayment(method)),
        child: Container(
          height: 48,
          decoration: BoxDecoration(
            color: AppColors.surface,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: on ? AppColors.green : AppColors.line, width: on ? 2 : 1),
          ),
          alignment: Alignment.center,
          child: Text(method.label, style: AppText.body(14, weight: on ? FontWeight.w600 : FontWeight.w500)),
        ),
      ),
    );
  }
}
