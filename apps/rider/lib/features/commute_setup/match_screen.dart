import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/fares.dart';
import '../../core/models.dart';
import '../../core/schedule.dart';
import '../../ui/map_preview.dart';
import '../../ui/ride_map.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import '../home/home_screen.dart';
import 'confirm_screen.dart';
import 'setup_controller.dart';

/// R1.4 Driver match: the proposed regular driver, or what to do when
/// nobody can take the commute yet.
class MatchScreen extends StatefulWidget {
  const MatchScreen({super.key, required this.setup});

  final CommuteSetup setup;

  @override
  State<MatchScreen> createState() => _MatchScreenState();
}

class _MatchScreenState extends State<MatchScreen> {
  CommuteSetup get setup => widget.setup;
  List<DriverMatch>? _matches;
  bool _searching = true;
  bool _started = false;
  bool _joining = false;
  String? _error;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!_started) {
      _started = true;
      _search();
    }
  }

  Future<void> _search() async {
    try {
      final results = await AppScope.of(context).api.findDrivers(setup.toRequest());
      if (!mounted) return;
      if (results.isNotEmpty) setup.setMatch(results.first);
      setState(() {
        _matches = results;
        _searching = false;
        _error = null;
      });
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _searching = false;
        _error = e.message;
      });
    }
  }

  void _retry(void Function() change) {
    change();
    setState(() => _searching = true);
    _search();
  }

  Future<void> _joinWaitlist() async {
    setState(() => _joining = true);
    try {
      await AppScope.of(context).api.joinWaitlist(setup.toRequest());
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() => _joining = false);
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(e.message)));
      return;
    }
    if (!mounted) return;
    final messenger = ScaffoldMessenger.of(context);
    Navigator.of(context).pushAndRemoveUntil(
      MaterialPageRoute<void>(builder: (_) => const HomeScreen()),
      (_) => false,
    );
    messenger.showSnackBar(
      const SnackBar(content: Text("You're on the waitlist. We'll tell you as soon as a driver is free.")),
    );
  }

  Future<void> _showOthers() async {
    final others = _matches ?? const <DriverMatch>[];
    final picked = await showModalBottomSheet<DriverMatch>(
      context: context,
      backgroundColor: AppColors.ground,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Other drivers on your route', style: AppText.display(20)),
              const SizedBox(height: 12),
              for (final m in others) ...[
                InkWell(
                  key: Key('others.${m.driver.id}'),
                  onTap: () => Navigator.of(context).pop(m),
                  borderRadius: BorderRadius.circular(14),
                  child: CardBox(
                    border: Border.all(
                      color: m.driver.id == setup.match?.driver.id ? AppColors.green : AppColors.cardBorder,
                      width: m.driver.id == setup.match?.driver.id ? 2 : 1,
                    ),
                    padding: const EdgeInsets.all(12),
                    child: Row(
                      children: [
                        Avatar(m.driver.initials, size: 40),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(m.driver.name, style: AppText.body(15, weight: FontWeight.w600)),
                              Text(m.driver.vehicle, style: AppText.body(13, color: AppColors.muted)),
                            ],
                          ),
                        ),
                        Text('${m.driver.rating}', style: AppText.body(13, weight: FontWeight.w600)),
                      ],
                    ),
                  ),
                ),
                const SizedBox(height: 8),
              ],
            ],
          ),
        ),
      ),
    );
    if (picked != null) setup.setMatch(picked);
    if (mounted) setState(() {});
  }

  @override
  Widget build(BuildContext context) {
    final match = setup.match;
    final found = !_searching && match != null && (_matches?.isNotEmpty ?? false);
    final m = match;
    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            const FlowHeader(label: 'Step 3 of 3'),
            Expanded(
              child: _searching
                  ? _searchingView()
                  : _error != null
                      ? _errorView()
                      : found
                          ? _foundView(m!)
                          : _noMatchView(),
            ),
            if (found && m != null)
              BottomActions(
                children: [
                  PriceRow(
                    label: 'Fare per ride, paid to ${m.driver.firstName}',
                    price: formatRupees(m.fare),
                    priceKey: const Key('match.fare'),
                  ),
                  const SizedBox(height: 10),
                  PrimaryButton(
                    key: const Key('match.continue'),
                    label: 'Continue',
                    onPressed: () => Navigator.of(context).push(
                      MaterialPageRoute<void>(builder: (_) => ConfirmScreen(setup: setup)),
                    ),
                  ),
                  if ((_matches?.length ?? 0) > 1)
                    LinkButton(key: const Key('match.others'), label: 'See other drivers', onPressed: _showOthers),
                ],
              ),
          ],
        ),
      ),
    );
  }

  Widget _errorView() {
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        key: const Key('match.error'),
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Text(_error!, textAlign: TextAlign.center, style: AppText.body(15, color: AppColors.muted, height: 1.45)),
          const SizedBox(height: 16),
          PrimaryButton(key: const Key('match.retry'), label: 'Try again', onPressed: () => _retry(() {})),
        ],
      ),
    );
  }

  Widget _searchingView() {
    return Column(
      key: const Key('match.searching'),
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        const CircularProgressIndicator(color: AppColors.green),
        const SizedBox(height: 16),
        Text('Finding your driver…', style: AppText.display(20)),
        const SizedBox(height: 6),
        Text(
          'Checking ${setup.rideType?.noun ?? ''} drivers who already run your route',
          style: AppText.body(14, color: AppColors.muted),
        ),
      ],
    );
  }

  Widget _foundView(DriverMatch m) {
    final r = setup.toRequest();
    final km = m.trip.distanceKm.round();
    return ListView(
      key: const Key('match.found'),
      padding: const EdgeInsets.only(bottom: 16),
      children: [
        const ScreenTitle('We found your driver', padding: EdgeInsets.fromLTRB(20, 0, 20, 14)),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: Column(
            children: [
              RideMap(
                pickup: setup.pickup!,
                drop: setup.drop!,
                route: m.trip.polyline,
                height: 170,
                label: 'About ${m.trip.minutes} min · $km km',
                fallback: RouteMapPreview(label: 'About ${m.trip.minutes} min · $km km'),
              ),
              const SizedBox(height: 12),
              CardBox(
                child: Column(
                  children: [
                    Row(
                      children: [
                        Avatar(m.driver.initials),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(m.driver.name, key: const Key('match.driverName'), style: AppText.body(17, weight: FontWeight.w600)),
                              const SizedBox(height: 3),
                              Text(m.driver.vehicle, style: AppText.body(13, color: AppColors.muted)),
                            ],
                          ),
                        ),
                        Container(
                          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                          decoration: BoxDecoration(color: AppColors.chip, borderRadius: BorderRadius.circular(8)),
                          child: Text('${m.driver.rating} rating', style: AppText.body(13, weight: FontWeight.w600)),
                        ),
                      ],
                    ),
                    const SizedBox(height: 12),
                    for (final reason in m.reasons) ...[
                      CheckLine(reason),
                      const SizedBox(height: 8),
                    ],
                  ],
                ),
              ),
              const SizedBox(height: 12),
              Container(
                decoration: BoxDecoration(color: AppColors.surface, borderRadius: BorderRadius.circular(16)),
                child: IntrinsicHeight(
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Expanded(child: _scheduleCell(r.pickupTime < 720 ? 'Morning' : 'Pickup', r.pickupTime, r.weekdays)),
                      if (r.returnTrip) ...[
                        Container(width: 1, color: AppColors.divider),
                        Expanded(child: _scheduleCell(r.returnTime < 720 ? 'Return' : 'Evening', r.returnTime, r.weekdays)),
                      ],
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }

  Widget _scheduleCell(String label, int time, Set<int> days) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label, style: AppText.body(12, color: AppColors.muted)),
          const SizedBox(height: 2),
          Text(formatTime(time), style: AppText.body(16, weight: FontWeight.w600)),
          const SizedBox(height: 2),
          Text(daysLabel(days), style: AppText.body(12, color: AppColors.muted)),
        ],
      ),
    );
  }

  Widget _noMatchView() {
    final type = setup.rideType;
    final hasWoman = setup.preferences.contains(RidePreference.womanDriver);
    final later = setup.time + 15;
    return ListView(
      key: const Key('match.none'),
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
      children: [
        const ScreenTitle('No driver yet', padding: EdgeInsets.fromLTRB(4, 0, 4, 8)),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 4),
          child: Text(
            'No ${type?.noun ?? ''} driver can take this commute right now'
            '${hasWoman ? ' with your preferences' : ''}. Try one of these:',
            style: AppText.body(14, color: AppColors.muted, height: 1.45),
          ),
        ),
        const SizedBox(height: 16),
        if (hasWoman) ...[
          OutlineButtonBox(
            key: const Key('match.relax'),
            label: 'Search without "Woman driver"',
            height: 52,
            onPressed: () => _retry(() => setup.removePreference(RidePreference.womanDriver)),
          ),
          const SizedBox(height: 10),
        ],
        OutlineButtonBox(
          key: const Key('match.later'),
          label: setup.timing == TimingMode.pickupAt
              ? 'Try ${formatTime(later)} instead'
              : 'Arrive by ${formatTime(later)} instead',
          height: 52,
          onPressed: () => _retry(() => setup.setTime(later)),
        ),
        const SizedBox(height: 10),
        PrimaryButton(
          key: const Key('match.waitlist'),
          label: 'Join the waitlist',
          loading: _joining,
          onPressed: _joinWaitlist,
        ),
      ],
    );
  }
}
