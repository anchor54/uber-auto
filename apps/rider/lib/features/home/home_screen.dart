import 'dart:async';

import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../core/push.dart';
import '../../core/schedule.dart';
import '../../ui/icons.dart';
import '../../ui/tab_bar.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import '../commute_setup/review_screen.dart';
import '../commute_setup/setup_controller.dart';
import '../commute_setup/setup_screen.dart';
import '../ride/ride_screen.dart';

/// Home, with a card for every series that isn't ended:
/// * R1.5 `finding_driver`: "Finding your driver", with Cancel request.
/// * R1.6 `active`: the next pickup and each weekday's driver.
/// * R1.7 `no_driver_found`: "No driver yet": Try again, Change times or
///   ride type, Cancel request.
/// * R1.6b no series (or only ended ones): "Set up a daily or weekly ride".
///
/// The series are read again whenever Home opens, comes back to the
/// foreground or a result push arrives, so a missed push never leaves the
/// rider on a stale card.
class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

/// Rides whose pay-and-rate screen already opened by itself this session,
/// per backend, so Done isn't undone by the next Home load.
final _openedUnsettled = Expando<Set<String>>();

class _HomeScreenState extends State<HomeScreen> with WidgetsBindingObserver {
  /// While a driver is being found, Home checks back this often.
  static const findingRefresh = Duration(seconds: 5);

  /// Close to a pickup, or with a ride under way: so "Track ride" appears
  /// soon after the driver departs.
  static const rideRefresh = Duration(seconds: 15);

  Future<HomeData>? _data;
  Timer? _poll;

  /// The rides of active series for the week (flow R2).
  List<Ride> _rides = const [];

  /// Finished rides the rider hasn't marked or rated.
  List<Ride> _unsettled = const [];
  bool _pushRegistered = false;
  StreamSubscription<PushEvent>? _pushEvents;

  /// The ride whose screen Home has open, so a second tap doesn't stack it.
  String? _openRideId;

