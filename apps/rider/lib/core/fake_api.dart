import 'api.dart';
import 'fake_ride_script.dart';
import 'fares.dart';
import 'models.dart';
import 'schedule.dart';

/// In-memory backend used in demo mode and in every test.
///
/// It plays the same story as the real backend (backend/API.md), on the
/// injected clock, so tests move the clock instead of waiting:
/// * The OTP is always [demoOtp]. The rider is "Priya Sharma".
/// * Creating a series opens round 1 of offers for [roundLength] (15 minutes,
///   as on the backend; demo mode uses 8 seconds). When a round closes, the
///   demo driver Ravi was interested and is picked: the series becomes
///   active with Ravi on every weekday, and two weeks of rides are created.
/// * With the "Woman driver" preference nobody is interested: after 3 rounds
///   the series is `no_driver_found`, so the "No driver yet" path can be
///   tested. So is every request while [driversInterested] is false.
/// * Ride types in [noDriverTypes] have no eligible driver: straight to
///   `no_driver_found`, no rounds.
/// * [closeRound] closes the open round now, whatever the clock says.
/// * An active series has a ride per slot for the next two weeks (flow R2).
///   [playNextRide] makes the next one play itself out; in demo mode
///   [demoRideAfter] does that on its own, sped up by [rideSpeed].
class FakeRiderApi implements RiderApi {
  FakeRiderApi({
    this.latency = Duration.zero,
    List<RideType>? rideTypes,
    this.roundLength = const Duration(minutes: 15),
    this.driversInterested = true,
    Set<RideType>? noDriverTypes,
    this.demoRideAfter,
    this.rideSpeed = 1,
    DateTime Function()? now,
  })  : rideTypes = rideTypes ?? RideType.values,
        noDriverTypes = noDriverTypes ?? <RideType>{},
        _explicitNow = now != null,
        _now = now ?? DateTime.now;

  static const demoOtp = '123456';

  /// Rounds per request before `no_driver_found`, as on the backend.
  static const maxRounds = 3;

  final Duration latency;

  /// The ride types the config allows for regular rides, in config order.
  final List<RideType> rideTypes;

  /// How long each round of offers stays open.
  final Duration roundLength;

  /// Whether the demo driver says "I'm interested" (unless the rider asked
  /// for a woman driver).
  bool driversInterested;

  /// Ride types with no eligible driver at all.
  final Set<RideType> noDriverTypes;

  /// Demo mode: once the series is active, the next ride starts playing after this long.
  final Duration? demoRideAfter;

  /// How much faster than real time a scripted ride plays.
  final double rideSpeed;
  DateTime Function() _now;
  final bool _explicitNow;

  /// Uses the app's clock, unless the test gave this backend its own.
  void syncClock(DateTime Function() now) {
    if (!_explicitNow) _now = now;
  }

  final Map<String, Ride> _rides = {};
  String? _scriptedRideId;
  FakeRideScript? _script;

  /// Push tokens registered, for tests.
  final List<String> pushTokens = [];

  /// Every request sent to create or retry a series, for tests.
  final List<CommuteRequest> requests = [];

  RiderProfile? _profile;

  /// Newest last.
  final List<Series> _series = [];
  int _ids = 0;

  /// The latest series, for tests to inspect.
  Series? get series {
    _catchUp();
    return _series.isEmpty ? null : _series.last;
  }

  /// Every series, oldest first.
  List<Series> get allSeries {
    _catchUp();
    return List.unmodifiable(_series);
  }

  Future<void> _wait() => latency == Duration.zero ? Future<void>.value() : Future<void>.delayed(latency);

  /// Demo sessions live as long as this object: a fresh app starts signed out.
  @override
  Future<RiderProfile?> restoreSession() async => _profile;

  @override
  Future<void> sendOtp(String phone) async {
    await _wait();
    if (!RegExp(r'^[6-9]\d{9}$').hasMatch(phone)) {
      throw const ApiException('invalid_phone', 'Enter a 10-digit mobile number starting with 6, 7, 8 or 9');
    }
  }

