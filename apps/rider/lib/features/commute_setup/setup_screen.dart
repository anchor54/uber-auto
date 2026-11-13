import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/fares.dart';
import '../../core/models.dart';
import '../../core/schedule.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'place_search_screen.dart';
import 'ride_type_screen.dart';
import 'setup_controller.dart';

/// R1.2 Set up a commute: route, repeat, timing, return trip.
class SetupScreen extends StatefulWidget {
  const SetupScreen({super.key});

  @override
  State<SetupScreen> createState() => _SetupScreenState();
}

class _SetupScreenState extends State<SetupScreen> {
  CommuteSetup? _setup;
  int _loadId = 0;

  CommuteSetup get setup => _setup!;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _setup ??= CommuteSetup(today: AppScope.of(context).clock.now())..addListener(_changed);
  }

  @override
  void dispose() {
    // Not disposed here: screens pushed on top may still hold it while the
    // whole flow is removed at once after confirming.
    _setup?.removeListener(_changed);
    super.dispose();
  }

  void _changed() => setState(() {});

  Future<void> _pickPlace({required bool pickup}) async {
    final place = await Navigator.of(context).push<Place>(
      MaterialPageRoute(builder: (_) => PlaceSearchScreen(title: pickup ? 'Pickup' : 'Drop-off')),
    );
    if (place == null) return;
    pickup ? setup.setPickup(place) : setup.setDrop(place);
    await _loadRoute();
  }

  /// Once both ends are known, fetch the trip estimate and fares.
  Future<void> _loadRoute() async {
    final a = setup.pickup, b = setup.drop;
    if (a == null || b == null || a == b) return;
    final id = ++_loadId;
    final api = AppScope.of(context).api;
    // Only the summary's preview: if it can't load, the summary shows "–" and
    // the ride-type step loads (and reports errors) on its own.
    try {
      final trip = await api.estimateTrip(a, b);
      if (!mounted || id != _loadId) return;
      setup.setTrip(trip);
    } on ApiException {
      return;
    }
    await _loadOptions();
  }

  Future<void> _loadOptions() async {
    final a = setup.pickup, b = setup.drop;
    if (a == null || b == null || a == b) return;
    final id = ++_loadId;
    try {
      final options = await AppScope.of(context).api.bookingOptions(
            flow: setup.flow,
            pickup: a,
            drop: b,
            pickupTime: setup.pickupTime,
          );
      if (!mounted || id != _loadId) return;
      setup.setOptions(options);
    } on ApiException {
      // As above: a preview only.
    }
  }

  Future<void> _pickTime({required bool returnLeg}) async {
    final current = returnLeg ? setup.returnTime : setup.time;
    final picked = await showTimePicker(
      context: context,
      initialTime: TimeOfDay(hour: current ~/ 60, minute: current % 60),
      helpText: returnLeg
          ? 'Return pickup time'
          : (setup.timing == TimingMode.pickupAt ? 'Pickup time' : 'Arrive by'),
    );
    if (picked == null) return;
    final minutes = picked.hour * 60 + picked.minute;
    returnLeg ? setup.setReturnTime(minutes) : setup.setTime(minutes);
  }

  void _continue() {
    Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => RideTypeScreen(setup: setup)));
  }

  @override
  Widget build(BuildContext context) {
    final s = setup;
    final blocker = s.blocker;
    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            const FlowHeader(label: 'Step 1 of 3'),
            Expanded(
              child: ListView(
                key: const Key('setup.list'),
                padding: const EdgeInsets.only(bottom: 16),
                children: [
                  const ScreenTitle(
                    'New commute',
                    subtitle: 'Same route, same time, every day you choose. We match you with one regular driver.',
                  ),
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    child: Column(
                      children: [
                        _routeCard(s),
                        const SizedBox(height: 14),
                        _repeatCard(s),
                        const SizedBox(height: 14),
                        _timingCard(s),
                        const SizedBox(height: 14),
                        _summaryCard(s),
                      ],
                    ),
                  ),
                ],
              ),
            ),
            BottomActions(
              children: [
                if (blocker != null && (s.pickup != null || s.drop != null)) ...[
                  Text(blocker, key: const Key('setup.blocker'), style: AppText.body(13, color: AppColors.muted)),
                  const SizedBox(height: 8),
                ],
                PrimaryButton(
                  key: const Key('setup.continue'),
                  label: 'Choose ride type',
                  onPressed: blocker == null ? _continue : null,
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Widget _routeCard(CommuteSetup s) {
    return CardBox(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _placeField(
            key: const Key('setup.pickup'),
            label: 'Pickup',
            value: s.pickup?.display,
            hint: 'Where do we pick you up?',
            onTap: () => _pickPlace(pickup: true),
          ),
          const SizedBox(height: 12),
          _placeField(
            key: const Key('setup.drop'),
            label: 'Drop-off',
            value: s.drop?.display,
            hint: 'Where are you going?',
            onTap: () => _pickPlace(pickup: false),
          ),
          if (s.samePlace) ...[
            const SizedBox(height: 8),
            Text("Pickup and drop-off can't be the same place", style: AppText.body(13, color: AppColors.red)),
          ],
        ],
      ),
    );
  }

  Widget _placeField({required Key key, required String label, required String? value, required String hint, required VoidCallback onTap}) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(label, style: AppText.body(12, weight: FontWeight.w600, color: AppColors.muted)),
        const SizedBox(height: 4),
        Semantics(
          button: true,
          label: '$label: ${value ?? hint}',
          excludeSemantics: true,
          child: InkWell(
            key: key,
            onTap: onTap,
            borderRadius: BorderRadius.circular(10),
            child: Container(
              height: 44,
              padding: const EdgeInsets.symmetric(horizontal: 12),
              decoration: BoxDecoration(
                color: AppColors.inputFill,
                border: Border.all(color: AppColors.line),
                borderRadius: BorderRadius.circular(10),
              ),
              alignment: Alignment.centerLeft,
              child: Text(
                value ?? hint,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: AppText.body(15, weight: FontWeight.w500, color: value == null ? AppColors.outline : AppColors.ink),
              ),
            ),
          ),
        ),
      ],
    );
  }

  Widget _repeatCard(CommuteSetup s) {
    return CardBox(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Repeat', style: AppText.body(15, weight: FontWeight.w600)),
          const SizedBox(height: 10),
          PillSegments<CommuteRepeat>(
            label: 'Repeat',
            value: s.repeat,
            options: const [
              (CommuteRepeat.everyWeekday, 'Every weekday'),
              (CommuteRepeat.chosenDays, 'Chosen days each week'),
            ],
            onChanged: (m) {
              s.setRepeat(m);
              _loadOptions();
            },
          ),
          const SizedBox(height: 10),
          Text(
            'Weekly rides work too, e.g. only Saturdays. Pick the days below.',
            style: AppText.body(12, color: AppColors.muted, height: 1.4),
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              for (var d = 1; d <= 7; d++) ...[
                if (d > 1) const SizedBox(width: 6),
                Expanded(child: _dayButton(s, d)),
              ],
            ],
          ),
          if (s.weekdays.isEmpty) ...[
            const SizedBox(height: 8),
            Text('Pick at least one day', style: AppText.body(13, color: AppColors.red)),
          ],
        ],
      ),
    );
  }

  Widget _dayButton(CommuteSetup s, int d) {
    final on = s.weekdays.contains(d);
    const letters = ['M', 'T', 'W', 'T', 'F', 'S', 'S'];
    return Semantics(
      button: true,
      toggled: on,
      label: weekdayShort(d),
      excludeSemantics: true,
      child: GestureDetector(
        key: Key('setup.day.$d'),
        onTap: () {
          final wasWeekday = s.repeat == CommuteRepeat.everyWeekday;
          s.toggleDay(d);
          if (wasWeekday) _loadOptions();
        },
        child: Container(
          height: 44,
          decoration: BoxDecoration(
            color: on ? AppColors.green : AppColors.surface,
            borderRadius: BorderRadius.circular(10),
            border: on ? null : Border.all(color: AppColors.line),
          ),
          alignment: Alignment.center,
          child: Text(
            letters[d - 1],
            style: AppText.body(13, weight: FontWeight.w600, color: on ? Colors.white : AppColors.ink),
          ),
        ),
      ),
    );
  }

  Widget _timingCard(CommuteSetup s) {
    final (hm, ampm) = splitTime(s.time);
    final arriveBy = s.timing == TimingMode.arriveBy;
    final drop = s.drop?.title ?? 'your drop-off';
    return CardBox(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Timing', style: AppText.body(15, weight: FontWeight.w600)),
          const SizedBox(height: 12),
          PillSegments<TimingMode>(
            label: 'Timing',
            value: s.timing,
            options: const [
              (TimingMode.pickupAt, 'Pick me up at'),
              (TimingMode.arriveBy, 'Get me there by'),
            ],
            onChanged: s.setTiming,
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('$hm $ampm', key: const Key('setup.time'), style: AppText.display(30, letterSpacing: 0)),
                    const SizedBox(height: 2),
                    Text(
                      arriveBy
                          ? 'Pickup around ${formatTime(s.pickupTime)}${s.trip == null ? '' : ' (about ${s.trip!.minutes} min trip)'}'
                          : 'Driver may arrive ${pickupWindow(s.time)}',
                      key: const Key('setup.window'),
                      style: AppText.body(13, color: AppColors.muted),
                    ),
                  ],
                ),
              ),
              OutlineButtonBox(
                key: const Key('setup.changeTime'),
                label: 'Change',
                expand: false,
                onPressed: () => _pickTime(returnLeg: false),
              ),
            ],
          ),
          const SizedBox(height: 12),
          const Hairline(),
          const SizedBox(height: 12),
          Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('Add return trip', style: AppText.body(14, weight: FontWeight.w600)),
                    const SizedBox(height: 2),
                    if (s.returnTrip)
                      GestureDetector(
                        key: const Key('setup.returnTime'),
                        onTap: () => _pickTime(returnLeg: true),
                        child: Text.rich(
                          TextSpan(
                            style: AppText.body(13, color: AppColors.muted),
                            children: [
                              TextSpan(text: '${formatTime(s.returnTime)} from $drop · '),
                              TextSpan(
                                text: 'Change',
                                style: AppText.body(13, weight: FontWeight.w600, color: AppColors.green),
                              ),
                            ],
                          ),
                        ),
                      )
                    else
                      Text('One way only', style: AppText.body(13, color: AppColors.muted)),
                  ],
                ),
              ),
              Switch(
                key: const Key('setup.return'),
                value: s.returnTrip,
                onChanged: s.setReturnTrip,
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _summaryCard(CommuteSetup s) {
    final low = s.lowestFare;
    return CardBox(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SummaryRow(
            'Starts',
            s.weekdays.isEmpty ? '–' : formatDate(s.startDate),
            valueKey: const Key('setup.starts'),
          ),
          const SizedBox(height: 10),
          SummaryRow(
            'Rides in a month',
            s.weekdays.isEmpty ? '–' : ridesPerMonthLabel(s.weekdays, returnTrip: s.returnTrip),
            valueKey: const Key('setup.ridesPerMonth'),
          ),
          const SizedBox(height: 10),
          const Hairline(),
          const SizedBox(height: 10),
          PriceRow(
            label: 'Fare per ride',
            labelStyle: AppText.body(14, weight: FontWeight.w600),
            price: low == null ? '–' : 'From ${formatRupees(low)}',
            priceKey: const Key('setup.fareFrom'),
          ),
          const SizedBox(height: 10),
          Text(
            'You pay your driver directly after each ride, by UPI or cash. '
            'Skip a day by 9 PM the night before so your driver can plan.',
            style: AppText.body(12, color: AppColors.muted, height: 1.4),
          ),
        ],
      ),
    );
  }
}
