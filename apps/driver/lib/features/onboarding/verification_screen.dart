import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'documents_screen.dart';
import 'onboarding_common.dart';
import 'plan_screen.dart';
import 'vehicle_screen.dart';

/// D1.5 Verification: three rows (Documents, Vehicle, Approval) and, by
/// status, the rejected items with "Fix now" and "Submit again", "Choose
/// your plan" once approved, or the reason and "Contact support" when
/// turned down. Reads the driver every time it opens, on a push, on resume
/// and (with [AppScope.refreshEvery]) while waiting.
class VerificationScreen extends StatefulWidget {
  const VerificationScreen({super.key});

  @override
  State<VerificationScreen> createState() => _VerificationScreenState();
}

class _VerificationScreenState extends State<VerificationScreen> with DriverRefresh<VerificationScreen> {
  Driver? _driver;
  Object? _loadError;
  bool _submitting = false;
  String? _error;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_driver == null && _loadError == null) _load();
    startRefreshing();
  }

  @override
  void refresh() => _load();

  @override
  bool get keepRefreshing {
    final s = _driver?.status;
    return s == null || s == DriverStatus.submitted;
  }

  Future<void> _load() async {
    final scope = AppScope.of(context);
    try {
      final d = await scope.api.me();
      if (!mounted) return;
      final elsewhere = d.status == DriverStatus.live || d.status == DriverStatus.paused || d.status == DriverStatus.signingUp;
      // Not this screen's business any more (or yet). When another screen is
      // on top (Pay), that one moves on.
      if (elsewhere && (ModalRoute.of(context)?.isCurrent ?? true)) {
        openAsRoot(context, screenForDriver(d, scope.clock.now()));
        return;
      }
      setState(() {
        _driver = d;
        _loadError = null;
      });
    } on ApiException catch (e) {
      if (mounted && _driver == null) setState(() => _loadError = e);
    }
  }

  Future<void> _fix(DriverItem item) async {
    final Widget screen = ItemKind.documents.contains(item.kind) ? const DocumentsScreen() : const VehicleScreen();
    await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => screen));
    if (mounted) _load();
  }

  Future<void> _submitAgain() async {
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      final d = await AppScope.of(context).api.submitForReview();
      if (!mounted) return;
      setState(() {
        _driver = d;
        _submitting = false;
      });
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = e.message;
      });
      _load();
    }
  }

  Future<void> _choosePlan() async {
    await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => const PlanScreen()));
    if (mounted) _load();
  }

  @override
  Widget build(BuildContext context) {
    final d = _driver;
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const FlowHeader(label: 'Step 5 of 5', showBack: false),
            Expanded(
              child: d == null
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
                        key: const Key('verification.list'),
                        physics: const AlwaysScrollableScrollPhysics(),
                        padding: const EdgeInsets.fromLTRB(20, 4, 20, 16),
                        children: _content(d),
                      ),
                    ),
            ),
            if (d != null) _bottom(d),
          ],
        ),
      ),
    );
  }

  String _title(DriverStatus s) => switch (s) {
        DriverStatus.changesNeeded => 'A few things need fixing',
        DriverStatus.approved => "You're approved",
        DriverStatus.rejected => "We can't approve you",
        _ => "We're checking your details",
      };

  List<Widget> _content(Driver d) {
    final docsRejected = d.documents.any((i) => i.state == ItemState.rejected);
    final vehicleItems = [for (final k in ItemKind.vehicleItems) d.item(k)];
    final vehicleRejected = vehicleItems.any((i) => i.state == ItemState.rejected);
    final vehicleVerified = vehicleItems.every((i) => i.state == ItemState.verified);
    final approved = d.status == DriverStatus.approved;
    final rejected = d.rejectedItems;
    return [
      Semantics(header: true, child: Text(_title(d.status), key: const Key('verification.title'), style: AppText.display(28))),
      const SizedBox(height: 18),
      _row(
        key: const Key('verification.documents'),
        icon: Icons.description_outlined,
        title: 'Documents',
        subtitle: '${d.documentsVerified} of 7 verified',
        // The count says how far review is; only Fix or Verified show here.
        state: docsRejected ? 'Fix' : (d.documentsVerified == 7 ? 'Verified' : null),
      ),
      const SizedBox(height: 10),
      _row(
        key: const Key('verification.vehicle'),
        icon: Icons.directions_car_outlined,
        title: 'Vehicle',
        subtitle: d.vehicle == null ? null : [vehicleLabel(d.vehicle!.rideType), d.vehicle!.plate].where((s) => s.isNotEmpty).join(' · '),
        state: vehicleRejected ? 'Fix' : (vehicleVerified ? 'Verified' : 'Waiting'),
      ),
      const SizedBox(height: 10),
      _row(
        key: const Key('verification.approval'),
        icon: Icons.verified_user_outlined,
        title: 'Approval',
        state: approved ? 'Done' : 'Waiting',
      ),
      if (d.status == DriverStatus.changesNeeded) ...[
        const SizedBox(height: 22),
        Text('What to fix', style: AppText.body(15, weight: FontWeight.w600)),
        const SizedBox(height: 8),
        if (rejected.isEmpty)
          Text('Everything is replaced. Submit again so we can check it.',
              style: AppText.body(14, color: AppColors.muted, height: 1.45)),
        for (final item in rejected) ...[
          CardBox(
            key: Key('verification.fix.${item.kind}'),
            child: Row(
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(item.displayLabel, style: AppText.body(15, weight: FontWeight.w600)),
                      if (item.rejectReason != null) ...[
                        const SizedBox(height: 2),
                        Text(item.rejectReason!, style: AppText.body(13, color: AppColors.orange, height: 1.35)),
                      ],
                    ],
                  ),
                ),
                LinkButton(key: Key('fix.${item.kind}'), label: 'Fix now', onPressed: () => _fix(item)),
              ],
            ),
          ),
          const SizedBox(height: 8),
        ],
      ],
      if (d.status == DriverStatus.rejected) ...[
        const SizedBox(height: 22),
        CardBox(
          key: const Key('verification.rejectReason'),
          border: Border.all(color: AppColors.orange),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Why', style: AppText.body(13, weight: FontWeight.w600, color: AppColors.muted)),
              const SizedBox(height: 4),
              Text(d.rejectReason ?? 'Your application was turned down.', style: AppText.body(15, height: 1.45)),
            ],
          ),
        ),
      ],
      if (_error != null) ...[
        const SizedBox(height: 12),
        Text(_error!, key: const Key('verification.error'), style: AppText.body(14, color: AppColors.orange, height: 1.4)),
      ],
      const SizedBox(height: 22),
      Text("We'll notify you when this changes. Usually within $reviewHours working hours.",
          style: AppText.body(13, color: AppColors.muted, height: 1.45)),
    ];
  }

  Widget? _bottomButton(Driver d) => switch (d.status) {
        DriverStatus.changesNeeded => PrimaryButton(
            label: 'Submit again',
            height: 52,
            loading: _submitting,
            onPressed: d.rejectedItems.isEmpty ? _submitAgain : null,
          ),
        DriverStatus.approved => PrimaryButton(label: 'Choose your plan', height: 52, onPressed: _choosePlan),
        DriverStatus.rejected => OutlineButtonBox(label: 'Contact support', height: 52, onPressed: () => showInfo(context, supportLine)),
        _ => null,
      };

  Widget _bottom(Driver d) {
    final button = _bottomButton(d);
    if (button == null) return const SizedBox(height: 16);
    return Padding(padding: const EdgeInsets.fromLTRB(20, 8, 20, 16), child: button);
  }

  Widget _row({required Key key, required IconData icon, required String title, String? subtitle, String? state}) {
    final color = switch (state) {
      'Verified' || 'Done' => AppColors.green,
      'Fix' => AppColors.orange,
      _ => AppColors.muted,
    };
    final stateIcon = switch (state) {
      'Verified' || 'Done' => Icons.check_circle,
      'Fix' => Icons.error_outline,
      _ => Icons.schedule,
    };
    return CardBox(
      key: key,
      child: Row(
        children: [
          Icon(icon, color: AppColors.muted),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: AppText.body(15, weight: FontWeight.w600)),
                if (subtitle != null) ...[
                  const SizedBox(height: 2),
                  Text(subtitle, style: AppText.body(13, color: AppColors.muted)),
                ],
              ],
            ),
          ),
          if (state != null) ItemStateLabel(state, color: color, icon: stateIcon),
        ],
      ),
    );
  }
}