  @override
  Future<RiderProfile> verifyOtp(String phone, String code) async {
    await _wait();
    if (code != demoOtp) {
      throw const ApiException('invalid_code', "That code isn't right. Check the SMS and try again.");
    }
    return _profile = RiderProfile(phone: phone, name: demoRiderName);
  }

  @override
  Future<List<Place>> searchPlaces(String query) async {
    await _wait();
    final q = query.trim().toLowerCase();
    if (q.isEmpty) return demoPlaces;
    return demoPlaces.where((p) => p.title.toLowerCase().contains(q) || p.detail.toLowerCase().contains(q)).toList();
  }

  @override
  Future<List<RideTypeOption>> rideTypeOptions(CommuteRequest request) async {
    await _wait();
    final km = roadKm(request.pickup, request.drop);
    return [for (final t in rideTypes) RideTypeOption(type: t, fare: suggestedFare(t, km))];
  }

  // ---------------------------------------------------------------- R1

  DateTime get _today => dateOnly(_now());

  /// What the backend checks and works out for a request (series.preview).
  SeriesPreview _preview(CommuteRequest r) {
    if (!rideTypes.contains(r.rideType)) {
      throw ApiException('ride_type_not_allowed', '${r.rideType.shortLabel} rides aren\'t available for regular rides here.');
    }
    if (r.slots.isEmpty) throw const ApiException('invalid_request', 'Pick at least one day.');
    final start = r.startDate;
    if (start != null && !dateOnly(start).isAfter(_today)) {
      throw const ApiException('start_too_soon', 'The first ride can be tomorrow at the earliest.');
    }
    final km = roadKm(r.pickup, r.drop);
    final minutes = travelMinutes(r.rideType, km);
    final slots = <SeriesSlot>[];
    for (final s in r.slots) {
      final pickup = s.timing == TimingMode.arriveBy ? pickupForArrival(s.time, minutes) : s.time;
      if (pickup < 0) throw const ApiException('invalid_time', 'That arrival time is too early for this trip.');
      slots.add(SeriesSlot(
        weekday: s.weekday,
        direction: s.direction,
        timing: s.timing,
        arriveByTime: s.timing == TimingMode.arriveBy ? s.time : null,
        pickupTime: pickup,
        estimatedDropTime: pickup + minutes,
      ));
    }
    slots.sort((a, b) => a.weekday != b.weekday ? a.weekday.compareTo(b.weekday) : a.direction.index.compareTo(b.direction.index));
    final days = {for (final s in slots) s.weekday};
    var from = _today;
    if (start != null && dateOnly(start).isAfter(from.add(const Duration(days: 1)))) {
      from = dateOnly(start).subtract(const Duration(days: 1));
    }
    return SeriesPreview(
      rideType: r.rideType,
      distanceKm: (km * 10).round() / 10,
      travelMinutes: minutes,
      startDate: firstRideDate(from, days),
      suggestedFare: suggestedFare(r.rideType, km),
      ridesPerMonth: (slots.length * 30 / 7).round(),
      slots: slots,
    );
  }

  @override
  Future<SeriesPreview> previewSeries(CommuteRequest request) async {
    await _wait();
    return _preview(request);
  }

  SeriesOffer _openRound(int round, DateTime at) {
    _ids++;
    return SeriesOffer(id: 'of_$_ids', round: round, status: OfferStatus.open, openedAt: at, expiresAt: at.add(roundLength));
  }

  SeriesOffer _closed(SeriesOffer o, OfferOutcome outcome) =>
      SeriesOffer(id: o.id, round: o.round, status: OfferStatus.closed, openedAt: o.openedAt, expiresAt: o.expiresAt, outcome: outcome);

  /// A request goes out: round 1, or no rounds at all when nobody is eligible.
  Series _request(Series s) {
    if (noDriverTypes.contains(s.rideType)) return s.copyWith(status: SeriesStatus.noDriverFound, clearOffer: true);
    return s.copyWith(status: SeriesStatus.findingDriver, offer: _openRound(1, _now()));
  }

