import 'dart:async';

import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../core/schedule.dart';
import '../../ui/route_map.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';

/// D2.2 Request detail: the map, each weekday's pickup and estimated drop-off,
/// the return leg if any, the earnings, and "I'm interested" until the
/// window closes (then "Closed"). Once interested: when we'll tell, and
/// "Not interested any more".
///
/// One-ride offers open here too until D2.4 is built.
class RequestDetailScreen extends StatefulWidget {
  const RequestDetailScreen({super.key, required this.offerId, this.offer});

  final String offerId;

  /// The offer as the list had it, drawn at once while it is read again.
  final Offer? offer;

  @override
  State<RequestDetailScreen> createState() => _RequestDetailScreenState();
}

class _RequestDetailScreenState extends State<RequestDetailScreen> {
  Offer? _offer;
  ApiException? _error;
  bool _busy = false;
  Timer? _tick;
  bool _started = false;

  @override
  void initState() {
    super.initState();
    _offer = widget.offer;
    // The countdown, and "Closed" once the window passes.
    _tick = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted) setState(() {});
    });
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!_started) {
      _started = true;
      _load();
    }
  }

  @override
  void dispose() {
    _tick?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final o = await AppScope.of(context).api.offer(widget.offerId);
      if (!mounted) return;
      setState(() {
        _offer = o;
        _error = null;
      });
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() => _error = e);
    }
  }

  /// Runs [call]; a 409 offer_closed redraws from the offer it carries.
  Future<void> _act(Future<void> Function() call, {bool popAfter = false}) async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await call();
      if (!mounted) return;
      if (popAfter) {
        Navigator.of(context).pop(true);
        return;
      }
      await _load();
    } on ApiException catch (e) {
      if (!mounted) return;
      final state = e.state;
      if (state is Offer) {
        setState(() => _offer = state);
      } else {
        await _load();
      }
      if (mounted && e.code != 'offer_closed') showInfo(context, e.message);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final o = _offer;
    final now = AppScope.of(context).clock.now();
    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            const FlowHeader(label: 'Request'),
            Expanded(
              child: o == null
                  ? Center(
                      child: _error == null
                          ? const CircularProgressIndicator(color: AppColors.yellow)
                          : Padding(
                              padding: const EdgeInsets.all(24),
                              child: Text(_error!.message, textAlign: TextAlign.center, style: AppText.body(15, color: AppColors.muted)),
                            ),
                    )
                  : _body(o),
            ),
            if (o != null) _actions(o, now),
          ],
        ),
      ),
    );
  }

  Widget _body(Offer o) {
    final details = [
      'Starts ${formatDate(o.startDate)}',
      if (o.vehicleType.isNotEmpty) vehicleLabel(o.vehicleType),
      for (final p in o.preferences) preferenceLabel(p),
      '${o.rider.rating} rating',
    ].join(' · ');
    return ListView(
      key: const Key('detail.list'),
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 16),
      children: [
        Semantics(
          header: true,
          child: Text(
            o.kind == OfferKind.ride ? 'Cover one ride' : "${o.rider.firstName}'s regular ride",
            style: AppText.display(26),
          ),
        ),
        if (o.kind == OfferKind.slot && o.outbound.isNotEmpty) ...[
          const SizedBox(height: 8),
          Align(alignment: Alignment.centerLeft, child: Chip2('${weekdayPlural(o.outbound.first.weekday)} only', foreground: AppColors.ink)),
        ],
        const SizedBox(height: 6),
        Text(details, style: AppText.body(14, color: AppColors.muted, height: 1.4)),
        const SizedBox(height: 4),
        Text('${o.pickup.label} → ${o.drop.label} · about ${o.travelMinutes} min', style: AppText.body(14, weight: FontWeight.w600)),
        const SizedBox(height: 12),
        RouteMap(pickup: o.pickup, drop: o.drop),
        const SizedBox(height: 12),
        CardBox(child: _schedule(o)),
        const SizedBox(height: 12),
        CardBox(
          child: Column(
            children: [
              SummaryRow('Fare per ride, paid to you', '₹${o.farePerRide}', valueStyle: AppText.display(22, letterSpacing: 0)),
              const SizedBox(height: 4),
              const Hairline(),
              const SizedBox(height: 4),
              SummaryRow('Rides a month', 'About ${o.ridesPerMonth}'),
              SummaryRow('Usually pays by', o.usualPayment.label),
            ],
          ),
        ),
        if (o.result != null) ...[
          const SizedBox(height: 12),
          CardBox(
            key: const Key('detail.result'),
            color: o.result == 'picked' ? AppColors.greenDark : AppColors.card,
            child: Text(
              o.result == 'picked' ? "You got it. ${o.rider.firstName} has been told." : 'This one went to another driver.',
              style: AppText.body(14, weight: FontWeight.w600, color: o.result == 'picked' ? AppColors.green : AppColors.ink),
            ),
          ),
        ],
      ],
    );
  }

  /// Days, Pickup, "Drop, about", days with equal times grouped; then the
  /// return leg's rows ("Return, Mon–Fri").
  Widget _schedule(Offer o) {
    Map<int, (int, int?)> times(List<OfferSlot> slots) => {for (final s in slots) s.weekday: (s.pickupTime, s.estimatedDropTime)};
    final head = AppText.body(12, weight: FontWeight.w600, color: AppColors.muted);
    final cell = AppText.body(14, weight: FontWeight.w600);
    Widget row(String a, String b, String c, TextStyle style) => Padding(
          padding: const EdgeInsets.symmetric(vertical: 5),
          child: Row(
            children: [
              Expanded(flex: 4, child: Text(a, style: style)),
              Expanded(flex: 3, child: Text(b, style: style)),
              Expanded(flex: 3, child: Text(c, style: style)),
            ],
          ),
        );
    return Column(
      children: [
        row('Days', 'Pickup', 'Drop, about', head),
        for (final (days, (pickup, drop)) in groupDays(times(o.outbound)))
          row(daysRangeLabel(days), formatTime(pickup), drop == null ? '—' : formatTime(drop), cell),
        for (final (days, (pickup, drop)) in groupDays(times(o.returns)))
          row('Return, ${daysRangeLabel(days)}', formatTime(pickup), drop == null ? '—' : formatTime(drop), cell),
      ],
    );
  }

  Widget _actions(Offer o, DateTime now) {
    final api = AppScope.of(context).api;
    final open = o.openAt(now);
    final left = o.closesAt.difference(now).inSeconds;
    final Widget content;
    if (!open) {
      content = const PrimaryButton(key: Key('detail.interested'), label: 'Closed', onPressed: null);
    } else if (o.interested) {
      content = Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          CardBox(
            key: const Key('detail.interestedPanel'),
            color: AppColors.greenDark,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  "You're interested. We'll tell you by ${formatClock(o.expiresAt)} whether you got it.",
                  style: AppText.body(15, weight: FontWeight.w600, color: AppColors.green, height: 1.4),
                ),
                const SizedBox(height: 8),
                Text(
                  "Saying you're interested isn't a booking. When the request closes, one interested driver whose week fits "
                  'every one of these rides is picked at random.',
                  style: AppText.body(13, color: AppColors.ink, height: 1.45),
                ),
              ],
            ),
          ),
          const SizedBox(height: 4),
          Center(
            child: LinkButton(
              key: const Key('detail.withdraw'),
              label: 'Not interested any more',
              onPressed: _busy ? null : () => _act(() => api.withdrawInterest(o.id)),
            ),
          ),
        ],
      );
    } else {
      content = Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Align(alignment: Alignment.centerLeft, child: Chip2(closesIn(left < 0 ? 0 : left))),
          const SizedBox(height: 10),
          Row(
            children: [
              Expanded(
                child: OutlineButtonBox(
                  key: const Key('detail.notForMe'),
                  label: 'Not for me',
                  height: 54,
                  onPressed: _busy ? null : () => _act(() => api.dismissOffer(o.id), popAfter: true),
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: PrimaryButton(
                  key: const Key('detail.interested'),
                  label: "I'm interested",
                  loading: _busy,
                  onPressed: () => _act(() => api.markInterested(o.id)),
                ),
              ),
            ],
          ),
        ],
      );
    }
    return Padding(padding: const EdgeInsets.fromLTRB(16, 8, 16, 16), child: content);
  }
}
