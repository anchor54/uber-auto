import 'dart:async';

import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../core/schedule.dart';
import '../../ui/icons.dart';
import '../../ui/tab_bar.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import '../commute_setup/match_screen.dart';
import '../commute_setup/setup_controller.dart';
import '../commute_setup/setup_screen.dart';
import '../ride/ride_screen.dart';

/// R1.6 Home (with a commute) and R1.6b Home (no regular ride yet).
class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

/// Rides whose pay-and-rate screen already opened by itself this session,
/// per backend, so Done isn't undone by the next Home load.
final _openedUnsettled = Expando<Set<String>>();

class _HomeScreenState extends State<HomeScreen> {
  /// While the driver hasn't answered, Home checks back this often.
  static const awaitingRefresh = Duration(seconds: 10);

  /// Close to a pickup, or with a ride under way: so "Track ride" appears
  /// soon after the driver departs.
  static const rideRefresh = Duration(seconds: 15);

  Future<HomeData>? _data;
  Timer? _poll;

  /// The commute's rides for the week (flow R2); empty until the driver accepts.
  List<Ride> _rides = const [];

  /// Finished rides the rider hasn't marked or rated.
  List<Ride> _unsettled = const [];
  bool _pushRegistered = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_data == null) _load();
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  void _load() {
    final scope = AppScope.of(context);
    final api = scope.api;
    final next = api.home().then((h) async {
      final active = h.commute?.status == CommuteStatus.active;
      _rides = active ? await api.upcomingRides() : const [];
      _unsettled = active ? await api.unsettledRides() : const [];
      return h;
    });
    _data = next;
    next.then((d) {
      if (!mounted) return;
      _poll?.cancel();
      final now = scope.clock.now();
      final ride = _nextRide();
      // Keep checking until the driver accepts or declines (flow D2), and
      // around a pickup so the ride can be tracked once it starts (R2).
      if (d.commute?.status == CommuteStatus.awaitingDriver) {
        _poll = Timer(awaitingRefresh, _reload);
      } else if (ride != null && (ride.state.isLive || ride.pickupAt.difference(now) < const Duration(minutes: 90))) {
        _poll = Timer(rideRefresh, _reload);
      }
      _afterLoad(d);
    }, onError: (Object _) {});
  }

  /// The next ride that hasn't finished: what Home's card shows.
  Ride? _nextRide() => _rides.where((r) => r.state == RideState.scheduled || r.state.isLive).firstOrNull;

  Future<void> _afterLoad(HomeData d) async {
    final scope = AppScope.of(context);
    if (!_pushRegistered && d.commute != null) {
      _pushRegistered = true;
      final token = await scope.push.token();
      if (token != null) {
        try {
          await scope.api.registerDevice(token, scope.push.platform);
        } on ApiException {
          _pushRegistered = false; // Try again on the next load.
        }
      }
    }
    // A finished ride the rider hasn't marked or rated opens R2.3, once.
    if (!mounted) return;
    final seen = _openedUnsettled[scope.api] ??= <String>{};
    final ride = _unsettled.where((r) => !seen.contains(r.id)).firstOrNull;
    if (ride == null) return;
    seen.add(ride.id);
    _openRide(ride.id);
  }

  Future<void> _openRide(String id) async {
    _poll?.cancel();
    _clearMessages();
    await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => RideScreen(rideId: id)));
    if (mounted) _reload();
  }

  void _reload() {
    if (!mounted) return;
    setState(_load);
  }

  Future<void> _refresh() async {
    _reload();
    try {
      await _data;
    } catch (_) {
      // The error state shows on screen.
    }
  }

  /// After a decline: the same route and times, a fresh search for a driver.
  Future<void> _findAnotherDriver(Commute c) async {
    _clearMessages();
    final setup = CommuteSetup.fromRequest(c.request, today: dateOnly(AppScope.of(context).clock.now()));
    await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => MatchScreen(setup: setup)));
    if (mounted) _reload();
  }

  /// A message from the last flow ("Commute confirmed …") floats over the
  /// next screens and can cover their buttons: clear it when leaving Home.
  void _clearMessages() => ScaffoldMessenger.of(context).hideCurrentSnackBar();

  Future<void> _startSetup() async {
    _clearMessages();
    await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => const SetupScreen()));
    if (mounted) _reload();
  }

  void _comingSoon(String what, String flow) => showInfo(context, '$what comes with flow $flow.');

  Widget _loadError(Object? error) {
    final message = error is ApiException ? error.message : 'Something went wrong loading your rides.';
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          key: const Key('home.error'),
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(message, textAlign: TextAlign.center, style: AppText.body(15, color: AppColors.muted, height: 1.45)),
            const SizedBox(height: 16),
            PrimaryButton(key: const Key('home.retry'), label: 'Try again', onPressed: _reload),
          ],
        ),
      ),
    );
  }

  /// The driver declined, or didn't answer in time (flow D2).
  Widget _declinedCard(Commute c) {
    final first = c.driver.firstName;
    final r = c.request;
    final noAnswer = c.declineReason == DeclineReason.noAnswer;
    return CardBox(
      key: const Key('home.declined'),
      radius: 20,
      padding: const EdgeInsets.all(18),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Semantics(
            header: true,
            child: Text(
              noAnswer ? "$first didn't reply in time" : "$first can't take this commute",
              style: AppText.display(22, letterSpacing: 0),
            ),
          ),
          const SizedBox(height: 10),
          Text(
            'Your route and times are saved: ${r.pickup.title} → ${r.drop.title}, '
            '${daysLabel(r.weekdays)} at ${formatTime(r.pickupTime)}. Pick another driver to get started. '
            "You haven't paid anything.",
            style: AppText.body(14, color: AppColors.muted, height: 1.45),
          ),
          const SizedBox(height: 14),
          PrimaryButton(
            key: const Key('home.rematch'),
            label: 'Find another driver',
            height: 48,
            onPressed: () => _findAnotherDriver(c),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final now = AppScope.of(context).clock.now();
    return Scaffold(
      body: SafeArea(
        bottom: false,
        child: FutureBuilder<HomeData>(
          future: _data,
          builder: (context, snap) {
            if (snap.hasError) return _loadError(snap.error);
            if (!snap.hasData) {
              return const Center(child: CircularProgressIndicator(color: AppColors.green));
            }
            final data = snap.data!;
            final commute = data.commute;
            return RefreshIndicator(
              color: AppColors.green,
              onRefresh: _refresh,
              child: ListView(
                key: Key(commute == null ? 'home.empty' : 'home.commute'),
                physics: const AlwaysScrollableScrollPhysics(),
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 16),
                children: [
                  _whereTo(now, data.profile),
                  const SizedBox(height: 14),
                  if (commute == null)
                    ..._emptyState(data)
                  else if (commute.status == CommuteStatus.declined)
                    _declinedCard(commute)
                  else
                    ..._commuteState(commute, now),
                ],
              ),
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
              for (final (i, (label, eta))
                  in const [('Bike', '2 min'), ('Auto', '4 min'), ('Cab', '5 min')].indexed) ...[
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
                      Text('${r.rideType.label} · ${formatDateCompact(r.date)}',
                          style: AppText.body(12, color: AppColors.muted)),
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
    // R2: the next pickup comes from the next ride record, not the commute.
    final ride = _nextRide();
    final next = ride?.date ??
        (r.startDate.isAfter(today) ? r.startDate : firstRideDate(today.subtract(const Duration(days: 1)), r.weekdays));
    final isToday = sameDay(next, today);
    final pickupMinute = ride == null ? r.pickupTime : ride.pickupAt.hour * 60 + ride.pickupAt.minute;
    final (hm, ampm) = splitTime(pickupMinute);
    final from = ride?.pickup ?? r.pickup;
    final to = ride?.drop ?? r.drop;
    final awaiting = c.status == CommuteStatus.awaitingDriver;
    final live = ride != null && ride.state.isLive;
    final first = c.driver.firstName;
    final statusText = awaiting
        ? 'Waiting for $first'
        : switch (ride?.state) {
            RideState.onTheWay => '$first is on the way',
            RideState.arrived => '$first has arrived',
            RideState.inRide => 'In the ride',
            _ => 'Driver confirmed',
          };
    final respondBy = c.respondBy;
    final noShow = _rides.where((x) => x.state == RideState.noShow && sameDay(x.date, today)).firstOrNull;
    final dateLabel = isToday
        ? 'Today · ${formatDateCompact(next)}'
        : (ride != null && !ride.date.isAfter(r.startDate) || ride == null)
            ? 'First ride · ${formatDateCompact(next)}'
            : 'Next ride · ${formatDateCompact(next)}';
    return [
      if (noShow != null) ...[
        CardBox(
          key: const Key('home.noShow'),
          color: AppColors.orangeTint,
          child: Text(
            'Marked as a no-show for ${formatTime(noShow.pickupAt.hour * 60 + noShow.pickupAt.minute)}. '
            '${noShow.direction == RideDirection.outbound && r.returnTrip ? 'Your return ride is still booked.' : 'Your next rides are still booked.'}',
            style: AppText.body(14, color: AppColors.orangeText, weight: FontWeight.w600, height: 1.4),
          ),
        ),
        const SizedBox(height: 14),
      ],
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
                    dateLabel,
                    key: const Key('home.nextDate'),
                    style: AppText.body(13, weight: FontWeight.w600, color: AppColors.muted),
                  ),
                ),
                StatusChip(
                  statusText,
                  key: Key(awaiting ? 'home.awaiting' : (live ? 'home.live' : 'home.confirmed')),
                  background: awaiting ? AppColors.chip : (live ? AppColors.yellow : AppColors.greenTint),
                  foreground: awaiting || live ? AppColors.ink : AppColors.green,
                ),
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
                    '$ampm · ${from.title} → ${to.title}',
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
            if (awaiting) ...[
              const SizedBox(height: 12),
              Text(
                respondBy == null
                    ? "We've asked ${c.driver.firstName} to confirm. We'll tell you as soon as they do."
                    : "We've asked ${c.driver.firstName} to confirm by ${_whenLabel(respondBy, now)}. "
                        "If they can't, we'll help you pick another driver.",
                key: const Key('home.awaitingNote'),
                style: AppText.body(13, color: AppColors.muted, height: 1.45),
              ),
            ] else ...[
              const SizedBox(height: 12),
              // Track ride appears once the driver is on the way (R2).
              if (live)
                PrimaryButton(
                  key: const Key('home.track'),
                  label: 'Track ride',
                  height: 44,
                  onPressed: () => _openRide(ride.id),
                )
              else
                OutlineButtonBox(
                  key: const Key('home.skip'),
                  label: isToday ? 'Skip today' : 'Skip a day',
                  onPressed: () => _comingSoon('Skipping a ride', 'R3'),
                ),
            ],
          ],
        ),
      ),
      const SizedBox(height: 14),
      _weekStrip(r, next, today),
    ];
  }

  /// "9:00 PM" today, "9:00 PM tomorrow", or "Thu 22 Oct, 7:40 AM".
  String _whenLabel(DateTime at, DateTime now) {
    final time = formatTime(at.hour * 60 + at.minute);
    final day = dateOnly(at);
    final today = dateOnly(now);
    if (sameDay(day, today)) return time;
    if (sameDay(day, today.add(const Duration(days: 1)))) return '$time tomorrow';
    return '${formatDateCompact(day)}, $time';
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
          Text(weekdayShort(day.weekday),
              style: AppText.body(12, color: isToday ? AppColors.darkMuted : AppColors.muted)),
          const SizedBox(height: 4),
          Text('${day.day}',
              style: AppText.body(16, weight: FontWeight.w600, color: isToday ? Colors.white : AppColors.ink)),
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