  @override
  Future<Series> createSeries(CommuteRequest request) async {
    await _wait();
    final p = _preview(request);
    requests.add(request);
    _ids++;
    final s = Series(
      id: 'sr_$_ids',
      status: SeriesStatus.findingDriver,
      rideType: request.rideType,
      preferences: {...request.preferences},
      pickup: request.pickup,
      drop: request.drop,
      startDate: p.startDate,
      distanceKm: p.distanceKm,
      travelMinutes: p.travelMinutes,
      suggestedFare: p.suggestedFare,
      usualPayment: request.usualPayment,
      requestedAt: _now(),
      slots: [for (final (i, slot) in p.slots.indexed) _withId(slot, 'sl_${_ids}_$i')],
      version: 0,
    );
    final created = _request(s);
    _series.add(created);
    return created;
  }

  SeriesSlot _withId(SeriesSlot s, String id) => SeriesSlot(
        id: id,
        weekday: s.weekday,
        direction: s.direction,
        timing: s.timing,
        arriveByTime: s.arriveByTime,
        pickupTime: s.pickupTime,
        estimatedDropTime: s.estimatedDropTime,
        driver: s.driver,
        active: s.active,
      );

  int _indexOf(String id) {
    final i = _series.indexWhere((s) => s.id == id);
    if (i < 0) throw const ApiException('not_found', 'No such series');
    return i;
  }

  @override
  Future<Series> getSeries(String id) async {
    await _wait();
    _catchUp();
    return _series[_indexOf(id)];
  }

  @override
  Future<List<Series>> listSeries() async {
    await _wait();
    _catchUp();
    return _series.reversed.toList();
  }

  @override
  Future<void> cancelRequest(String id) async {
    await _wait();
    _catchUp();
    final i = _indexOf(id);
    final s = _series[i];
    switch (s.status) {
      case SeriesStatus.ended:
        return;
      case SeriesStatus.findingDriver:
      case SeriesStatus.noDriverFound:
        final o = s.offer;
        _series[i] = s.copyWith(
          status: SeriesStatus.ended,
          offer: o != null && o.status != OfferStatus.closed ? _closed(o, OfferOutcome.cancelled) : o,
        );
      case SeriesStatus.active:
      case SeriesStatus.paused:
        final driver = s.slots.map((x) => x.driver).whereType<Driver>().firstOrNull;
        throw ApiException(
          'already_assigned',
          '${driver?.firstName ?? 'A driver'} was just assigned.',
          state: s,
        );
    }
  }

  @override
  Future<Series> retrySeries(String id, {CommuteRequest? changes}) async {
    await _wait();
    _catchUp();
    final i = _indexOf(id);
    final s = _series[i];
    if (s.status != SeriesStatus.noDriverFound) {
      throw ApiException('not_retryable', 'This request is not waiting for a retry.', state: s);
    }
    final base = s.toRequest();
    final r = changes ?? base;
    requests.add(r);
    final p = _preview(r);
    final next = _request(s.copyWith(
      rideType: r.rideType,
      preferences: {...r.preferences},
      usualPayment: r.usualPayment,
      startDate: p.startDate,
      suggestedFare: p.suggestedFare,
      travelMinutes: p.travelMinutes,
      distanceKm: p.distanceKm,
      slots: [for (final (j, slot) in p.slots.indexed) _withId(slot, '${s.id}_r${s.version}_$j')],
    ));
    _series[i] = next;
    return next;
  }

  /// Closes every open round now, as if its window had passed: the pick runs.
  void closeRound() {
    final now = _now();
    for (var i = 0; i < _series.length; i++) {
      final s = _series[i];
      final o = s.offer;
      if (s.status != SeriesStatus.findingDriver || o == null || o.status != OfferStatus.open) continue;
      if (o.expiresAt.isAfter(now)) {
        _series[i] = s.copyWith(
          offer: SeriesOffer(id: o.id, round: o.round, status: o.status, openedAt: o.openedAt, expiresAt: now),
        );
      }
    }
    _catchUp();
  }

