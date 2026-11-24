import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/fares.dart';
import '../../core/models.dart';
import '../../core/schedule.dart';
import '../../ui/time_picker.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'place_search_screen.dart';
import 'ride_type_screen.dart';
import 'setup_controller.dart';

/// R1.2 Set up a regular ride ("New commute"): route, days, a time per day
/// (pickup at or arrive by), an optional return leg. Step 1 of 3.
///
/// Calls series.preview whenever an input settles, for the start date, the
/// fare and arrive-by pickup times.
class NewCommuteScreen extends StatefulWidget {
  const NewCommuteScreen({super.key, this.setup});

  /// Choices to start from, e.g. "Change times or ride type" on R1.7.
  /// Null: a new, empty setup.
  final CommuteSetup? setup;

  @override
  State<NewCommuteScreen> createState() => _NewCommuteScreenState();
}

class _NewCommuteScreenState extends State<NewCommuteScreen> {
  CommuteSetup? _setup;
  int _previewId = 0;
  final _scroll = ScrollController();

  CommuteSetup get setup => _setup!;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_setup == null) {
      _setup = (widget.setup ?? CommuteSetup(today: AppScope.of(context).clock.now()))..addListener(_changed);
      _schedulePreview();
    }
  }

  @override
  void dispose() {
    _scroll.dispose();
    // Not disposed here: screens pushed on top may still hold it.
    _setup?.removeListener(_changed);
    super.dispose();
  }

  void _changed() {
    if (!mounted) return;
    setState(() {});
    if (setup.preview == null && setup.previewError == null) _schedulePreview();
  }

  /// Inputs here are taps, so each one has settled: ask straight away.
  /// Only the latest answer is used.
  void _schedulePreview() {
    if (setup.complete) _preview();
  }

  Future<void> _preview() async {
    if (!mounted || !setup.complete) return;
    final id = ++_previewId;
    final request = setup.toRequest();
    try {
      final p = await AppScope.of(context).api.previewSeries(request);
      if (!mounted || id != _previewId) return;
      setup.setPreview(p);
    } on ApiException catch (e) {
      if (!mounted || id != _previewId) return;
      // A network error leaves the summary at its local values.
      if (e.code != 'network') setup.setPreviewError(e);
    }
  }

  Future<void> _pickPlace({required bool pickup}) async {
    final place = await Navigator.of(context).push<Place>(
      MaterialPageRoute(builder: (_) => PlaceSearchScreen(title: pickup ? 'Pickup' : 'Drop-off')),
    );
    if (place == null) return;
    pickup ? setup.setPickup(place) : setup.setDrop(place);
  }

  Future<void> _pickTime({int? day, required bool returnLeg}) async {
    final current = returnLeg
        ? (day == null ? setup.returnTime : setup.returnTimeFor(day))
        : (day == null ? setup.time : setup.timeFor(day));
    final what = returnLeg ? 'Return pickup' : (setup.timing == TimingMode.pickupAt ? 'Pickup' : 'Arrive by');
    final picked = await showFiveMinuteTimePicker(
      context,
      initial: current,
      title: day == null ? what : '$what, ${weekdayShort(day)}',
    );
    if (picked == null) return;
    if (returnLeg) {
      day == null ? setup.setReturnTime(picked) : setup.setReturnDayTime(day, picked);
    } else {
      day == null ? setup.setTime(picked) : setup.setDayTime(day, picked);
    }
  }

  Future<void> _continue() async {
    await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => RideTypeScreen(setup: setup)));
    // Back from the next steps: show the route first, with everything kept.
    if (mounted && _scroll.hasClients) _scroll.jumpTo(0);
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
            // Outside the list, so the title stays put while the form scrolls.
            const ScreenTitle(
              'New commute',
              subtitle: "Same route, same time, every day you choose. We'll find you a regular driver for these days.",
            ),
            Expanded(
              child: ListView(
                key: const Key('setup.list'),
                controller: _scroll,
                padding: const EdgeInsets.only(bottom: 16),
                children: [
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
                        _returnCard(s),
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
                  height: 54,
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
            onChanged: s.setRepeat,
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
        onTap: () => s.toggleDay(d),
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
    final arriveBy = s.timing == TimingMode.arriveBy;
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
          const SizedBox(height: 8),
          _switchRow(
            key: const Key('setup.sameTime'),
            title: 'Same time every day',
            hint: 'Turn off to set a time for each day',
            value: s.sameTime,
            onChanged: s.setSameTime,
          ),
          const SizedBox(height: 4),
          const Hairline(),
          const SizedBox(height: 8),
          if (s.sameTime)
            _sameTimeRow(s, arriveBy)
          else if (s.weekdays.isEmpty)
            Text('Pick at least one day', style: AppText.body(13, color: AppColors.muted))
          else
            for (final d in s.sortedDays)
              _dayRow(
                key: Key('setup.time.${dayCode(d)}'),
                day: d,
                time: s.timeFor(d),
                different: s.differs(d),
                note: arriveBy ? 'Pickup around ${formatTime(s.pickupTimeFor(d, RideDirection.outbound))}' : null,
                onTap: () => _pickTime(day: d, returnLeg: false),
              ),
          const SizedBox(height: 8),
          Text(
            'Your driver may arrive up to 5 minutes either side of each time.',
            style: AppText.body(12, color: AppColors.muted, height: 1.4),
          ),
        ],
      ),
    );
  }

  Widget _sameTimeRow(CommuteSetup s, bool arriveBy) {
    final (hm, ampm) = splitTime(s.time);
    final first = s.firstDay;
    return Row(
      children: [
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('$hm $ampm', key: const Key('setup.time'), style: AppText.display(30, letterSpacing: 0)),
              if (arriveBy && first != null) ...[
                const SizedBox(height: 2),
                Text(
                  'Pickup around ${formatTime(s.pickupTimeFor(first, RideDirection.outbound))}',
                  key: const Key('setup.window'),
                  style: AppText.body(13, color: AppColors.muted),
                ),
              ],
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
    );
  }

  /// One weekday's time: the day, its time, "Different" when it differs
  /// from the first chosen day, and "Change".
  Widget _dayRow({
    required Key key,
    required int day,
    required int time,
    required bool different,
    String? note,
    bool showChange = true,
    required VoidCallback onTap,
  }) {
    return Semantics(
      button: true,
      label: '${weekdayShort(day)} ${formatTime(time)}${different ? ', different' : ''}${showChange ? '. Change' : ''}',
      child: InkWell(
        key: key,
        onTap: onTap,
        borderRadius: BorderRadius.circular(10),
        child: ConstrainedBox(
          constraints: const BoxConstraints(minHeight: 48),
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 4),
            child: Row(
              children: [
                SizedBox(width: 44, child: Text(weekdayShort(day), style: AppText.body(14, weight: FontWeight.w600))),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Text(formatTime(time), style: AppText.body(15, weight: FontWeight.w600)),
                          if (different) ...[
                            const SizedBox(width: 8),
                            const StatusChip('Different', background: AppColors.orangeTint, foreground: AppColors.orangeText),
                          ],
                        ],
                      ),
                      if (note != null) Text(note, style: AppText.body(12, color: AppColors.muted)),
                    ],
                  ),
                ),
                if (showChange) Text('Change', style: AppText.body(14, weight: FontWeight.w600, color: AppColors.green)),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _switchRow({required Key key, required String title, String? hint, required bool value, required ValueChanged<bool> onChanged}) {
    return Row(
      children: [
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(title, style: AppText.body(14, weight: FontWeight.w600)),
              if (hint != null) ...[
                const SizedBox(height: 2),
                Text(hint, style: AppText.body(12, color: AppColors.muted)),
              ],
            ],
          ),
        ),
        Switch(key: key, value: value, onChanged: onChanged),
      ],
    );
  }

  Widget _returnCard(CommuteSetup s) {
    final from = s.drop?.title ?? 'your drop-off';
    return CardBox(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _switchRow(
            key: const Key('setup.return'),
            title: 'Add return trip',
            hint: s.returnTrip ? null : 'One way only',
            value: s.returnTrip,
            onChanged: s.setReturnTrip,
          ),
          if (s.returnTrip) ...[
            const SizedBox(height: 4),
            if (s.sameTime)
              Semantics(
                button: true,
                label: '${formatTime(s.returnTime)} every day from $from. Change',
                excludeSemantics: true,
                child: InkWell(
                  key: const Key('setup.returnTime'),
                  onTap: () => _pickTime(returnLeg: true),
                  child: ConstrainedBox(
                    constraints: const BoxConstraints(minHeight: 44),
                    child: Row(
                      children: [
                        Expanded(
                          child: Text(
                            '${formatTime(s.returnTime)} every day from $from',
                            style: AppText.body(14, color: AppColors.ink),
                          ),
                        ),
                        Text('Change', style: AppText.body(14, weight: FontWeight.w600, color: AppColors.green)),
                      ],
                    ),
                  ),
                ),
              )
            else
              for (final d in s.sortedDays)
                _dayRow(
                  key: Key('setup.returnTime.${dayCode(d)}'),
                  day: d,
                  time: s.returnTimeFor(d),
                  different: s.returnDiffers(d),
                  // Tapping the row changes it; one "Change" per day is enough.
                  showChange: false,
                  onTap: () => _pickTime(day: d, returnLeg: true),
                ),
          ],
        ],
      ),
    );
  }

  Widget _summaryCard(CommuteSetup s) {
    final fare = s.preview?.suggestedFare;
    final err = s.previewError;
    final days = rideDaysPerMonth(s.weekdays);
    return CardBox(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SummaryRow('Starts', s.weekdays.isEmpty ? '–' : formatDate(s.startDate), valueKey: const Key('setup.starts')),
          if (err != null) ...[
            const SizedBox(height: 4),
            Align(
              alignment: Alignment.centerRight,
              child: Text(err.message, key: const Key('setup.previewError'), textAlign: TextAlign.right, style: AppText.body(13, color: AppColors.red)),
            ),
          ],
          const SizedBox(height: 10),
          SummaryRow(
            'Rides in a month',
            s.weekdays.isEmpty ? '–' : (s.returnTrip ? 'About ${days * 2} ($days days × 2)' : 'About $days'),
            valueKey: const Key('setup.ridesPerMonth'),
          ),
          const SizedBox(height: 10),
          const Hairline(),
          const SizedBox(height: 10),
          PriceRow(
            label: 'Fare per ride',
            labelStyle: AppText.body(14, weight: FontWeight.w600),
            price: fare == null ? '–' : formatRupees(fare),
            priceKey: const Key('setup.fare'),
          ),
          const SizedBox(height: 10),
          Text(
            'You pay your driver directly after each ride, by UPI or cash. Skip or change a day until 1 hour before pickup.',
            style: AppText.body(12, color: AppColors.muted, height: 1.4),
          ),
        ],
      ),
    );
  }
}