  /// Series with a call under way (cancel, retry), so buttons can't be tapped twice.
  final Set<String> _busy = {};

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _pushEvents ??= AppScope.of(context).push.events.listen(_onPush);
    if (_data == null) _load();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _poll?.cancel();
    _pushEvents?.cancel();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed && _openRideId == null) _reload();
  }

  /// A series result (driver confirmed, no driver yet) reads Home again. A
  /// tapped ride notification opens its ride; one that arrives while the app
  /// is open makes Home ask for the latest.
  void _onPush(PushEvent e) {
    if (!mounted) return;
    final rideId = e.rideId;
    if (e.opened && rideId != null && rideId != _openRideId) {
      _openRide(rideId);
    } else if (_openRideId == null) {
      _reload();
    }
  }

  void _load() {
    final scope = AppScope.of(context);
    final api = scope.api;
    final next = api.home().then((h) async {
      final active = h.series.any((s) => s.status == SeriesStatus.active);
      // The daily ride (R2) is reworked later: if its calls fail, Home still
      // shows the series.
      try {
        _rides = active ? await api.upcomingRides() : const [];
        _unsettled = active ? await api.unsettledRides() : const [];
      } on ApiException {
        _rides = const [];
        _unsettled = const [];
      }
      return h;
    });
    _data = next;
    next.then((d) {
      if (!mounted) return;
      _poll?.cancel();
      final now = scope.clock.now();
      final ride = _nextRide();
      if (d.series.any((s) => s.status == SeriesStatus.findingDriver)) {
        _poll = Timer(findingRefresh, _reload);
      } else if (ride != null && (ride.state.isLive || ride.pickupAt.difference(now) < const Duration(minutes: 90))) {
        _poll = Timer(rideRefresh, _reload);
      }
      _afterLoad(d);
    }, onError: (Object _) {});
  }

  /// The next ride that hasn't finished.
  Ride? _nextRide() => _rides.where((r) => r.state == RideState.scheduled || r.state.isLive).firstOrNull;

  Future<void> _afterLoad(HomeData d) async {
    final scope = AppScope.of(context);
    if (!_pushRegistered && d.series.isNotEmpty) {
      _pushRegistered = true;
      final token = await scope.push.token();
      if (token != null) {
        try {
          await scope.api.registerDevice(token, scope.push.platform);
        } on ApiException {
          _pushRegistered = false; // Try again on the next load.
        }
      }
      // The app was opened from a notification: go to its ride. A series
      // result needs nothing more: Home has just read the series.
      final launch = await scope.push.takeLaunchEvent();
      final rideId = launch?.rideId;
      if (rideId != null && mounted) {
        _openRide(rideId);
        return;
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
    _openRideId = id;
    await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => RideScreen(rideId: id)));
    if (_openRideId == id) _openRideId = null;
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

  /// A message from the last flow floats over the next screens and can
  /// cover their buttons: clear it when leaving Home.
  void _clearMessages() => ScaffoldMessenger.of(context).hideCurrentSnackBar();

  Future<void> _push(Widget screen) async {
    _poll?.cancel();
    _clearMessages();
    await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => screen));
    if (mounted) _reload();
  }

  Future<void> _startSetup() => _push(const NewCommuteScreen());

  DateTime get _today => dateOnly(AppScope.of(context).clock.now());

  void _viewRequest(Series s) => _push(ReviewRequestScreen(setup: CommuteSetup.fromSeries(s, today: _today), series: s));

  /// R1.7: R1.2 filled in, then R1.3 and R1.4, whose button retries.
  void _changeRequest(Series s) => _push(NewCommuteScreen(setup: CommuteSetup.fromSeries(s, today: _today)));

  Future<void> _retry(Series s) async {
    if (!_busy.add(s.id)) return;
    setState(() {});
    try {
      await AppScope.of(context).api.retrySeries(s.id);
      if (mounted) showInfo(context, requestSentMessage);
    } on ApiException catch (e) {
      if (mounted) showInfo(context, e.message);
    } finally {
      _busy.remove(s.id);
      _reload();
    }
  }

  Future<void> _cancel(Series s) async {
    final yes = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Cancel this request?'),
        content: const Text("Drivers will stop seeing it. You haven't paid anything."),
        actions: [
          TextButton(
            key: const Key('cancelRequest.keep'),
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Keep request'),
          ),
          TextButton(
            key: const Key('cancelRequest.confirm'),
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Cancel request'),
          ),
        ],
      ),
    );
    if (yes != true || !mounted || !_busy.add(s.id)) return;
    setState(() {});
    final api = AppScope.of(context).api;
    try {
      await api.cancelRequest(s.id);
      if (mounted) showInfo(context, 'Request cancelled.');
    } on ApiException catch (e) {
      if (!mounted) return;
      if (e.code == 'already_assigned') {
        await _justAssigned(e.state is Series ? e.state as Series : s);
      } else {
        showInfo(context, e.message);
      }
    } finally {
      _busy.remove(s.id);
      _reload();
    }
  }

  /// The pick committed before the cancel: the rider keeps the driver, or ends the series.
  Future<void> _justAssigned(Series s) async {
    final driver = s.slots.map((x) => x.driver).whereType<Driver>().firstOrNull;
    final name = driver?.firstName ?? 'A driver';
    final end = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        key: const Key('home.justAssigned'),
        content: Text('$name was just assigned. Keep him, or end the series?'),
        actions: [
          TextButton(
            key: const Key('justAssigned.end'),
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('End the series'),
          ),
          TextButton(
            key: const Key('justAssigned.keep'),
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Keep him'),
          ),
        ],
      ),
    );
    if (end == true && mounted) _comingSoon('Ending a series', 'R3');
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
            final series = data.series;
            return RefreshIndicator(
              color: AppColors.green,
              onRefresh: _refresh,
              child: ListView(
                key: Key(series.isEmpty ? 'home.empty' : 'home.commute'),
                physics: const AlwaysScrollableScrollPhysics(),
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 16),
                children: [
                  _whereTo(now, data.profile),
                  const SizedBox(height: 14),
                  if (series.isEmpty) _emptyState() else ..._seriesState(series, now),
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
              _reload();
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
    final name = profile.firstName;
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
        ],
      ),
    );
  }

  Widget _emptyState() {
    return CardBox(
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
    );
  }

  List<Widget> _seriesState(List<Series> series, DateTime now) {
    final today = dateOnly(now);
    final active = series.where((s) => s.status == SeriesStatus.active || s.status == SeriesStatus.paused).firstOrNull;
    final finding = series.where((s) => s.status == SeriesStatus.findingDriver).firstOrNull;
    final noShow = _rides.where((x) => x.state == RideState.noShow && sameDay(x.date, today)).firstOrNull;
    return [
      if (noShow != null) ...[
        CardBox(
          key: const Key('home.noShow'),
          color: AppColors.orangeTint,
          child: Text(
            'Marked as a no-show for ${formatTime(noShow.pickupAt.hour * 60 + noShow.pickupAt.minute)}. '
            '${noShow.direction == RideDirection.outbound && (active?.returnTrip ?? false) ? 'Your return ride is still booked.' : 'Your next rides are still booked.'}',
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
      for (final s in series) ...[
        switch (s.status) {
          SeriesStatus.findingDriver => _requestCard(s, now, noDriver: false),
          SeriesStatus.noDriverFound => _requestCard(s, now, noDriver: true),
          _ => _activeCard(s, now),
        },
        const SizedBox(height: 14),
      ],
      if (active != null)
        _weekStrip(active, today)
      else if (finding != null)
        _waitingWeek(finding),
    ];
  }

  // ---------------------------------------------------------------- R1.5 / R1.7

  /// "8:40 AM (Fri 9:30 AM) · back at 6:30 PM · Cab · Sedan"
  String _timesLine(Series s) {
    final out = s.outbound;
    if (out.isEmpty) return s.rideType.label;
    final base = out.first.pickupTime;
    final exceptions = [for (final x in out) if (x.pickupTime != base) '${weekdayShort(x.weekday)} ${formatTime(x.pickupTime)}'];
    final back = s.returns;
    return [
      exceptions.isEmpty ? formatTime(base) : '${formatTime(base)} (${exceptions.join(', ')})',
      if (back.isNotEmpty) 'back at ${formatTime(back.first.pickupTime)}',
      s.rideType.label,
    ].join(' · ');
  }

  String _requestedLabel(Series s, DateTime now) {
    final at = s.requestedAt;
    final time = formatTime(at.hour * 60 + at.minute);
    return sameDay(at, now) ? 'Requested today, $time' : 'Requested ${formatDateCompact(at)}, $time';
  }

  Widget _requestCard(Series s, DateTime now, {required bool noDriver}) {
    final busy = _busy.contains(s.id);
    final route = s.returnTrip ? '${s.pickup.title} ⇄ ${s.drop.title}' : '${s.pickup.title} → ${s.drop.title}';
    return Container(
      key: Key(noDriver ? 'home.noDriver' : 'home.finding'),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.surface,
        borderRadius: BorderRadius.circular(18),
        boxShadow: const [BoxShadow(color: Color(0x0F12161A), blurRadius: 2, offset: Offset(0, 1))],
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(_requestedLabel(s, now), style: AppText.body(13, weight: FontWeight.w600, color: AppColors.muted)),
              ),
              StatusChip(
                noDriver ? 'No driver yet' : 'Finding your driver',
                key: Key(noDriver ? 'home.noDriverChip' : 'home.findingChip'),
                background: noDriver ? AppColors.orangeTint : AppColors.chip,
                foreground: noDriver ? AppColors.orangeText : AppColors.ink,
              ),
            ],
          ),
          const SizedBox(height: 12),
          Text('${daysLabel(s.weekdays)} · $route', style: AppText.body(16, weight: FontWeight.w600)),
          const SizedBox(height: 4),
          Text(_timesLine(s), key: const Key('home.requestTimes'), style: AppText.body(13, color: AppColors.muted)),
          const SizedBox(height: 12),
          Text(
            noDriver
                ? 'No ${s.rideType.noun} driver could take every one of these rides yet. '
                    'Try again, or change the times or ride type to reach more drivers.'
                : "We've sent your request to ${s.rideType.noun} drivers. We'll notify you as soon as your "
                    'driver is confirmed, usually within 15 minutes.',
            style: AppText.body(13, color: AppColors.ink, height: 1.45),
          ),
          const SizedBox(height: 14),
          if (noDriver) ...[
            PrimaryButton(
              key: const Key('noDriver.retry'),
              label: 'Try again',
              height: 48,
              loading: busy,
              onPressed: () => _retry(s),
            ),
            const SizedBox(height: 8),
            OutlineButtonBox(
              key: const Key('noDriver.change'),
              label: 'Change times or ride type',
              onPressed: busy ? null : () => _changeRequest(s),
            ),
            const SizedBox(height: 8),
            OutlineButtonBox(
              key: const Key('home.cancelRequest'),
              label: 'Cancel request',
              onPressed: busy ? null : () => _cancel(s),
            ),
          ] else
            Row(
              children: [
                Expanded(
                  child: OutlineButtonBox(
                    key: const Key('home.viewRequest'),
                    label: 'View request',
                    onPressed: () => _viewRequest(s),
                  ),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: OutlineButtonBox(
                    key: const Key('home.cancelRequest'),
                    label: 'Cancel request',
                    onPressed: busy ? null : () => _cancel(s),
                  ),
                ),
              ],
            ),
        ],
      ),
    );
  }

  Widget _waitingWeek(Series s) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 4),
          child: Text('This week', style: AppText.body(15, weight: FontWeight.w600)),
        ),
        const SizedBox(height: 8),
        CardBox(
          key: const Key('home.waitingWeek'),
          child: Text(
            'Your rides start ${formatDateCompact(s.startDate)}, once your driver is confirmed.',
            style: AppText.body(14, color: AppColors.muted, height: 1.4),
          ),
        ),
      ],
    );
  }

  // ---------------------------------------------------------------- R1.6

  /// The next pickup of [s]: its next ride (R2) if Home has it, else from its slots.
  (DateTime, int, RideDirection)? _nextPickup(Series s, DateTime now) {
    final ride = _nextRide();
    if (ride != null) return (dateOnly(ride.date), ride.pickupAt.hour * 60 + ride.pickupAt.minute, ride.direction);
    final today = dateOnly(now);
    var day = s.startDate.isAfter(today) ? s.startDate : today;
    for (var i = 0; i < 15; i++) {
      final legs = [for (final x in s.slots) if (x.weekday == day.weekday && x.active) x]
        ..sort((a, b) => a.pickupTime.compareTo(b.pickupTime));
      for (final x in legs) {
        final at = DateTime(day.year, day.month, day.day, x.pickupTime ~/ 60, x.pickupTime % 60);
        if (at.isAfter(now)) return (day, x.pickupTime, x.direction);
      }
      day = DateTime(day.year, day.month, day.day + 1);
    }
    return null;
  }

  Widget _activeCard(Series s, DateTime now) {
    final today = dateOnly(now);
    final ride = _nextRide();
    final next = _nextPickup(s, now);
    final paused = s.status == SeriesStatus.paused;
    final liveRide = ride != null && ride.state.isLive ? ride : null;
    final live = liveRide != null;
    final first = ride?.driver.firstName ?? '';
    final statusText = paused
        ? 'Paused'
        : switch (ride?.state) {
            RideState.onTheWay => '$first is on the way',
            RideState.arrived => '$first has arrived',
            RideState.inRide => 'In the ride',
            _ => 'Driver confirmed',
          };
    final outbound = next == null || next.$3 == RideDirection.outbound;
    final from = outbound ? s.pickup : s.drop;
    final to = outbound ? s.drop : s.pickup;
    final (hm, ampm) = next == null ? ('–', '') : splitTime(next.$2);
    final isToday = next != null && sameDay(next.$1, today);
    // One line per driver, with the weekdays they drive.
    final byDriver = <String, (Driver, Set<int>)>{};
    for (final x in s.slots) {
      final d = x.driver;
      if (d == null) continue;
      byDriver.putIfAbsent(d.id, () => (d, <int>{})).$2.add(x.weekday);
    }
    return Container(
      key: const Key('home.nextRide'),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppColors.surface,
        borderRadius: BorderRadius.circular(18),
        boxShadow: const [BoxShadow(color: Color(0x0F12161A), blurRadius: 2, offset: Offset(0, 1))],
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  next == null ? 'No rides in the next two weeks' : '${relativeDay(next.$1, today)} · ${formatDayMonth(next.$1)}',
                  key: const Key('home.nextDate'),
                  style: AppText.body(13, weight: FontWeight.w600, color: AppColors.muted),
                ),
              ),
              StatusChip(
                statusText,
                key: Key(live ? 'home.live' : (paused ? 'home.paused' : 'home.confirmed')),
                background: live ? AppColors.yellow : (paused ? AppColors.chip : AppColors.greenTint),
                foreground: live || paused ? AppColors.ink : AppColors.green,
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
          for (final (driver, days) in byDriver.values) ...[
            const SizedBox(height: 12),
            Row(
              children: [
                Avatar(driver.initials, size: 36),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('${driver.name} · ${s.rideType.label}', style: AppText.body(14, weight: FontWeight.w600)),
                      const SizedBox(height: 2),
                      Text(
                        '${driver.car} · ${driver.plate} · your driver ${daysLabel(days)}',
                        style: AppText.body(13, color: AppColors.muted),
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ],
          const SizedBox(height: 10),
          // Each weekday's driver, one line per day.
          for (final d in (s.weekdays.toList()..sort()))
            _slotLine(s, d),
          const SizedBox(height: 12),
          // Track ride appears once the driver is on the way (R2).
          if (liveRide != null)
            PrimaryButton(
              key: const Key('home.track'),
              label: 'Track ride',
              height: 44,
              onPressed: () => _openRide(liveRide.id),
            )
          else
            OutlineButtonBox(
              key: const Key('home.skip'),
              label: isToday ? 'Skip today' : 'Skip a day',
              onPressed: () => _comingSoon('Skipping a ride', 'R3'),
            ),
        ],
      ),
    );
  }

  /// "Mon: Ravi · auto KA 05 AB 1234" (or "Mon: no driver yet").
  Widget _slotLine(Series s, int day) {
    final slot = s.slotFor(day, RideDirection.outbound);
    final d = slot?.driver;
    final text = d == null ? 'no driver yet' : '${d.firstName} · ${s.rideType.noun} ${d.plate}';
    return Padding(
      key: Key('home.slot.${dayCode(day)}'),
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Text.rich(
        TextSpan(
          style: AppText.body(12, color: AppColors.muted),
          children: [
            TextSpan(text: '${weekdayShort(day)}: ', style: AppText.body(12, weight: FontWeight.w600, color: AppColors.ink)),
            TextSpan(text: '$text · ${formatTime(slot?.pickupTime ?? 0)}'),
          ],
        ),
      ),
    );
  }

  Widget _weekStrip(Series s, DateTime today) {
    final monday = weekStart(today);
    final showWeekend = s.weekdays.any((d) => d > 5);
    final count = showWeekend ? 7 : 5;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 4),
          child: Row(
            children: [
              Expanded(child: Text('This week', style: AppText.body(15, weight: FontWeight.w600))),
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
              Expanded(child: _dayCell(monday.add(Duration(days: i)), s, today)),
            ],
          ],
        ),
      ],
    );
  }

  Widget _dayCell(DateTime day, Series s, DateTime today) {
    final isToday = sameDay(day, today);
    final legs = s.slots.where((x) => x.weekday == day.weekday && x.active).length;
    final booked = !day.isBefore(s.startDate) && !day.isBefore(today);
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
                      decoration: BoxDecoration(shape: BoxShape.circle, border: Border.all(color: dotColor, width: 1.5)),
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