  /// Runs the sweep: closes rounds whose window has passed and picks.
  void _catchUp() {
    final now = _now();
    for (var i = 0; i < _series.length; i++) {
      var s = _series[i];
      while (s.status == SeriesStatus.findingDriver) {
        final o = s.offer;
        if (o == null || o.status != OfferStatus.open || o.expiresAt.isAfter(now)) break;
        if (_someoneInterested(s)) {
          final ravi = demoDriverFor(s.rideType).withPhone(demoDriverPhone);
          s = s.copyWith(
            status: SeriesStatus.active,
            offer: _closed(o, OfferOutcome.assigned),
            slots: [for (final slot in s.slots) slot.withDriver(ravi)],
          );
          _series[i] = s;
          _seriesActive(s);
        } else if (o.round < maxRounds) {
          s = s.copyWith(offer: _openRound(o.round + 1, o.expiresAt));
        } else {
          s = s.copyWith(status: SeriesStatus.noDriverFound, offer: _closed(o, OfferOutcome.noFit));
        }
      }
      _series[i] = s;
    }
  }

  bool _someoneInterested(Series s) => driversInterested && !s.preferences.contains(RidePreference.womanDriver);

  @override
  Future<HomeData> home() async {
    await _wait();
    _catchUp();
    return HomeData(
      profile: _profile ?? const RiderProfile(phone: '', name: demoRiderName),
      series: [for (final s in _series.reversed) if (s.status != SeriesStatus.ended) s],
    );
  }

  // ---------------------------------------------------------------- flow R2

  /// Creates two weeks of rides, as the backend does when a driver is picked.
  void _seriesActive(Series s) {
    final today = _today;
    var day = s.startDate.isAfter(today) ? s.startDate : today;
    final last = today.add(const Duration(days: 14));
    while (!day.isAfter(last)) {
      for (final slot in s.slots) {
        if (slot.weekday != day.weekday || !slot.active) continue;
        final out = slot.direction == RideDirection.outbound;
        _addRide(s, slot, day, out ? s.pickup : s.drop, out ? s.drop : s.pickup);
      }
      day = DateTime(day.year, day.month, day.day + 1);
    }
    final after = demoRideAfter;
    if (after != null) playNextRide(at: _now().add(after), speed: rideSpeed);
  }

  void _addRide(Series s, SeriesSlot slot, DateTime day, Place pickup, Place drop) {
    final minute = slot.pickupTime;
    final pickupAt = DateTime(day.year, day.month, day.day, minute ~/ 60, minute % 60);
    if (!pickupAt.isAfter(_now())) return;
    final id = 'ride_${s.id}_${dateKey(day)}_${slot.direction.name}';
    _rides[id] = Ride(
      id: id,
      date: day,
      direction: slot.direction,
      state: RideState.scheduled,
      pickup: pickup,
      drop: drop,
      pickupAt: pickupAt,
      windowStart: pickupAt.subtract(const Duration(minutes: 5)),
      windowEnd: pickupAt.add(const Duration(minutes: 5)),
      tripMinutes: s.travelMinutes,
      fare: s.suggestedFare,
      rideType: s.rideType,
      driver: slot.driver ?? demoDriverFor(s.rideType),
      pin: (1000 + (_rides.length * 7919) % 9000).toString(),
    );
  }

  static String dateKey(DateTime d) =>
      '${d.year}${d.month.toString().padLeft(2, '0')}${d.day.toString().padLeft(2, '0')}';

