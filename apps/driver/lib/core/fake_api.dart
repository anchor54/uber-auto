import 'api.dart';
import 'models.dart';
import 'schedule.dart';

/// In-memory backend used in demo mode and in every test, on the injected
/// clock (tests move the clock instead of waiting).
///
/// The demo story, for Ravi Kumar (auto), as in the backend's spec seed:
/// * The OTP is always [demoOtp]; any 10-digit number signs in as Ravi.
/// * With [seed], two offers are open from the clock's now: Priya's regular
///   ride (Koramangala → Whitefield, Mon/Wed/Fri 08:30 from next Monday, no
///   return, a [seriesWindow] window) and a one-ride offer (Kavya, tomorrow
///   09:00, a [rideWindow] window).
/// * When a window passes, the demo pick runs: if Ravi was interested he is
///   picked, the offer's rides join his week (14 days ahead, flagged new),
///   and a "picked" result shows. An offer he wasn't interested in just
///   leaves the list.
/// * Marking or withdrawing after the window: 409 `offer_closed`.
/// * Test hooks: [goesToAnotherDriver], [riderWithdraws], [closeWindows].
class FakeDriverApi implements DriverApi {
  FakeDriverApi({
    this.latency = Duration.zero,
    DateTime Function()? now,
    bool seed = true,
    this.seriesWindow = const Duration(minutes: 15),
    this.rideWindow = const Duration(minutes: 5),
  }) : _now = now ?? DateTime.now {
    if (seed) _seed();
  }

  static const demoOtp = '123456';
  static const demoPhone = '9000000201';
  static const demoName = 'Ravi Kumar';
  static const demoVehicleType = 'auto';

  final Duration latency;
  final Duration seriesWindow;
  final Duration rideWindow;
  final DateTime Function() _now;

  DriverProfile? _profile;
  final List<_FakeOffer> _offers = [];
  final List<WeekRide> _rides = [];
  final List<String> pushTokens = [];
  int _ids = 0;

  Future<void> _wait() => latency == Duration.zero ? Future<void>.value() : Future<void>.delayed(latency);

  void _seed() {
    final now = _now();
    final today = dateOnly(now);
    // Next week's Monday, as in the spec seed (Thu 19 Nov → Mon 23 Nov).
    final start = weekStart(addDays(today, 7));
    addSeriesOffer(
      rider: const OfferRider(firstName: 'Priya', name: 'Priya S.', rating: 4.8),
      pickup: const OfferPlace(label: 'Koramangala', lat: 12.9352, lng: 77.6245),
      drop: const OfferPlace(label: 'Whitefield', lat: 12.9698, lng: 77.7178),
      weekdays: const {1, 3, 5},
      pickupTime: 8 * 60 + 30,
      travelMinutes: 51,
      distanceKm: 15.1,
      fare: 197,
      startDate: start,
    );
    final tomorrow = addDays(today, 1);
    _offers.add(_FakeOffer(
      id: 'of_ride_${++_ids}',
      kind: OfferKind.ride,
      seriesId: 'sr_kavya',
      openedAt: now,
      expiresAt: now.add(rideWindow),
      rider: const OfferRider(firstName: 'Kavya', name: 'Kavya N.', rating: 4.7),
      pickup: const OfferPlace(label: 'Indiranagar', lat: 12.9784, lng: 77.6408),
      drop: const OfferPlace(label: 'Manyata Tech Park', lat: 13.0475, lng: 77.6203),
      startDate: tomorrow,
      distanceKm: 11.2,
      travelMinutes: 38,
      fare: 155,
      ridesPerMonth: 1,
      payment: PaymentMethod.cash,
      slots: [OfferSlot(weekday: tomorrow.weekday, direction: RideDirection.outbound, pickupTime: 9 * 60, estimatedDropTime: 9 * 60 + 38)],
    ));
  }

  /// Opens a new series offer to this driver now (demo and tests).
  Offer addSeriesOffer({
    required OfferRider rider,
    required OfferPlace pickup,
    required OfferPlace drop,
    required Set<int> weekdays,
    required int pickupTime,
    int? returnTime,
    required int travelMinutes,
    required double distanceKm,
    required int fare,
    required DateTime startDate,
    PaymentMethod payment = PaymentMethod.upi,
  }) {
    final now = _now();
    final days = weekdays.toList()..sort();
    final slots = [
      for (final d in days) OfferSlot(weekday: d, direction: RideDirection.outbound, pickupTime: pickupTime, estimatedDropTime: pickupTime + travelMinutes),
      if (returnTime != null)
        for (final d in days)
          OfferSlot(weekday: d, direction: RideDirection.returnLeg, pickupTime: returnTime, estimatedDropTime: returnTime + travelMinutes),
    ];
    final o = _FakeOffer(
      id: 'of_${++_ids}',
      kind: OfferKind.series,
      seriesId: 'sr_$_ids',
      openedAt: now,
      expiresAt: now.add(seriesWindow),
      rider: rider,
      pickup: pickup,
      drop: drop,
      startDate: startDate,
      distanceKm: distanceKm,
      travelMinutes: travelMinutes,
      fare: fare,
      ridesPerMonth: (slots.length * 30 / 7).round(),
      payment: payment,
      slots: slots,
    );
    _offers.add(o);
    return _view(o, now);
  }

