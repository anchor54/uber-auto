import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/fares.dart';
import '../../core/models.dart';
import '../../core/schedule.dart';
import '../../ui/icons.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'match_screen.dart';
import 'setup_controller.dart';

/// R1.3 Choose a ride type: only the types the backend config allows for
/// this booking flow in the rider's city, plus ride preferences.
class RideTypeScreen extends StatefulWidget {
  const RideTypeScreen({super.key, required this.setup});

  final CommuteSetup setup;

  @override
  State<RideTypeScreen> createState() => _RideTypeScreenState();
}

class _RideTypeScreenState extends State<RideTypeScreen> {
  CommuteSetup get setup => widget.setup;
  bool _loading = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    setup.addListener(_changed);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!_started && (setup.options == null || setup.options!.flow != setup.flow)) {
      _started = true;
      _loading = true;
      _load();
    }
  }

  bool _started = false;

  @override
  void dispose() {
    setup.removeListener(_changed);
    super.dispose();
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  Future<void> _load() async {
    _error = null;
    try {
      final o = await AppScope.of(context).api.bookingOptions(
            flow: setup.flow,
            pickup: setup.pickup!,
            drop: setup.drop!,
            pickupTime: setup.pickupTime,
          );
      if (!mounted) return;
      setup.setOptions(o);
      setState(() => _loading = false);
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = e.message;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final options = setup.options?.options ?? const <RideTypeOption>[];
    final selected = setup.rideType;
    final fare = setup.selectedFare;
    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            const FlowHeader(label: 'Step 2 of 3'),
            Expanded(
              child: ListView(
                padding: const EdgeInsets.only(bottom: 16),
                children: [
                  const ScreenTitle(
                    'Choose your ride type',
                    subtitle: "You'll get the same type on every ride. Fares are per ride, paid to your driver.",
                    padding: EdgeInsets.fromLTRB(20, 0, 20, 14),
                  ),
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        if (_loading)
                          const Padding(
                            padding: EdgeInsets.all(32),
                            child: Center(child: CircularProgressIndicator(color: AppColors.green)),
                          )
                        else if (_error != null)
                          Text(_error!, style: AppText.body(14, color: AppColors.red))
                        else if (options.isEmpty)
                          CardBox(
                            key: const Key('rideType.none'),
                            child: Text(
                              "Regular rides aren't available in your city yet. You can still book one-time rides from Home.",
                              style: AppText.body(14, height: 1.45),
                            ),
                          )
                        else
                          ..._optionList(options, selected),
                        const SizedBox(height: 10),
                        if (selected != null) _preferencesCard(selected),
                        const SizedBox(height: 10),
                        Padding(
                          padding: const EdgeInsets.symmetric(horizontal: 4),
                          child: Text(
                            'Ride types shown depend on your city and on the kind of booking.',
                            style: AppText.body(12, color: AppColors.muted, height: 1.4),
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
                if (selected != null && fare != null) ...[
                  PriceRow(
                    label: '${selected.label} · per ride',
                    price: formatRupees(fare),
                    priceKey: const Key('rideType.fare'),
                  ),
                  const SizedBox(height: 8),
                ],
                PrimaryButton(
                  key: const Key('rideType.continue'),
                  label: 'Find my driver',
                  onPressed: selected == null || _loading
                      ? null
                      : () => Navigator.of(context).push(
                            MaterialPageRoute<void>(builder: (_) => MatchScreen(setup: setup)),
                          ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  List<Widget> _optionList(List<RideTypeOption> options, RideType? selected) {
    final nonCab = options.where((o) => !o.type.isCab).toList();
    final cab = options.where((o) => o.type.isCab).toList();
    return [
      for (final o in nonCab) ...[_optionCard(o, o.type == selected), const SizedBox(height: 8)],
      if (cab.isNotEmpty) ...[
        Padding(
          padding: const EdgeInsets.fromLTRB(2, 6, 2, 8),
          child: Text('CAB', style: AppText.body(12, weight: FontWeight.w700, color: AppColors.muted, letterSpacing: 0.6)),
        ),
        for (final o in cab) ...[_optionCard(o, o.type == selected), const SizedBox(height: 8)],
      ],
    ];
  }

  Widget _optionCard(RideTypeOption o, bool isSelected) {
    final iconColor = isSelected ? AppColors.green : AppColors.ink;
    final note = o.driversOnRoute == 0
        ? "No ${o.type.noun} drivers on your route yet. We'll still look, or add you to the waitlist."
        : '${o.driversOnRoute} ${o.type.noun} driver${o.driversOnRoute == 1 ? '' : 's'} '
            '${o.driversOnRoute == 1 ? 'runs' : 'run'} your route at ${formatTime(setup.pickupTime)}';
    return Semantics(
      selected: isSelected,
      button: true,
      label: '${o.type.label}, ${formatRupees(o.fare)} per ride',
      child: GestureDetector(
        key: Key('rideType.${o.type.configKey}'),
        onTap: () => setup.setRideType(o.type),
        child: Container(
          padding: EdgeInsets.all(isSelected ? 13 : 14),
          decoration: BoxDecoration(
            color: AppColors.surface,
            borderRadius: BorderRadius.circular(14),
            border: Border.all(color: isSelected ? AppColors.green : AppColors.cardBorder, width: isSelected ? 2 : 1),
          ),
          child: Column(
            children: [
              Row(
                children: [
                  SizedBox(width: 48, child: Center(child: AppIcon(_iconFor(o.type), size: 48, color: iconColor))),
                  const SizedBox(width: 14),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(o.type.label, style: AppText.body(16, weight: FontWeight.w600)),
                        const SizedBox(height: 2),
                        Text(o.type.description, style: AppText.body(13, color: AppColors.muted)),
                      ],
                    ),
                  ),
                  Text(formatRupees(o.fare), style: AppText.body(15, weight: FontWeight.w600)),
                ],
              ),
              if (isSelected) ...[
                const SizedBox(height: 8),
                const Hairline(),
                const SizedBox(height: 8),
                Align(
                  alignment: Alignment.centerLeft,
                  child: Text(
                    note,
                    key: const Key('rideType.note'),
                    style: AppText.body(13, weight: FontWeight.w500, color: AppColors.green),
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }

  Widget _preferencesCard(RideType type) {
    final prefs = [
      if (type.isCab) RidePreference.acOn,
      RidePreference.quiet,
      RidePreference.womanDriver,
    ];
    return CardBox(
      padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Ride preferences', style: AppText.body(15, weight: FontWeight.w600)),
          const SizedBox(height: 10),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final p in prefs) _prefChip(p, setup.preferences.contains(p)),
            ],
          ),
        ],
      ),
    );
  }

  Widget _prefChip(RidePreference p, bool on) {
    return Semantics(
      button: true,
      toggled: on,
      label: p.label,
      excludeSemantics: true,
      child: GestureDetector(
        key: Key('pref.${p.name}'),
        onTap: () => setup.togglePreference(p),
        child: Container(
          height: 40,
          padding: const EdgeInsets.symmetric(horizontal: 14),
          decoration: BoxDecoration(
            color: on ? AppColors.ink : AppColors.surface,
            borderRadius: BorderRadius.circular(999),
            border: on ? null : Border.all(color: AppColors.line),
          ),
          alignment: Alignment.center,
          child: Text(
            p.label,
            style: AppText.body(13, weight: on ? FontWeight.w600 : FontWeight.w500, color: on ? Colors.white : AppColors.ink),
          ),
        ),
      ),
    );
  }
}

AppIconData _iconFor(RideType t) => switch (t) {
      RideType.bike => AppIcons.bike,
      RideType.auto => AppIcons.auto,
      RideType.cabHatchback => AppIcons.hatchback,
      RideType.cabSedan => AppIcons.sedan,
      RideType.cabSuv => AppIcons.suv,
    };

AppIconData rideTypeIcon(RideType t) => _iconFor(t);
