import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'onboarding_common.dart';
import 'pay_screen.dart';

/// D1.6 Your plan: the one plan for the driver's vehicle type (plans.get).
class PlanScreen extends StatefulWidget {
  const PlanScreen({super.key});

  @override
  State<PlanScreen> createState() => _PlanScreenState();
}

class _PlanScreenState extends State<PlanScreen> {
  PlanQuote? _quote;
  Object? _error;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_quote == null && _error == null) _load();
  }

  Future<void> _load() async {
    try {
      final q = await AppScope.of(context).api.plan();
      if (mounted) {
        setState(() {
          _quote = q;
          _error = null;
        });
      }
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final q = _quote;
    final noun = q == null ? '' : vehicleNoun(q.plan.rideType);
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const FlowHeader(),
            Expanded(
              child: q == null
                  ? LoadingBody(
                      error: _error,
                      onRetry: () {
                        setState(() => _error = null);
                        _load();
                      },
                    )
                  : ListView(
                      key: const Key('plan.list'),
                      padding: const EdgeInsets.fromLTRB(20, 4, 20, 16),
                      children: [
                        Semantics(header: true, child: Text('Driver subscription', style: AppText.display(28))),
                        const SizedBox(height: 6),
                        Text('One flat monthly fee. Keep every fare.', style: AppText.body(15, color: AppColors.muted)),
                        const SizedBox(height: 18),
                        CardBox(
                          key: const Key('plan.card'),
                          radius: 18,
                          border: Border.all(color: AppColors.yellow, width: 1.5),
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(q.label, style: AppText.display(20)),
                              const SizedBox(height: 6),
                              Text.rich(
                                TextSpan(children: [
                                  TextSpan(text: rupees(q.monthlyPrice), style: AppText.display(32, letterSpacing: 0)),
                                  TextSpan(text: ' /mo', style: AppText.body(15, color: AppColors.muted)),
                                ]),
                              ),
                              const SizedBox(height: 14),
                              for (final b in ['No commission on any ride', 'All regular and one-time requests for $noun drivers'])
                                Padding(
                                  padding: const EdgeInsets.only(bottom: 8),
                                  child: Row(
                                    crossAxisAlignment: CrossAxisAlignment.start,
                                    children: [
                                      const Icon(Icons.check, size: 18, color: AppColors.green),
                                      const SizedBox(width: 8),
                                      Expanded(child: Text(b, style: AppText.body(14, height: 1.4))),
                                    ],
                                  ),
                                ),
                            ],
                          ),
                        ),
                        const SizedBox(height: 16),
                        const NoteBox(
                          icon: Icons.calendar_month_outlined,
                          text: "Pay by the 1st of each month by UPI or bank transfer. If it isn't received by the 4th, "
                              'your account pauses until you pay.',
                        ),
                      ],
                    ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 20, 16),
              child: PrimaryButton(
                label: 'Continue to payment',
                height: 52,
                onPressed: q == null ? null : () => Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => const PayScreen())),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