  @override
  Future<DriverProfile?> restoreSession() async => _profile;

  @override
  Future<void> sendOtp(String phone) async {
    await _wait();
    if (!RegExp(r'^[6-9]\d{9}$').hasMatch(phone)) {
      throw const ApiException('invalid_phone', 'Enter a 10-digit mobile number starting with 6, 7, 8 or 9');
    }
  }

  @override
  Future<DriverProfile> verifyOtp(String phone, String code) async {
    await _wait();
    if (code != demoOtp) {
      throw const ApiException('invalid_code', "That code isn't right. Check the SMS and try again.");
    }
    return _profile = DriverProfile(phone: phone, name: demoName, vehicleType: demoVehicleType);
  }

  /// Runs the sweep: closes windows that have passed and makes the demo pick.
  void _catchUp() {
    final now = _now();
    for (final o in _offers) {
      if (o.closedAt != null || o.expiresAt.isAfter(now)) continue;
      _close(o, o.expiresAt, o.interested ? 'picked' : null);
    }
  }

  void _close(_FakeOffer o, DateTime at, String? result, {String outcome = 'assigned'}) {
    o.closedAt = at;
    o.outcome = outcome;
    o.result = result;
    if (result == 'picked') _win(o);
  }

  /// The won offer's rides, 14 days ahead from now, flagged new.
  void _win(_FakeOffer o) {
    final now = _now();
    final last = addDays(dateOnly(now), 14);
    var day = o.startDate.isAfter(dateOnly(now)) ? o.startDate : addDays(dateOnly(now), 1);
    while (!day.isAfter(last)) {
      for (final s in o.slots) {
        if (s.weekday != day.weekday) continue;
        _rides.add(WeekRide(
          id: 'ride_${o.id}_${dateKeyOf(day)}_${s.direction.name}',
          seriesId: o.seriesId,
          date: day,
          weekday: day.weekday,
          direction: s.direction,
          pickupTime: s.pickupTime,
          dropTime: s.estimatedDropTime,
          riderName: o.riderFullName,
          fare: o.fare,
          isNew: true,
          pickupLabel: s.direction == RideDirection.outbound ? o.pickup.label : o.drop.label,
          dropLabel: s.direction == RideDirection.outbound ? o.drop.label : o.pickup.label,
        ));
      }
      if (o.kind == OfferKind.ride) break;
      day = addDays(day, 1);
    }
    o.wonAt = now;
  }

  static String dateKeyOf(DateTime d) =>
      '${d.year}${d.month.toString().padLeft(2, '0')}${d.day.toString().padLeft(2, '0')}';

  /// The pick gives [offerId] to another driver now.
  void goesToAnotherDriver(String offerId) {
    final o = _find(offerId);
    if (o.closedAt != null) return;
    _close(o, _now(), o.interested ? 'not_picked' : null);
  }

  /// The rider cancels the request now.
  void riderWithdraws(String offerId) {
    final o = _find(offerId);
    if (o.closedAt != null) return;
    _close(o, _now(), null, outcome: 'cancelled');
  }

  /// Closes every open window now, as if it had passed: the demo pick runs.
  void closeWindows() {
    final now = _now();
    for (final o in _offers) {
      if (o.closedAt == null) _close(o, now, o.interested ? 'picked' : null);
    }
  }

  _FakeOffer _find(String id) {
    for (final o in _offers) {
      if (o.id == id) return o;
    }
    throw const ApiException('not_found', 'No such offer');
  }

  Offer _view(_FakeOffer o, DateTime now) {
    final open = o.closedAt == null && now.isBefore(o.expiresAt);
    final left = open ? o.expiresAt.difference(now).inSeconds : 0;
    return Offer(
      id: o.id,
      kind: o.kind,
      seriesId: o.seriesId,
      round: 1,
      status: o.closedAt != null ? OfferStatus.closed : (open ? OfferStatus.open : OfferStatus.closing),
      outcome: o.outcome,
      openedAt: o.openedAt,
      expiresAt: o.expiresAt,
      secondsLeft: left,
      fetchedAt: now,
      canRespond: open,
      interested: o.interested,
      interestedAt: o.interestedAt,
      dismissed: o.dismissed,
      result: o.result,
      rider: o.rider,
      vehicleType: demoVehicleType,
      pickup: o.pickup,
      drop: o.drop,
      startDate: o.startDate,
      distanceKm: o.distanceKm,
      travelMinutes: o.travelMinutes,
      farePerRide: o.fare,
      ridesPerMonth: o.ridesPerMonth,
      farePerMonth: o.ridesPerMonth * o.fare,
      usualPayment: o.payment,
      slots: o.slots,
    );
  }

