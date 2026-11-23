import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../core/schedule.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';

/// D2.3 Your week: the week's rides as a timetable (a row per rider, a
/// column per day), a banner after a win with the new rider's row tagged
/// "New rider", and the driver's free time. Rides show here as soon as
/// they're created, 14 days ahead.
class WeekScreen extends StatefulWidget {
  const WeekScreen({super.key, this.start, this.onOpenRequests});

  /// Monday of the week to show; null: this week.
  final DateTime? start;

  /// Goes to the Requests tab. Null: pops back.
  final VoidCallback? onOpenRequests;

  @override
  State<WeekScreen> createState() => _WeekScreenState();
}

class _WeekScreenState extends State<WeekScreen> {
  DateTime? _start;
  Future<DriverWeek>? _data;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_data == null) {
      _start = weekStart(widget.start ?? AppScope.of(context).clock.now());
      _load();
    }
  }

  void _load() => _data = AppScope.of(context).api.week(start: _start);

  void _move(int weeks) => setState(() {
        _start = addDays(_start!, 7 * weeks);
        _load();
      });

  Future<void> _refresh() async {
    setState(_load);
    try {
      await _data;
    } catch (_) {
      // Shown on screen.
    }
  }

  void _openRequests() {
    final open = widget.onOpenRequests;
    if (open != null) {
      open();
    } else {
      Navigator.of(context).maybePop();
    }
  }

  @override
  Widget build(BuildContext context) {
    final body = SafeArea(
      child: FutureBuilder<DriverWeek>(
        future: _data,
        builder: (context, snap) {
          final w = snap.data;
          return RefreshIndicator(
            color: AppColors.yellow,
            onRefresh: _refresh,
            child: ListView(
              key: const Key('week.list'),
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.fromLTRB(16, 20, 16, 24),
              children: [
                Row(
                  children: [
                    Expanded(child: Semantics(header: true, child: Text('Your week', style: AppText.display(28)))),
                    IconButton(
                      key: const Key('week.prev'),
                      tooltip: 'Previous week',
                      onPressed: () => _move(-1),
                      icon: const Icon(Icons.chevron_left, color: AppColors.ink),
                    ),
                    IconButton(
                      key: const Key('week.next'),
                      tooltip: 'Next week',
                      onPressed: () => _move(1),
                      icon: const Icon(Icons.chevron_right, color: AppColors.ink),
                    ),
                  ],
                ),
                if (snap.hasError)
                  Padding(
                    padding: const EdgeInsets.only(top: 16),
                    child: Text(
                      snap.error is ApiException ? (snap.error as ApiException).message : 'Something went wrong loading your week.',
                      style: AppText.body(14, color: AppColors.muted),
                    ),
                  )
                else if (w == null)
                  const Padding(
                    padding: EdgeInsets.all(32),
                    child: Center(child: CircularProgressIndicator(color: AppColors.yellow)),
                  )
                else
                  ..._content(w),
              ],
            ),
          );
        },
      ),
    );
    // Pushed on its own (not as a tab): give it a back arrow.
    if (widget.onOpenRequests == null && Navigator.of(context).canPop()) {
      return Scaffold(body: Column(children: [const FlowHeader(), Expanded(child: body)]));
    }
    return body;
  }

  List<Widget> _content(DriverWeek w) {
    final n = w.rides.length;
    // One row per rider (series), in order of their first pickup.
    final bySeries = <String, List<WeekRide>>{};
    for (final r in w.rides) {
      bySeries.putIfAbsent(r.seriesId ?? r.riderName, () => []).add(r);
    }
    final newGroup = bySeries.values.where((rs) => rs.any((r) => r.isNew)).firstOrNull;
    return [
      Text('${formatWeekRange(w.weekStart, w.weekEnd)} · $n ${n == 1 ? 'ride' : 'rides'}',
          key: const Key('week.summary'), style: AppText.body(14, color: AppColors.muted)),
      const SizedBox(height: 16),
      if (newGroup != null) ...[_winBanner(newGroup), const SizedBox(height: 14)],
      CardBox(child: _timetable(w, bySeries)),
      const SizedBox(height: 14),
      _freeTime(w),
      const SizedBox(height: 16),
      PrimaryButton(key: const Key('week.requests'), label: 'Requests', onPressed: _openRequests),
      const SizedBox(height: 10),
      OutlineButtonBox(
        key: const Key('week.timeOff'),
        label: 'Take time off',
        height: 54,
        onPressed: () => showInfo(context, 'Taking time off comes with flow D5.'),
      ),
    ];
  }

  Widget _winBanner(List<WeekRide> rides) {
    final first = rides.first.riderFirstName;
    final from = rides.map((r) => r.date).reduce((a, b) => a.isBefore(b) ? a : b);
    return CardBox(
      key: const Key('week.winBanner'),
      color: AppColors.green,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text("You're $first's regular driver from ${formatDate(from)}.",
              style: AppText.body(16, weight: FontWeight.w700, color: AppColors.onYellow, height: 1.3)),
          const SizedBox(height: 4),
          Text("You were picked for $first's request, and $first has been told.",
              style: AppText.body(13, color: AppColors.onYellow, height: 1.4)),
        ],
      ),
    );
  }

  /// "8:30 AM · back 6:30 PM", with other times listed.
  String _times(List<WeekRide> rides) {
    final out = {for (final r in rides) if (r.direction == RideDirection.outbound) r.pickupTime}.toList()..sort();
    final back = {for (final r in rides) if (r.direction == RideDirection.returnLeg) r.pickupTime}.toList()..sort();
    return [
      if (out.isNotEmpty) out.map(formatTime).join(', '),
      if (back.isNotEmpty) 'back ${back.map(formatTime).join(', ')}',
    ].join(' · ');
  }

  Widget _timetable(DriverWeek w, Map<String, List<WeekRide>> bySeries) {
    if (bySeries.isEmpty) {
      return Text('No rides this week yet.', key: const Key('week.empty'), style: AppText.body(14, color: AppColors.muted));
    }
    final head = AppText.body(11, weight: FontWeight.w600, color: AppColors.muted);
    final groups = bySeries.values.toList()
      ..sort((a, b) => a.map((r) => r.pickupTime).reduce((x, y) => x < y ? x : y).compareTo(b.map((r) => r.pickupTime).reduce((x, y) => x < y ? x : y)));
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            const Spacer(flex: 3),
            for (var d = 1; d <= 7; d++) Expanded(child: Text(weekdayShort(d), textAlign: TextAlign.center, style: head)),
          ],
        ),
        const SizedBox(height: 6),
        for (final rides in groups) ...[
          const Hairline(),
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 10),
            child: Row(
              children: [
                Expanded(
                  flex: 3,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(rides.first.riderFirstName, style: AppText.body(14, weight: FontWeight.w600)),
                      Text(_times(rides), style: AppText.body(11, color: AppColors.muted)),
                      if (rides.any((r) => r.isNew)) ...[
                        const SizedBox(height: 4),
                        const Chip2('New rider', background: AppColors.greenDark, foreground: AppColors.green),
                      ],
                    ],
                  ),
                ),
                for (var d = 1; d <= 7; d++) Expanded(child: _countCell(rides.where((r) => r.weekday == d).length)),
              ],
            ),
          ),
        ],
      ],
    );
  }

  Widget _countCell(int count) => Center(
        child: count == 0
            ? Text('·', style: AppText.body(14, color: AppColors.line))
            : Container(
                width: 24,
                height: 24,
                alignment: Alignment.center,
                decoration: const BoxDecoration(color: AppColors.yellow, shape: BoxShape.circle),
                child: Text('$count', style: AppText.body(12, weight: FontWeight.w700, color: AppColors.onYellow)),
              ),
      );

  /// "Free every weekday 9:20 AM – 9:00 PM": after the last morning drop-off,
  /// before the first afternoon pickup, Monday to Friday.
  Widget _freeTime(DriverWeek w) {
    final weekday = w.rides.where((r) => r.weekday <= 5);
    var from = 7 * 60;
    var to = 21 * 60;
    for (final r in weekday) {
      final end = r.dropTime ?? r.pickupTime + 30;
      if (r.pickupTime < 12 * 60 && end > from) from = end;
      if (r.pickupTime >= 12 * 60 && r.pickupTime < to) to = r.pickupTime;
    }
    return CardBox(
      key: const Key('week.free'),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Free every weekday ${formatTime(from)} – ${formatTime(to)}', style: AppText.body(15, weight: FontWeight.w600)),
          const SizedBox(height: 2),
          LinkButton(
            key: const Key('week.midday'),
            label: 'Open to midday commute requests',
            onPressed: _openRequests,
          ),
        ],
      ),
    );
  }
}
