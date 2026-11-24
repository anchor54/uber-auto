import 'dart:async';

import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../core/schedule.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'request_detail_screen.dart';

/// Win banners the driver has tapped this session, per backend: the banner
/// shows until tapped.
final _seenWins = Expando<Set<String>>();

/// D2.1 Requests: open offers newest first, the win banner, result cards
/// for requests the driver was interested in, and the empty state.
class RequestsScreen extends StatefulWidget {
  const RequestsScreen({super.key, this.profile, this.onOpenWeek, this.showLiveBanner = false});

  final DriverProfile? profile;

  /// D1 "Going live": the green "You're live" banner at the top.
  final bool showLiveBanner;

  /// Opens D2.3 at the week starting [start] (the win banner). Null: push it.
  final void Function(DateTime? start)? onOpenWeek;

  @override
  State<RequestsScreen> createState() => _RequestsScreenState();
}

class _RequestsData {
  const _RequestsData(this.list, this.win);
  final OfferList list;

  /// The won offer whose banner shows, or null.
  final Offer? win;
}

class _RequestsScreenState extends State<RequestsScreen> {
  Future<_RequestsData>? _data;
  _RequestsData? _last;
  Timer? _tick;
  final Set<String> _busy = {};

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_data == null) _load();
  }

  @override
  void dispose() {
    _tick?.cancel();
    super.dispose();
  }

  Set<String> get _seen => _seenWins[AppScope.of(context).api] ??= <String>{};

  void _load() {
    final api = AppScope.of(context).api;
    final seen = _seen;
    final next = api.offers().then((list) async {
      Offer? win;
      for (final r in list.results) {
        if (r.result != 'picked' || seen.contains(r.offerId)) continue;
        try {
          win = await api.offer(r.offerId);
        } on ApiException {
          win = null;
        }
        if (win != null) break;
      }
      return _RequestsData(list, win);
    });
    _data = next;
    next.then((d) {
      if (!mounted) return;
      _last = d;
      _startTicking(d);
    }, onError: (Object _) {});
  }

  /// Countdowns tick every second while a window is open; when one passes,
  /// the list is read again so the result shows.
  void _startTicking(_RequestsData d) {
    _tick?.cancel();
    if (d.list.offers.isEmpty) return;
    _tick = Timer.periodic(const Duration(seconds: 1), (_) {
      if (!mounted) return;
      final now = AppScope.of(context).clock.now();
      final passed = _last?.list.offers.any((o) => o.canRespond && !now.isBefore(o.closesAt)) ?? false;
      if (passed) {
        _tick?.cancel();
        _reload();
      } else {
        setState(() {});
      }
    });
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
      // Shown on screen.
    }
  }

  Future<void> _act(Offer o, Future<void> Function() call) async {
    if (!_busy.add(o.id)) return;
    setState(() {});
    try {
      await call();
    } on ApiException catch (e) {
      if (mounted) showInfo(context, e.message);
    } finally {
      _busy.remove(o.id);
      _reload();
    }
  }

  void _interested(Offer o) => _act(o, () => AppScope.of(context).api.markInterested(o.id));

  void _notForMe(Offer o) => _act(o, () => AppScope.of(context).api.dismissOffer(o.id));

  Future<void> _openDetail(Offer o) async {
    _tick?.cancel();
    await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => RequestDetailScreen(offerId: o.id, offer: o)));
    _reload();
  }

  void _openWin(Offer win) {
    _seen.add(win.id);
    final start = weekStart(win.startDate);
    final open = widget.onOpenWeek;
    if (open != null) {
      open(start);
    } else {
      _reload();
    }
  }

  String _rideTypeNoun(_RequestsData? d) {
    final type = widget.profile?.vehicleType ?? d?.list.offers.firstOrNull?.vehicleType ?? d?.win?.vehicleType;
    return type == null || type.isEmpty ? 'matching' : vehicleNoun(type);
  }

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: FutureBuilder<_RequestsData>(
        future: _data,
        builder: (context, snap) {
          final d = snap.data ?? _last;
          return RefreshIndicator(
            color: AppColors.yellow,
            onRefresh: _refresh,
            child: ListView(
              key: const Key('requests.list'),
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.fromLTRB(16, 20, 16, 24),
              children: [
                Semantics(header: true, child: Text('Requests', style: AppText.display(28))),
                const SizedBox(height: 6),
                Text(
                  "Regular rides open to ${_rideTypeNoun(d)} drivers. Tap I'm interested; when a request closes, "
                  'one interested driver whose week fits is picked at random.',
                  style: AppText.body(14, color: AppColors.muted, height: 1.45),
                ),
                const SizedBox(height: 16),
                if (snap.hasError && d == null)
                  _error(snap.error)
                else if (d == null)
                  const Padding(
                    padding: EdgeInsets.all(32),
                    child: Center(child: CircularProgressIndicator(color: AppColors.yellow)),
                  )
                else
                  ..._content(d),
              ],
            ),
          );
        },
      ),
    );
  }

  Widget _error(Object? error) {
    final message = error is ApiException ? error.message : 'Something went wrong loading requests.';
    return CardBox(
      key: const Key('requests.error'),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(message, style: AppText.body(14, color: AppColors.muted, height: 1.45)),
          const SizedBox(height: 10),
          PrimaryButton(key: const Key('requests.retry'), label: 'Try again', height: 48, onPressed: _reload),
        ],
      ),
    );
  }

  List<Widget> _content(_RequestsData d) {
    final now = AppScope.of(context).clock.now();
    final win = d.win;
    final losses = [for (final r in d.list.results) if (r.result != 'picked') r];
    return [
      if (widget.showLiveBanner) ...[_liveBanner(d), const SizedBox(height: 14)],
      if (win != null) ...[_winBanner(win), const SizedBox(height: 14)],
      for (final o in d.list.offers) ...[
        if (o.kind == OfferKind.ride) _rideCard(o, now) else _seriesCard(o, now),
        const SizedBox(height: 12),
      ],
      for (final r in losses) ...[_resultCard(r), const SizedBox(height: 12)],
      if (d.list.offers.isEmpty)
        CardBox(
          key: const Key('requests.empty'),
          child: Text(
            "No open requests right now. We'll notify you when a new one comes in.",
            style: AppText.body(14, color: AppColors.muted, height: 1.45),
          ),
        ),
    ];
  }

  /// "You're live. New requests for auto drivers show up here."
  Widget _liveBanner(_RequestsData? d) => Container(
        key: const Key('requests.liveBanner'),
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(color: AppColors.green, borderRadius: BorderRadius.circular(16)),
        child: Row(
          children: [
            const Icon(Icons.check_circle, color: AppColors.onYellow),
            const SizedBox(width: 12),
            Expanded(
              child: Text(
                "You're live. New requests for ${_rideTypeNoun(d)} drivers show up here.",
                style: AppText.body(14, weight: FontWeight.w600, color: AppColors.onYellow, height: 1.4),
              ),
            ),
          ],
        ),
      );

  /// "You got it: Priya's Mon, Wed, Fri 8:30 AM rides are yours from Mon 23 Nov."
  Widget _winBanner(Offer o) {
    final out = o.outbound;
    final time = out.isEmpty ? '' : ' ${formatTime(out.first.pickupTime)}';
    return Semantics(
      button: true,
      child: Material(
        color: AppColors.green,
        borderRadius: BorderRadius.circular(16),
        child: InkWell(
          key: const Key('requests.winBanner'),
          borderRadius: BorderRadius.circular(16),
          onTap: () => _openWin(o),
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Row(
              children: [
                const Icon(Icons.celebration_outlined, color: AppColors.onYellow),
                const SizedBox(width: 12),
                Expanded(
                  child: Text(
                    "You got it: ${o.rider.firstName}'s ${daysLabel(o.weekdays)}$time rides are yours from ${formatDate(o.startDate)}.",
                    style: AppText.body(14, weight: FontWeight.w600, color: AppColors.onYellow, height: 1.4),
                  ),
                ),
                const Icon(Icons.chevron_right, color: AppColors.onYellow),
              ],
            ),
          ),
        ),
      ),
    );
  }

  int _secondsLeft(Offer o, DateTime now) {
    final s = o.closesAt.difference(now).inSeconds;
    return s < 0 ? 0 : s;
  }

  Widget _seriesCard(Offer o, DateTime now) {
    final busy = _busy.contains(o.id);
    final open = o.openAt(now);
    final out = o.outbound;
    final base = out.isEmpty ? null : out.first.pickupTime;
    final exceptions = [
      for (final s in out)
        if (s.pickupTime != base) '${weekdayShort(s.weekday)} ${formatTime(s.pickupTime)}',
    ];
    final back = o.returns;
    return CardBox(
      key: Key('requests.offer.${o.id}'),
      radius: 18,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Avatar(o.rider.initials),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    InkWell(
                      key: Key('requests.card.${o.id}'),
                      onTap: () => _openDetail(o),
                      child: ConstrainedBox(
                        constraints: const BoxConstraints(minHeight: 28),
                        child: Text('${o.rider.name} · details', style: AppText.body(15, weight: FontWeight.w600, color: AppColors.yellow)),
                      ),
                    ),
                    Text('Starts ${formatDate(o.startDate)} · ${o.rider.rating} rating', style: AppText.body(13, color: AppColors.muted)),
                    if (o.kind == OfferKind.slot && out.isNotEmpty) ...[
                      const SizedBox(height: 6),
                      Chip2('${weekdayPlural(out.first.weekday)} only', foreground: AppColors.ink),
                    ],
                  ],
                ),
              ),
              const SizedBox(width: 8),
              Chip2(open ? closesIn(_secondsLeft(o, now)) : 'Closed'),
            ],
          ),
          const SizedBox(height: 14),
          _placeLine(Icons.trip_origin, AppColors.green, o.pickup.label),
          const SizedBox(height: 6),
          _placeLine(Icons.place, AppColors.yellow, o.drop.label),
          const SizedBox(height: 14),
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(child: _tile('Days', daysLabel(o.weekdays))),
              const SizedBox(width: 8),
              Expanded(child: _tile('Pickup', base == null ? '—' : formatTime(base), under: exceptions.isEmpty ? null : exceptions.join(', '))),
              const SizedBox(width: 8),
              Expanded(child: _tile('Return', back.isEmpty ? '—' : formatTime(back.first.pickupTime))),
            ],
          ),
          const SizedBox(height: 14),
          Row(
            crossAxisAlignment: CrossAxisAlignment.baseline,
            textBaseline: TextBaseline.alphabetic,
            children: [
              Expanded(child: Text('Fare per ride, paid to you', style: AppText.body(14, color: AppColors.muted))),
              Text('₹${o.farePerRide}', style: AppText.display(22, letterSpacing: 0)),
            ],
          ),
          const SizedBox(height: 14),
          Row(
            children: [
              Expanded(
                child: OutlineButtonBox(
                  key: Key('requests.notForMe.${o.id}'),
                  label: 'Not for me',
                  onPressed: busy ? null : () => _notForMe(o),
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: o.interested
                    ? PrimaryButton(
                        key: Key('requests.interested.${o.id}'),
                        label: 'Interested',
                        icon: Icons.check,
                        height: 48,
                        color: AppColors.greenDark,
                        textColor: AppColors.green,
                        onPressed: () => _openDetail(o),
                      )
                    : PrimaryButton(
                        key: Key('requests.interested.${o.id}'),
                        label: "I'm interested",
                        height: 48,
                        loading: busy,
                        onPressed: open ? () => _interested(o) : null,
                      ),
              ),
            ],
          ),
          if (o.interested) ...[
            const SizedBox(height: 10),
            Text("We'll tell you by ${formatClock(o.expiresAt)}", style: AppText.body(13, color: AppColors.green)),
          ],
        ],
      ),
    );
  }

  /// One-ride offer, compact; opens the detail.
  Widget _rideCard(Offer o, DateTime now) {
    final slot = o.slots.firstOrNull;
    final time = slot == null ? '' : formatTime(slot.pickupTime);
    return Material(
      color: AppColors.card,
      borderRadius: BorderRadius.circular(16),
      child: InkWell(
        key: Key('requests.card.${o.id}'),
        borderRadius: BorderRadius.circular(16),
        onTap: () => _openDetail(o),
        child: Padding(
          padding: const EdgeInsets.all(14),
          child: Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Chip2('One ride', foreground: AppColors.ink),
                    const SizedBox(height: 8),
                    Text('${o.pickup.label} → ${o.drop.label}', style: AppText.body(15, weight: FontWeight.w600)),
                    const SizedBox(height: 2),
                    Text('${formatDate(o.startDate)} · $time · cover for another driver', style: AppText.body(13, color: AppColors.muted)),
                    if (o.interested) ...[
                      const SizedBox(height: 4),
                      Text("We'll tell you by ${formatClock(o.expiresAt)}", style: AppText.body(13, color: AppColors.green)),
                    ],
                  ],
                ),
              ),
              const SizedBox(width: 8),
              Text(minutesLeft(_secondsLeft(o, now)), style: AppText.body(13, weight: FontWeight.w600, color: AppColors.yellow)),
            ],
          ),
        ),
      ),
    );
  }

  Widget _resultCard(OfferResult r) {
    final text = r.result == 'rider_withdrew' ? 'The rider withdrew this request' : 'Went to another driver';
    return CardBox(
      key: Key('requests.result.${r.offerId}'),
      child: Row(
        children: [
          const Icon(Icons.info_outline, color: AppColors.muted, size: 20),
          const SizedBox(width: 10),
          Expanded(child: Text(text, style: AppText.body(14, weight: FontWeight.w600))),
        ],
      ),
    );
  }

  Widget _placeLine(IconData icon, Color color, String label) => Row(
        children: [
          Icon(icon, size: 16, color: color),
          const SizedBox(width: 8),
          Expanded(child: Text(label, style: AppText.body(14), maxLines: 1, overflow: TextOverflow.ellipsis)),
        ],
      );

  Widget _tile(String label, String value, {String? under}) => Container(
        padding: const EdgeInsets.all(10),
        decoration: BoxDecoration(color: AppColors.cardRaised, borderRadius: BorderRadius.circular(12)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(label, style: AppText.body(12, color: AppColors.muted)),
            const SizedBox(height: 4),
            Text(value, style: AppText.body(14, weight: FontWeight.w600)),
            if (under != null) ...[
              const SizedBox(height: 2),
              Text(under, style: AppText.body(11, color: AppColors.muted)),
            ],
          ],
        ),
      );
}