  @override
  Future<OfferList> offers() async {
    await _wait();
    _catchUp();
    final now = _now();
    final open = [for (final o in _offers) if (o.closedAt == null && !o.dismissed) o]
      ..sort((a, b) => b.openedAt.compareTo(a.openedAt));
    final results = <OfferResult>[];
    for (final o in _offers.reversed) {
      final closed = o.closedAt;
      if (closed == null || !o.interested || now.difference(closed) >= const Duration(hours: 24)) continue;
      final (result, message) = switch ((o.outcome, o.result)) {
        ('cancelled', _) => ('rider_withdrew', 'The rider withdrew this request'),
        (_, 'picked') => ('picked', 'You got it.'),
        _ => ('went_to_another_driver', 'Went to another driver'),
      };
      results.add(OfferResult(offerId: o.id, seriesId: o.seriesId, result: result, message: message, closedAt: closed));
    }
    return OfferList(offers: [for (final o in open) _view(o, now)], results: results);
  }

  @override
  Future<Offer> offer(String id) async {
    await _wait();
    _catchUp();
    return _view(_find(id), _now());
  }

  void _respond(String id, void Function(_FakeOffer o) change) {
    _catchUp();
    final o = _find(id);
    final now = _now();
    if (o.closedAt != null || !now.isBefore(o.expiresAt)) {
      throw ApiException('offer_closed', 'This request has closed.', state: _view(o, now));
    }
    change(o);
  }

  @override
  Future<void> markInterested(String id) async {
    await _wait();
    _respond(id, (o) {
      if (o.interested) return;
      o.interested = true;
      o.interestedAt = _now();
    });
  }

  @override
  Future<void> withdrawInterest(String id) async {
    await _wait();
    _respond(id, (o) {
      o.interested = false;
      o.interestedAt = null;
    });
  }

  @override
  Future<void> dismissOffer(String id) async {
    await _wait();
    _catchUp();
    final o = _find(id);
    o.dismissed = true;
    if (o.closedAt == null) {
      o.interested = false;
      o.interestedAt = null;
    }
  }

  @override
  Future<DriverWeek> week({DateTime? start}) async {
    await _wait();
    _catchUp();
    final now = _now();
    final from = dateOnly(start ?? weekStart(now));
    final to = addDays(from, 6);
    final rides = [
      for (final r in _rides)
        if (!r.date.isBefore(from) && !r.date.isAfter(to))
          WeekRide(
            id: r.id,
            seriesId: r.seriesId,
            date: r.date,
            weekday: r.weekday,
            direction: r.direction,
            pickupTime: r.pickupTime,
            dropTime: r.dropTime,
            riderName: r.riderName,
            fare: r.fare,
            isNew: _isNew(r, now),
            pickupLabel: r.pickupLabel,
            dropLabel: r.dropLabel,
          ),
    ]..sort((a, b) => a.date != b.date ? a.date.compareTo(b.date) : a.pickupTime.compareTo(b.pickupTime));
    return DriverWeek(weekStart: from, weekEnd: to, rides: rides);
  }

  bool _isNew(WeekRide r, DateTime now) {
    for (final o in _offers) {
      if (o.seriesId == r.seriesId && o.wonAt != null) return now.difference(o.wonAt!) < const Duration(hours: 24);
    }
    return false;
  }

  @override
  Future<void> registerDevice(String pushToken, String platform) async {
    await _wait();
    pushTokens.add(pushToken);
  }
}

class _FakeOffer {
  _FakeOffer({
    required this.id,
    required this.kind,
    required this.seriesId,
    required this.openedAt,
    required this.expiresAt,
    required this.rider,
    required this.pickup,
    required this.drop,
    required this.startDate,
    required this.distanceKm,
    required this.travelMinutes,
    required this.fare,
    required this.ridesPerMonth,
    required this.payment,
    required this.slots,
  });

  final String id;
  final OfferKind kind;
  final String seriesId;
  final DateTime openedAt;
  final DateTime expiresAt;
  final OfferRider rider;
  final OfferPlace pickup;
  final OfferPlace drop;
  final DateTime startDate;
  final double distanceKm;
  final int travelMinutes;
  final int fare;
  final int ridesPerMonth;
  final PaymentMethod payment;
  final List<OfferSlot> slots;

  bool interested = false;
  DateTime? interestedAt;
  bool dismissed = false;
  DateTime? closedAt;
  String? outcome;
  String? result;
  DateTime? wonAt;

  /// "Priya Sharma" in the week, from "Priya S." on the offer.
  String get riderFullName => switch (rider.firstName) {
        'Priya' => 'Priya Sharma',
        'Kavya' => 'Kavya Nair',
        _ => rider.name,
      };
}
