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
import 'setup_controller.dart';

/// The toast on Home after a request is sent (R1.4 → R1.5).
const requestSentMessage = "Request sent. We'll let you know who your driver is.";

/// R1.4 Review and request (step 3 of 3). Nothing is promised yet: "Request a
/// regular driver" sends series.create (or series.retry, when changing a
/// request that found no driver) and goes back to Home.
///
/// With [series] it is read-only: "View request" on the R1.5 card.
class ReviewRequestScreen extends StatefulWidget {
  const ReviewRequestScreen({super.key, required this.setup, this.series});

  final CommuteSetup setup;

  /// The sent request to show, read-only; null while setting one up.
  final Series? series;

  @override
  State<ReviewRequestScreen> createState() => _ReviewRequestScreenState();
}

class _ReviewRequestScreenState extends State<ReviewRequestScreen> {
  CommuteSetup get setup => widget.setup;
  bool get readOnly => widget.series != null;

  SeriesPreview? _preview;
  ApiException? _loadError;
  bool _sending = false;
  ApiException? _sendError;
  bool _started = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_started) return;
    _started = true;
    final s = widget.series;
    if (s != null) {
      _preview = SeriesPreview(
        rideType: s.rideType,
        distanceKm: s.distanceKm,
        travelMinutes: s.travelMinutes,
        startDate: s.startDate,
        suggestedFare: s.suggestedFare,
        ridesPerMonth: (s.slots.length * 30 / 7).round(),
        slots: s.slots,
      );
    } else {
      _load();
    }
  }

  Future<void> _load() async {
    setState(() => _loadError = null);
    try {
      final p = await AppScope.of(context).api.previewSeries(setup.toRequest());
      if (!mounted) return;
      setup.setPreview(p);
      setState(() => _preview = p);
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() => _loadError = e);
    }
  }

  bool get _retrying => setup.retrySeriesId != null;

  Future<void> _send() async {
    if (_sending) return;
    setState(() {
      _sending = true;
      _sendError = null;
    });
    final api = AppScope.of(context).api;
    try {
      final id = setup.retrySeriesId;
      if (id == null) {
        await api.createSeries(setup.toRequest());
      } else {
        await api.retrySeries(id, changes: setup.toRequest());
      }
      if (!mounted) return;
      final messenger = ScaffoldMessenger.of(context);
      Navigator.of(context).pushAndRemoveUntil(
        MaterialPageRoute<void>(builder: (_) => const HomeScreen()),
        (_) => false,
      );
      messenger.showSnackBar(const SnackBar(content: Text(requestSentMessage)));
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _sending = false;
        _sendError = e;
      });
    }
  }

  /// R1.4 sits on R1.3, which sits on R1.2.
  void _backToRideType() => Navigator.of(context).pop();

  void _backToTimes() {
    var n = 0;
    Navigator.of(context).popUntil((_) => n++ >= 2);
  }

  @override
  Widget build(BuildContext context) {
    final p = _preview;
    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            FlowHeader(label: readOnly ? 'Your request' : 'Step 3 of 3'),
            Expanded(
              child: ListView(
                key: const Key('review.list'),
                padding: const EdgeInsets.only(bottom: 16),
                children: [
                  ScreenTitle(
                    'Review and request',
                    subtitle: readOnly
                        ? "We'll let you know who your driver is, usually within 15 minutes."
                        : "Check your regular ride. We'll find the driver after you send it.",
                  ),
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    child: p == null ? _loading() : _content(p),
                  ),
                ],
              ),
            ),
            if (!readOnly)
              BottomActions(
                children: [
                  if (_sendError != null) ...[
                    _sendErrorPanel(_sendError!),
                    const SizedBox(height: 10),
                  ],
                  PrimaryButton(
                    key: const Key('review.request'),
                    label: _retrying ? 'Try again with these changes' : 'Request a regular driver',
                    height: 54,
                    loading: _sending,
                    onPressed: p == null ? null : _send,
                  ),
                ],
              ),
          ],
        ),
      ),
    );
  }

  Widget _loading() {
    final e = _loadError;
    if (e == null) {
      return const Padding(
        padding: EdgeInsets.all(32),
        child: Center(child: CircularProgressIndicator(color: AppColors.green)),
      );
    }
    return CardBox(
      key: const Key('review.loadError'),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(e.message, style: AppText.body(14, color: AppColors.red, height: 1.4)),
          const SizedBox(height: 8),
          _fixLink(e) ?? LinkButton(key: const Key('review.reload'), label: 'Try again', onPressed: _load),
        ],
      ),
    );
  }

  /// A link back to the step that needs fixing, for a 422.
  Widget? _fixLink(ApiException e) {
    if (e.code == 'ride_type_not_allowed') {
      return LinkButton(key: const Key('review.fixRideType'), label: 'Change ride type', onPressed: _backToRideType);
    }
    if (e.code == 'start_too_soon' || e.code == 'invalid_time' || e.code == 'invalid_request') {
      return LinkButton(key: const Key('review.fixTimes'), label: 'Change times', onPressed: _backToTimes);
    }
    return null;
  }

  Widget _sendErrorPanel(ApiException e) {
    return Container(
      key: const Key('review.error'),
      width: double.infinity,
      padding: const EdgeInsets.fromLTRB(14, 10, 8, 6),
      decoration: BoxDecoration(color: AppColors.orangeTint, borderRadius: BorderRadius.circular(12)),
      child: Row(
        children: [
          Expanded(child: Text(e.message, style: AppText.body(13, color: AppColors.orangeText, height: 1.4))),
          _fixLink(e) ??
              LinkButton(key: const Key('review.retry'), label: 'Try again', onPressed: _send, color: AppColors.orangeText),
        ],
      ),
    );
  }

  Widget _content(SeriesPreview p) {
    final type = p.rideType;
    final km = p.distanceKm % 1 == 0 ? p.distanceKm.toStringAsFixed(0) : p.distanceKm.toStringAsFixed(1);
    final mapLabel = 'About ${p.travelMinutes} min · $km km';
    final hasReturn = p.slots.any((s) => s.direction == RideDirection.returnLeg);
    final route = hasReturn ? '${setup.pickup!.title} ⇄ ${setup.drop!.title}' : '${setup.pickup!.title} → ${setup.drop!.title}';
    final typeText = [type.label, for (final pref in RidePreference.values) if (setup.preferences.contains(pref)) pref.label].join(' · ');
    final city = cityName(widget.series?.city ?? defaultCity);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        RideMap(
          pickup: setup.pickup!,
          drop: setup.drop!,
          height: 140,
          label: mapLabel,
          fallback: RouteMapPreview(label: mapLabel, height: 140),
        ),
        const SizedBox(height: 12),
        CardBox(
          key: const Key('review.summary'),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Your regular ride', style: AppText.body(15, weight: FontWeight.w600)),
              const SizedBox(height: 12),
              SummaryRow('Route', route, valueKey: const Key('review.route')),
              const SizedBox(height: 8),
              SummaryRow('Ride type', typeText, valueKey: const Key('review.rideType')),
              const SizedBox(height: 8),
              SummaryRow('First ride', formatDate(p.startDate), valueKey: const Key('review.firstRide')),
              const SizedBox(height: 12),
              const Hairline(),
              const SizedBox(height: 8),
              _table(p, hasReturn),
              const SizedBox(height: 8),
              const Hairline(),
              const SizedBox(height: 10),
              PriceRow(label: 'Fare per ride', price: formatRupees(p.suggestedFare), priceKey: const Key('review.fare')),
            ],
          ),
        ),
        const SizedBox(height: 12),
        CardBox(
          color: AppColors.greenTint,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('What happens next', style: AppText.body(15, weight: FontWeight.w600)),
              const SizedBox(height: 10),
              CheckLine("${type.shortLabel} drivers in $city see your request and say if they're interested."),
              const SizedBox(height: 8),
              const CheckLine('We\'ll notify you who your driver is, usually within 15 minutes. No need to keep the app open.'),
              const SizedBox(height: 8),
              const CheckLine('Nothing to pay now. You pay your driver after each ride.'),
            ],
          ),
        ),
        const SizedBox(height: 12),
        CardBox(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text("How you'll usually pay your driver", style: AppText.body(15, weight: FontWeight.w600)),
              const SizedBox(height: 10),
              IgnorePointer(
                ignoring: readOnly,
                child: PillSegments<PaymentMethod>(
                  label: "How you'll usually pay your driver",
                  value: setup.usualPayment,
                  options: [for (final m in PaymentMethod.values) (m, m.label)],
                  onChanged: (m) => setState(() => setup.setUsualPayment(m)),
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }

  /// Days with the same times grouped: "Mon–Thu 8:40 AM 6:30 PM".
  Widget _table(SeriesPreview p, bool hasReturn) {
    final byDay = <int, (int, int?)>{};
    for (final s in p.slots.where((s) => s.direction == RideDirection.outbound)) {
      final back = p.slots.where((r) => r.direction == RideDirection.returnLeg && r.weekday == s.weekday).firstOrNull;
      byDay[s.weekday] = (s.pickupTime, back?.pickupTime);
    }
    final groups = groupDays(byDay);
    final head = AppText.body(12, weight: FontWeight.w600, color: AppColors.muted);
    Widget row(String a, String b, String c, {TextStyle? style, Key? key}) => Padding(
          key: key,
          padding: const EdgeInsets.symmetric(vertical: 5),
          child: Row(
            children: [
              Expanded(flex: 4, child: Text(a, style: style)),
              Expanded(flex: 3, child: Text(b, style: style)),
              if (hasReturn) Expanded(flex: 3, child: Text(c, style: style)),
            ],
          ),
        );
    final body = AppText.body(14, weight: FontWeight.w600);
    return Column(
      children: [
        row('Days', 'Pickup', 'Return', style: head),
        for (final (days, (pickup, back)) in groups)
          row(
            daysRangeLabel(days),
            formatTime(pickup),
            back == null ? '—' : formatTime(back),
            style: body,
            key: Key('review.days.${days.map(dayCode).join('_')}'),
          ),
      ],
    );
  }
}
