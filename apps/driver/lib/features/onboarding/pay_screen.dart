import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../core/schedule.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import '../shell/driver_shell.dart';
import 'onboarding_common.dart';

/// D1.7 Pay and go live: what to pay now (prorated to the month's end),
/// where (UPI ID and reference, each with Copy), then "I've paid". After
/// that the button waits ("Checking your payment") until the admin confirms
/// and the app opens Requests with "You're live"; a rejected claim shows
/// why and the button is active again.
class PayScreen extends StatefulWidget {
  const PayScreen({super.key});

  @override
  State<PayScreen> createState() => _PayScreenState();
}

class _PayScreenState extends State<PayScreen> with DriverRefresh<PayScreen> {
  PlanQuote? _quote;
  SubscriptionPayment? _payment;
  Object? _loadError;
  bool _claiming = false;
  bool _leaving = false;
  String? _error;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_quote == null && _loadError == null) _load();
    startRefreshing();
  }

  @override
  void refresh() => _load();

  @override
  bool get keepRefreshing => _payment?.status == PaymentStatus.claimed;

  Future<void> _load() async {
    final scope = AppScope.of(context);
    try {
      final q = await scope.api.plan();
      if (!mounted) return;
      if (q.status == DriverStatus.live) {
        await _goLive();
        return;
      }
      setState(() {
        _quote = q;
        _payment = q.latestPayment;
        _loadError = null;
      });
    } on ApiException catch (e) {
      if (mounted && _quote == null) setState(() => _loadError = e);
    }
  }

  /// Confirmed: Requests, with the "You're live" banner.
  Future<void> _goLive() async {
    if (_leaving) return;
    _leaving = true;
    final scope = AppScope.of(context);
    DriverProfile? profile;
    try {
      profile = (await scope.api.me()).profile;
    } on ApiException {
      profile = null;
    }
    if (!mounted) return;
    openAsRoot(context, DriverShell(profile: profile, showLiveBanner: true));
  }

  Future<void> _paid() async {
    setState(() {
      _claiming = true;
      _error = null;
    });
    try {
      final p = await AppScope.of(context).api.claimPayment(periodStart: _quote?.periodStart);
      if (!mounted) return;
      setState(() {
        _payment = p;
        _claiming = false;
      });
    } on ApiException catch (e) {
      if (!mounted) return;
      final state = e.state;
      setState(() {
        _claiming = false;
        if (e.code == 'already_claimed' && state is SubscriptionPayment) {
          _payment = state;
        } else if (e.code != 'already_claimed') {
          _error = e.message;
        }
      });
      if (e.code == 'already_claimed' && state is! SubscriptionPayment) _load();
    }
  }

  Future<void> _copy(String text, String what) async {
    try {
      await Clipboard.setData(ClipboardData(text: text));
    } catch (_) {
      // No clipboard (tests, some browsers): the text is on screen anyway.
    }
    if (mounted) showInfo(context, '$what copied');
  }

  String _range(DateTime start, DateTime end) => sameDay(start, end) ? formatDayMonth(start) : formatWeekRange(start, end);

  @override
  Widget build(BuildContext context) {
    final q = _quote;
    final p = _payment;
    final waiting = p != null && p.status == PaymentStatus.claimed;
    final rejected = p != null && p.status == PaymentStatus.rejected;
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const FlowHeader(),
            Expanded(
              child: q == null
                  ? LoadingBody(
                      error: _loadError,
                      onRetry: () {
                        setState(() => _loadError = null);
                        _load();
                      },
                    )
                  : RefreshIndicator(
                      color: AppColors.yellow,
                      onRefresh: _load,
                      child: ListView(
                        key: const Key('pay.list'),
                        physics: const AlwaysScrollableScrollPhysics(),
                        padding: const EdgeInsets.fromLTRB(20, 4, 20, 16),
                        children: [
                          const Align(
                            alignment: Alignment.centerLeft,
                            child: Chip2('Approved', background: AppColors.greenDark, foreground: AppColors.green),
                          ),
                          const SizedBox(height: 10),
                          Semantics(header: true, child: Text('Last step: pay your first month', style: AppText.display(28))),
                          const SizedBox(height: 16),
                          CardBox(
                            key: const Key('pay.plan'),
                            child: Column(
                              children: [
                                _line(q.label, '${rupees(q.monthlyPrice)} /mo', muted: false),
                                const SizedBox(height: 6),
                                const Hairline(),
                                const SizedBox(height: 6),
                                _line(
                                  'Pay now (${_range(q.periodStart, q.periodEnd)})',
                                  rupees(q.amountDue),
                                  muted: false,
                                  big: true,
                                  key: const Key('pay.amountDue'),
                                ),
                                _line('Then by the 1st of each month', rupees(q.monthlyPrice)),
                              ],
                            ),
                          ),
                          const SizedBox(height: 18),
                          Text('Pay by UPI or bank transfer', style: AppText.body(15, weight: FontWeight.w600)),
                          const SizedBox(height: 8),
                          CardBox(
                            child: Column(
                              children: [
                                _copyRow('UPI ID', q.payeeUpi, () => _copy(q.payeeUpi, 'UPI ID'), const Key('pay.upi')),
                                const SizedBox(height: 4),
                                const Hairline(),
                                const SizedBox(height: 4),
                                _copyRow('Add this reference', q.reference, () => _copy(q.reference, 'Reference'),
                                    const Key('pay.reference')),
                              ],
                            ),
                          ),
                          const SizedBox(height: 10),
                          Text(
                            'Bank details are in Account › Plan. We confirm payments within $reviewHours working hours.',
                            style: AppText.body(13, color: AppColors.muted, height: 1.45),
                          ),
                          if (rejected) ...[
                            const SizedBox(height: 14),
                            NoteBox(
                              key: const Key('pay.rejected'),
                              icon: Icons.error_outline,
                              color: AppColors.orange,
                              text: "We couldn't find your payment: ${p.rejectReason ?? 'not received'}",
                            ),
                          ],
                          if (_error != null) ...[
                            const SizedBox(height: 12),
                            Text(_error!, key: const Key('pay.error'), style: AppText.body(14, color: AppColors.orange, height: 1.4)),
                          ],
                        ],
                      ),
                    ),
            ),
            if (q != null)
              Padding(
                padding: const EdgeInsets.fromLTRB(20, 8, 20, 16),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (waiting)
                      const PrimaryButton(label: 'Checking your payment', height: 52, onPressed: null)
                    else
                      PrimaryButton(label: "I've paid", height: 52, loading: _claiming, onPressed: _paid),
                    const SizedBox(height: 8),
                    Text(
                      waiting
                          ? "Paid on ${formatDate(p.claimedAt ?? AppScope.of(context).clock.now())}. We'll notify you when it's confirmed."
                          : 'You go live as soon as we confirm it.',
                      key: const Key('pay.status'),
                      textAlign: TextAlign.center,
                      style: AppText.body(13, color: AppColors.muted, height: 1.4),
                    ),
                  ],
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget _line(String label, String value, {bool muted = true, bool big = false, Key? key}) => Padding(
        key: key,
        padding: const EdgeInsets.symmetric(vertical: 4),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.baseline,
          textBaseline: TextBaseline.alphabetic,
          children: [
            Expanded(child: Text(label, style: AppText.body(14, color: muted ? AppColors.muted : AppColors.ink))),
            const SizedBox(width: 12),
            Text(value, style: big ? AppText.display(22, letterSpacing: 0) : AppText.body(14, weight: FontWeight.w600)),
          ],
        ),
      );

  Widget _copyRow(String label, String value, VoidCallback onCopy, Key key) => Row(
        key: key,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(label, style: AppText.body(13, color: AppColors.muted)),
                const SizedBox(height: 2),
                Text(value, style: AppText.body(16, weight: FontWeight.w600)),
              ],
            ),
          ),
          LinkButton(label: 'Copy', onPressed: onCopy),
        ],
      );
}