  /// Makes the next scheduled ride play itself out from [at] (default now):
  /// it becomes today's ride, the driver departs at [at] and the rest
  /// follows the script. For demo mode and tests.
  void playNextRide({
    DateTime? at,
    double? speed,
    FakeRideOutcome outcome = FakeRideOutcome.completed,
    int lateMinutes = 0,
  }) {
    final next = _sorted().where((r) => r.state == RideState.scheduled).firstOrNull;
    if (next == null) return;
    final departAt = at ?? _now();
    final script = FakeRideScript(
      departAt: departAt,
      pickup: next.pickup,
      drop: next.drop,
      tripMinutes: next.tripMinutes,
      speed: speed ?? rideSpeed,
      outcome: outcome,
      lateMinutes: lateMinutes,
    );
    final pickupAt = script.pickupAt;
    // The played ride becomes today's, at the time the driver will get there.
    // Read the stored ride: the view above hides the PIN until departure.
    final pin = _rides.remove(next.id)!.pin;
    final id = 'ride_demo_${dateKey(pickupAt)}_${next.direction.name}';
    _rides[id] = Ride(
      id: id,
      date: dateOnly(pickupAt),
      direction: next.direction,
      state: RideState.scheduled,
      pickup: next.pickup,
      drop: next.drop,
      pickupAt: pickupAt,
      windowStart: pickupAt.subtract(Duration(milliseconds: (5 * 60000 / script.speed).round())),
      windowEnd: pickupAt.add(Duration(milliseconds: (5 * 60000 / script.speed).round())),
      tripMinutes: next.tripMinutes,
      fare: next.fare,
      rideType: next.rideType,
      driver: next.driver,
      pin: pin,
    );
    _scriptedRideId = id;
    _script = script;
  }

  /// The ride as it stands now: the script decides the state of a played ride.
  Ride _current(Ride r) {
    final script = _script;
    // The PIN shows only once the driver has departed, as on the backend.
    if (r.id != _scriptedRideId || script == null) return r.copyWith(clearPin: !(r.state == RideState.onTheWay || r.state == RideState.arrived));
    final now = _now();
    final state = script.stateAt(now);
    final departed = state != RideState.scheduled;
    return r.copyWith(
      state: state,
      late: script.late && departed,
      waitEndsAt: state.index >= RideState.arrived.index ? script.waitEndsAt : null,
      endedAt: state == RideState.completed ? script.endAt : null,
      // The demo driver marks the fare as received as soon as the ride ends.
      driverPayment: state == RideState.completed ? DriverPaymentMark.upi : null,
      paymentStatus: state == RideState.completed ? PaymentStatus.paid : null,
      clearPin: !(state == RideState.onTheWay || state == RideState.arrived),
    );
  }

  List<Ride> _sorted() => [for (final r in _rides.values) _current(r)]..sort((a, b) => a.pickupAt.compareTo(b.pickupAt));

  Ride _find(String id) {
    final r = _rides[id];
    if (r == null) throw const ApiException('ride_not_found', 'No such ride');
    return _current(r);
  }

  @override
  Future<List<Ride>> upcomingRides({int days = 7}) async {
    await _wait();
    _catchUp();
    final today = dateOnly(_now());
    final end = today.add(Duration(days: days));
    return [
      for (final r in _sorted())
        if (!r.date.isBefore(today) && r.date.isBefore(end)) r,
    ];
  }

  @override
  Future<Ride> ride(String id) async {
    await _wait();
    return _find(id);
  }

  @override
  Future<LiveRide> liveRide(String id) async {
    await _wait();
    final r = _find(id);
    final now = _now();
    final script = id == _scriptedRideId ? _script : null;
    final eta = script?.etaMinutes(now);
    return LiveRide(
      state: r.state,
      serverTime: now,
      driverAt: script?.driverAt(now),
      positionAt: script?.driverAt(now) == null ? null : now,
      etaMinutes: eta,
      etaAt: eta == null ? null : now.add(Duration(minutes: eta)),
      waitEndsAt: r.waitEndsAt,
      late: r.late,
      pinLocked: r.pinLocked,
    );
  }

