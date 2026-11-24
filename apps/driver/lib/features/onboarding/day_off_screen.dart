import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'documents_screen.dart';
import 'onboarding_common.dart';
import 'vehicle_screen.dart';
import 'verification_screen.dart';

const _letters = ['M', 'T', 'W', 'T', 'F', 'S', 'S'];
const _dayNames = ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'];

/// D1.4 Your day off: one weekday the driver never drives, or "No fixed day
/// off"; then "Submit for review". If anything is missing, a sheet lists it
/// with a way to each screen.
class DayOffScreen extends StatefulWidget {
  const DayOffScreen({super.key});

  @override
  State<DayOffScreen> createState() => _DayOffScreenState();
}

class _DayOffScreenState extends State<DayOffScreen> {
  Driver? _driver;
  Object? _loadError;

  /// The choice: false until one is made; then [_day] (null: no fixed day off).
  bool _chosen = false;
  int? _day;
  bool _submitting = false;
  String? _error;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_driver == null && _loadError == null) _load();
  }

  Future<void> _load() async {
    try {
      final d = await AppScope.of(context).api.me();
      if (!mounted) return;
      setState(() {
        if (_driver == null && d.dayOffSet) {
          _chosen = true;
          _day = d.dayOff;
        }
        _driver = d;
        _loadError = null;
      });
    } on ApiException catch (e) {
      if (mounted) setState(() => _loadError = e);
    }
  }

  void _choose(int? day) => setState(() {
        _chosen = true;
        _day = day;
        _error = null;
      });

  Future<void> _submit() async {
    final d = _driver;
    if (d == null || !_chosen) return;
    final api = AppScope.of(context).api;
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      if (d.status == DriverStatus.signingUp && (!d.dayOffSet || d.dayOff != _day)) {
        _driver = await api.setDayOff(_day);
      }
      await api.submitForReview();
      if (mounted) openAsRoot(context, const VerificationScreen());
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() => _submitting = false);
      final state = e.state;
      if (e.code == 'incomplete') {
        final missing = state is Driver ? state.missing : (await _missingNow());
        if (mounted) await _showMissing(missing);
      } else if (e.code == 'wrong_status') {
        openAsRoot(context, const VerificationScreen());
      } else {
        setState(() => _error = e.message);
      }
    }
  }

  Future<List<String>> _missingNow() async {
    try {
      return (await AppScope.of(context).api.me()).missing;
    } on ApiException {
      return const [];
    }
  }

  Future<void> _showMissing(List<String> missing) async {
    final target = await showModalBottomSheet<String>(
      context: context,
      backgroundColor: AppColors.card,
      showDragHandle: true,
      isScrollControlled: true,
      builder: (sheet) => SafeArea(
        child: Padding(
          key: const Key('incomplete.sheet'),
          padding: const EdgeInsets.fromLTRB(20, 0, 20, 16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('A few things are missing', style: AppText.display(22)),
              const SizedBox(height: 6),
              Text('Add these, then submit again.', style: AppText.body(14, color: AppColors.muted)),
              const SizedBox(height: 12),
              for (final kind in missing)
                InkWell(
                  key: Key('missing.$kind'),
                  onTap: () => Navigator.of(sheet).pop(kind),
                  borderRadius: BorderRadius.circular(10),
                  child: SizedBox(
                    height: 48,
                    child: Row(
                      children: [
                        const Icon(Icons.radio_button_unchecked, size: 18, color: AppColors.orange),
                        const SizedBox(width: 10),
                        Expanded(child: Text(ItemKind.label(kind), style: AppText.body(15, weight: FontWeight.w500))),
                        const Icon(Icons.chevron_right, color: AppColors.muted),
                      ],
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
    if (target == null || !mounted || target == ItemKind.dayOff) return;
    final Widget screen = ItemKind.documents.contains(target) ? const DocumentsScreen() : const VehicleScreen();
    await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => screen));
    if (mounted) _load();
  }

  @override
  Widget build(BuildContext context) {
    final d = _driver;
    final type = d?.rideType;
    final noun = type == null ? 'your vehicle type' : vehicleNoun(type);
    final city = cityName(d?.city ?? 'blr');
    final editable = d?.status == DriverStatus.signingUp;
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const FlowHeader(label: 'Step 4 of 5'),
            Expanded(
              child: d == null
                  ? LoadingBody(
                      error: _loadError,
                      onRetry: () {
                        setState(() => _loadError = null);
                        _load();
                      },
                    )
                  : ListView(
                      key: const Key('dayOff.list'),
                      padding: const EdgeInsets.fromLTRB(20, 4, 20, 16),
                      children: [
                        Semantics(header: true, child: Text('Your day off', style: AppText.display(28))),
                        const SizedBox(height: 8),
                        Text("Pick the day you never drive. You won't be offered regular rides on that day.",
                            style: AppText.body(15, color: AppColors.muted, height: 1.45)),
                        const SizedBox(height: 18),
                        Row(
                          children: [
                            for (var w = 1; w <= 7; w++) ...[
                              if (w > 1) const SizedBox(width: 6),
                              Expanded(child: _dayChip(w, editable)),
                            ],
                          ],
                        ),
                        const SizedBox(height: 10),
                        _noneChip(editable),
                        const SizedBox(height: 18),
                        NoteBox(
                          text: "You'll see every request for $noun drivers in $city, and choose the ones you want. "
                              'For a holiday, use Take time off later.',
                        ),
                        if (_error != null) ...[
                          const SizedBox(height: 10),
                          Text(_error!, key: const Key('dayOff.error'), style: AppText.body(14, color: AppColors.orange, height: 1.4)),
                        ],
                      ],
                    ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 20, 16),
              child: PrimaryButton(
                label: 'Submit for review',
                height: 52,
                loading: _submitting,
                onPressed: d != null && _chosen ? _submit : null,
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _dayChip(int weekday, bool editable) {
    final selected = _chosen && _day == weekday;
    return Semantics(
      button: true,
      selected: selected,
      label: _dayNames[weekday - 1],
      excludeSemantics: true,
      child: Material(
        key: Key('day.$weekday'),
        color: selected ? AppColors.yellow : AppColors.card,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
          side: BorderSide(color: selected ? AppColors.yellow : AppColors.line),
        ),
        child: InkWell(
          borderRadius: BorderRadius.circular(12),
          onTap: editable ? () => _choose(weekday) : null,
          child: SizedBox(
            height: 48,
            child: Center(
              child: Text(
                _letters[weekday - 1],
                style: AppText.body(16, weight: FontWeight.w700, color: selected ? AppColors.onYellow : AppColors.ink),
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _noneChip(bool editable) {
    final selected = _chosen && _day == null;
    const label = 'No fixed day off';
    return Semantics(
      button: true,
      selected: selected,
      label: label,
      excludeSemantics: true,
      child: Material(
        key: labelKey(label),
        color: selected ? AppColors.yellow : AppColors.card,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
          side: BorderSide(color: selected ? AppColors.yellow : AppColors.line),
        ),
        child: InkWell(
          borderRadius: BorderRadius.circular(12),
          onTap: editable ? () => _choose(null) : null,
          child: SizedBox(
            height: 48,
            width: double.infinity,
            child: Center(
              child: Text(label,
                  style: AppText.body(15, weight: FontWeight.w600, color: selected ? AppColors.onYellow : AppColors.ink)),
            ),
          ),
        ),
      ),
    );
  }
}
