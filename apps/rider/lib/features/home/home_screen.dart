import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../core/schedule.dart';
import '../../ui/icons.dart';
import '../../ui/tab_bar.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import '../commute_setup/setup_screen.dart';

/// R1.6 Home (with a commute) and R1.6b Home (no regular ride yet).
class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  Future<HomeData>? _data;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _data ??= AppScope.of(context).api.home();
  }

  void _reload() => setState(() => _data = AppScope.of(context).api.home());

  Future<void> _startSetup() async {
    await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => const SetupScreen()));
    if (mounted) _reload();
  }

  void _comingSoon(String what, String flow) => showInfo(context, '$what comes with flow $flow.');

  @override
  Widget build(BuildContext context) {
    final now = AppScope.of(context).clock.now();
    return Scaffold(
      body: SafeArea(
        bottom: false,
        child: FutureBuilder<HomeData>(
          future: _data,
          builder: (context, snap) {
            if (!snap.hasData) {
              return const Center(child: CircularProgressIndicator(color: AppColors.green));
            }
            final data = snap.data!;
            final commute = data.commute;
            return ListView(
              key: Key(commute == null ? 'home.empty' : 'home.commute'),
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 16),
              children: [
                _whereTo(now, data.profile),
                const SizedBox(height: 14),
                if (commute == null) ..._emptyState(data) else ..._commuteState(commute, now),
              ],
            );
          },
        ),
      ),
      bottomNavigationBar: RiderTabBar(
        current: RiderTab.home,
        onSelect: (tab) {
          switch (tab) {
            case RiderTab.home:
              break;
            case RiderTab.schedule:
              _comingSoon('The schedule', 'R3');
            case RiderTab.account:
              _comingSoon('Your account', 'R5');
          }
        },
      ),
    );
  }

  Widget _whereTo(DateTime now, RiderProfile profile) {
    final name = profile.name;
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(color: AppColors.ink, borderRadius: BorderRadius.circular(20)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            name == null ? greetingFor(now) : '${greetingFor(now)}, $name',
            key: const Key('home.greeting'),
            style: AppText.body(13, color: AppColors.darkMuted),
          ),
          const SizedBox(height: 12),
          Semantics(
            button: true,
            label: 'Where to? Book a ride now',
            excludeSemantics: true,
            child: Material(
              color: Colors.white,
              borderRadius: BorderRadius.circular(14),
              child: InkWell(
                key: const Key('home.whereTo'),
                borderRadius: BorderRadius.circular(14),
                onTap: () => _comingSoon('Booking a one-time ride', 'R6'),
                child: SizedBox(
                  height: 52,
                  child: Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 14),
                    child: Row(
                      children: [
                        const AppIcon(AppIcons.search, size: 20),
                        const SizedBox(width: 10),
                        Expanded(child: Text('Where to?', style: AppText.display(20, letterSpacing: 0))),
                        Container(
                          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                          decoration: BoxDecoration(color: AppColors.chip, borderRadius: BorderRadius.circular(999)),
                          child: Text('Now', style: AppText.body(12, weight: FontWeight.w600)),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              for (final (i, (label, eta)) in const [('Bike', '2 min'), ('Auto', '4 min'), ('Cab', '5 min')].indexed) ...[
                if (i > 0) const SizedBox(width: 8),
                Expanded(
                  child: Material(
                    color: AppColors.darkPanel,
                    borderRadius: BorderRadius.circular(12),
                    child: InkWell(
                      key: Key('home.quick.$label'),
                      borderRadius: BorderRadius.circular(12),
                      onTap: () => _comingSoon('Booking a one-time ride', 'R6'),
                      child: SizedBox(
                        height: 56,
                        child: Column(
                          mainAxisAlignment: MainAxisAlignment.center,
                          children: [
                            Text(label, style: AppText.body(14, weight: FontWeight.w600, color: Colors.white)),
                            const SizedBox(height: 2),
                            Text(eta, style: AppText.body(11, color: AppColors.darkMuted)),
                          ],
                        ),
                      ),
                    ),
                  ),
                ),
              ],
            ],
          ),
        ],
      ),
    );
  }

  List<Widget> _emptyState(HomeData data) {
    return [
      CardBox(
        radius: 20,
        padding: const EdgeInsets.all(18),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Container(
              width: 44,
              height: 44,
              decoration: BoxDecoration(color: AppColors.greenTint, borderRadius: BorderRadius.circular(12)),
              alignment: Alignment.center,
              child: const AppIcon(AppIcons.repeat, color: AppColors.green),
            ),
            const SizedBox(height: 12),
            Semantics(header: true, child: Text('No regular rides yet', style: AppText.display(22, letterSpacing: 0))),
            const SizedBox(height: 12),
            Text(
              'Going somewhere every weekday, or every Saturday? Set it up once and get the same driver '
              'at the same time, at a fixed fare per ride.',
              style: AppText.body(14, color: AppColors.muted, height: 1.45),
            ),
            const SizedBox(height: 12),
            PrimaryButton(
              key: const Key('home.setup'),
              label: 'Set up a daily or weekly ride',
              height: 48,
              onPressed: _startSetup,
            ),
          ],
        ),
      ),
      if (data.recentRides.isNotEmpty) ...[
        const SizedBox(height: 14),
        CardBox(
          padding: EdgeInsets.zero,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 14, 16, 4),
                child: Text('Recent rides', style: AppText.body(13, weight: FontWeight.w600, color: AppColors.muted)),
              ),
              for (final r in data.recentRides)
                Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(r.drop.display, style: AppText.body(14, weight: FontWeight.w600)),
                      Text('${r.rideType.label} · ${formatDateCompact(r.date)}', style: AppText.body(12, color: AppColors.muted)),
                    ],
                  ),
                ),
            ],
          ),
        ),
      ],
    ];
  }

  List<Widget> _commuteState(Commute c, DateTime now) {
    final r = c.request;
    final today = dateOnly(now);
    final next = r.startDate.isAfter(today) ? r.startDate : firstRideDate(today.subtract(const Duration(days: 1)), r.weekdays);
    final isToday = sameDay(next, today);
    final (hm, ampm) = splitTime(r.pickupTime);
    final statusText = c.status == CommuteStatus.awaitingDriver ? 'Waiting for ${c.driver.firstName}' : 'Driver confirmed';
    return [
      Padding(
        padding: const EdgeInsets.fromLTRB(4, 4, 4, 0),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.baseline,
          textBaseline: TextBaseline.alphabetic,
          children: [
            Expanded(child: Text('Your commute', style: AppText.display(22, letterSpacing: 0))),
            LinkButton(
              key: const Key('home.addRegular'),
              label: '+ Add a regular ride',
              size: 13,
              height: 32,
              onPressed: _startSetup,
            ),
          ],
        ),
      ),
      const SizedBox(height: 14),
      Container(
        key: const Key('home.nextRide'),
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: AppColors.surface,
          borderRadius: BorderRadius.circular(20),
          boxShadow: const [BoxShadow(color: Color(0x0F12161A), blurRadius: 2, offset: Offset(0, 1))],
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    isToday ? 'Today · ${formatDateCompact(next)}' : 'First ride · ${formatDateCompact(next)}',
                    key: const Key('home.nextDate'),
                    style: AppText.body(13, weight: FontWeight.w600, color: AppColors.muted),
                  ),
                ),
                StatusChip(statusText),
              ],
            ),
            const SizedBox(height: 12),
            Row(
              crossAxisAlignment: CrossAxisAlignment.baseline,
              textBaseline: TextBaseline.alphabetic,
              children: [
                Text(hm, key: const Key('home.nextTime'), style: AppText.display(36, letterSpacing: -1)),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    '$ampm · ${r.pickup.title} → ${r.drop.title}',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: AppText.body(15, weight: FontWeight.w600),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Row(
              children: [
                Avatar(c.driver.initials, size: 36),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('${c.driver.name} · ${r.rideType.label}', style: AppText.body(14, weight: FontWeight.w600)),
                      const SizedBox(height: 2),
                      Text('${c.driver.car} · ${c.driver.plate}', style: AppText.body(13, color: AppColors.muted)),
                    ],
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  child: PrimaryButton(
                    key: const Key('home.track'),
                    label: 'Track ride',
                    height: 44,
                    onPressed: () => _comingSoon('Live tracking', 'R2'),
                  ),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: OutlineButtonBox(
                    key: const Key('home.skip'),
                    label: isToday ? 'Skip today' : 'Skip a day',
                    onPressed: () => _comingSoon('Skipping a ride', 'R3'),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
      const SizedBox(height: 14),
      _weekStrip(r, next, today),
    ];
  }

  Widget _weekStrip(CommuteRequest r, DateTime next, DateTime today) {
    final monday = weekStart(next);
    final showWeekend = r.weekdays.any((d) => d > 5);
    final count = showWeekend ? 7 : 5;
    final inThisWeek = sameDay(weekStart(today), monday);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 4),
          child: Row(
            children: [
              Expanded(
                child: Text(
                  inThisWeek ? 'This week' : 'Your first week',
                  style: AppText.body(15, weight: FontWeight.w600),
                ),
              ),
              LinkButton(
                key: const Key('home.fullSchedule'),
                label: 'Full schedule',
                size: 13,
                height: 32,
                onPressed: () => _comingSoon('The schedule', 'R3'),
              ),
            ],
          ),
        ),
        const SizedBox(height: 10),
        Row(
          children: [
            for (var i = 0; i < count; i++) ...[
              if (i > 0) const SizedBox(width: 8),
              Expanded(child: _dayCell(monday.add(Duration(days: i)), r, today)),
            ],
          ],
        ),
      ],
    );
  }

  Widget _dayCell(DateTime day, CommuteRequest r, DateTime today) {
    final isToday = sameDay(day, today);
    final booked = r.weekdays.contains(day.weekday) && !day.isBefore(r.startDate);
    final legs = r.returnTrip ? 2 : 1;
    final dotColor = isToday ? AppColors.yellow : AppColors.outline;
    return Container(
      key: Key('home.day.${day.weekday}'),
      padding: const EdgeInsets.symmetric(vertical: 8),
      decoration: BoxDecoration(
        color: isToday ? AppColors.ink : AppColors.surface,
        borderRadius: BorderRadius.circular(12),
      ),
      child: Column(
        children: [
          Text(weekdayShort(day.weekday), style: AppText.body(12, color: isToday ? AppColors.darkMuted : AppColors.muted)),
          const SizedBox(height: 4),
          Text('${day.day}', style: AppText.body(16, weight: FontWeight.w600, color: isToday ? Colors.white : AppColors.ink)),
          const SizedBox(height: 4),
          SizedBox(
            height: 8,
            child: Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                if (booked)
                  for (var l = 0; l < legs; l++) ...[
                    if (l > 0) const SizedBox(width: 4),
                    Container(
                      width: 8,
                      height: 8,
                      decoration: BoxDecoration(
                        shape: BoxShape.circle,
                        border: Border.all(color: dotColor, width: 1.5),
                      ),
                    ),
                  ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}