  @override
  Future<Ride> runningLate(String id) async {
    await _wait();
    final r = _find(id);
    if (r.riderLateUsed) throw const ApiException('already_used', 'You can only do this once per ride');
    if (!(r.state == RideState.scheduled || r.state.isLive) || r.state == RideState.inRide) {
      throw const ApiException('invalid_transition', 'This ride has already started');
    }
    if (id == _scriptedRideId) _script?.riderLate = true;
    _rides[id] = _rides[id]!.copyWith(riderLateUsed: true);
    return _find(id);
  }

  @override
  Future<PaymentStatus> markPayment(String id, PaymentMethod method) async {
    await _wait();
    final r = _find(id);
    if (r.state != RideState.completed) throw const ApiException('not_completed', 'Mark payment once the ride has ended');
    final status = r.driverPayment == null ? PaymentStatus.paidByRider : PaymentStatus.paid;
    _rides[id] = _rides[id]!.copyWith(riderPayment: method, paymentStatus: status);
    return status;
  }

  @override
  Future<void> rateRide(String id, RideRating rating) async {
    await _wait();
    final r = _find(id);
    if (r.state != RideState.completed) throw const ApiException('not_completed', 'Rate the ride once it has ended');
    _rides[id] = _rides[id]!.copyWith(rating: rating);
  }

  @override
  Future<List<Ride>> unsettledRides() async {
    await _wait();
    return [
      for (final r in _sorted().reversed)
        if (r.state == RideState.completed && r.riderPayment == null && r.rating == null) r,
    ];
  }

  @override
  Future<void> registerDevice(String pushToken, String platform) async {
    await _wait();
    pushTokens.add(pushToken);
  }
}

/// The demo rider, as in the backend's spec seed.
const demoRiderName = 'Priya Sharma';

/// The demo driver's number, shared once he is picked.
const demoDriverPhone = '9000000201';

const demoPlaces = <Place>[
  Place(id: 'home', title: 'Home', detail: '14th Main, HSR Layout', lat: 12.9121, lng: 77.6446),
  Place(id: 'etv', title: 'Embassy Tech Village', detail: 'Gate 3', lat: 12.9406, lng: 77.6964),
  Place(id: 'koramangala', title: 'Koramangala', detail: 'Koramangala, Bengaluru', lat: 12.9352, lng: 77.6245),
  Place(id: 'whitefield', title: 'Whitefield', detail: 'Whitefield, Bengaluru', lat: 12.9698, lng: 77.7178),
  Place(id: 'manyata', title: 'Manyata Tech Park', detail: 'Hebbal', lat: 13.0475, lng: 77.6203),
  Place(id: 'itpl', title: 'ITPL', detail: 'Whitefield', lat: 12.9866, lng: 77.7378),
  Place(id: 'forum', title: 'Forum Mall', detail: 'Koramangala', lat: 12.9346, lng: 77.6113),
  Place(id: 'indiranagar', title: 'Indiranagar Metro', detail: '100 Feet Road', lat: 12.9784, lng: 77.6408),
  Place(id: 'airport', title: 'Kempegowda Airport', detail: 'Terminal 2', lat: 13.1989, lng: 77.7068),
];

/// Ravi, the demo driver who takes every request, in a vehicle of [type].
Driver demoDriverFor(RideType type) {
  final (vehicle, plate) = switch (type) {
    RideType.bike => ('Black Splendor · helmet', 'KA 51 EX 2210'),
    RideType.auto => ('Green auto · 3 seats', 'KA 05 AB 1234'),
    RideType.cabHatchback => ('Grey Swift · AC · 4 seats', 'KA 03 MB 5521'),
    RideType.cabSedan => ('White Dzire · AC · 4 seats', 'KA 01 AB 1234'),
    RideType.cabSuv => ('White Ertiga · AC · 6 seats', 'KA 01 MP 8890'),
  };
  return Driver(
    id: 'd_ravi',
    name: 'Ravi Kumar',
    vehicle: vehicle,
    plate: plate,
    rating: 4.9,
    upiId: 'ravi.kumar@okbank',
    rideType: type,
  );
}
