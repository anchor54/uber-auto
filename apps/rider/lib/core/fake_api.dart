import 'api.dart';
import 'fake_ride_script.dart';
import 'fares.dart';
import 'models.dart';
import 'schedule.dart';

/// In-memory backend used in demo mode and in every test.
///
/// Behaviour is deterministic so tests and manual testing see the same thing:
/// * The OTP is always [demoOtp].
/// * Choosing the "Woman driver" preference finds no driver, so the
///   "No driver yet" branch can be tested.
/// * Any other request is matched with three drivers of the chosen type,
///   leaving out any driver who already turned this rider down.
/// * The driver answers as [driverResponse] says (flow D2): straight away by
///   default, so tests see an active commute.
/// * An active commute has a ride per leg for the next two weeks (flow R2).
///   [playNextRide] makes the next one play itself out; in demo mode
///   [demoRideAfter] does that on its own, sped up by [rideSpeed].
class FakeRiderApi implements RiderApi {
  FakeRiderApi({
    this.latency = Duration.zero,
    Map<BookingFlow, RideTypeConfig>? config,
    this.driverResponse = DemoDriverResponse.acceptNow,
    this.driverResponseDelay = const Duration(seconds: 8),
    this.demoRideAfter,
    this.rideSpeed = 1,
    DateTime Function()? now,
  })  : config = config ?? defaultRideTypeConfig,
        _explicitNow = now != null,
        _now = now ?? DateTime.now;

  static const demoOtp = '123456';

  final Duration latency;

  /// Mirrors the backend ride-type config: allowed types per booking flow.
  final Map<BookingFlow, RideTypeConfig> config;

  /// How the demo driver answers a confirmed commute.
  DemoDriverResponse driverResponse;

  /// For the delayed responses: how long the driver takes.
  final Duration driverResponseDelay;

  /// Demo mode: once the commute is active, the next ride starts playing after this long.
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

  RiderProfile? _profile;
  Commute? _commute;
  DateTime? _confirmedAt;
  final Set<String> _declinedBy = {};

  /// The confirmed commute, for tests to inspect.
  Commute? get commute => _commute;
  final List<CommuteRequest> waitlist = [];
  int _ids = 0;

  Future<void> _wait() =>
      latency == Duration.zero ? Future<void>.value() : Future<void>.delayed(latency);

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
    return _profile = RiderProfile(phone: phone);
  }

  @override
  Future<List<Place>> searchPlaces(String query) async {
    await _wait();
    final q = query.trim().toLowerCase();
    if (q.isEmpty) return demoPlaces;
    return demoPlaces
        .where((p) => p.title.toLowerCase().contains(q) || p.detail.toLowerCase().contains(q))
        .toList();
  }

  @override
  Future<TripEstimate> estimateTrip(Place pickup, Place drop) async {
    await _wait();
    return estimateRoadTrip(pickup, drop);
  }

  @override
  Future<BookingOptions> bookingOptions({
    required BookingFlow flow,
    required Place pickup,
    required Place drop,
    required int pickupTime,
  }) async {
    await _wait();
    final trip = estimateRoadTrip(pickup, drop);
    final cfg = config[flow] ?? const RideTypeConfig(allowed: [], defaultType: null);
    return BookingOptions(
      flow: flow,
      defaultType: cfg.defaultType,
      options: [
        for (final type in cfg.allowed)
          RideTypeOption(
            type: type,
            fare: suggestedFare(type, trip.distanceKm),
            driversOnRoute: _driversOnRoute[type] ?? 0,
          ),
      ],
    );
  }

  @override
  Future<List<DriverMatch>> findDrivers(CommuteRequest request) async {
    await _wait();
    if (request.preferences.contains(RidePreference.womanDriver)) return const [];
    final trip = estimateRoadTrip(request.pickup, request.drop);
    final fare = suggestedFare(request.rideType, trip.distanceKm);
    final drivers = [
      for (final d in _driversByType[request.rideType] ?? const <Driver>[])
        if (!_declinedBy.contains(d.id)) d,
    ];
    final area = request.pickup.detail.isNotEmpty ? request.pickup.detail.split(', ').last : request.pickup.title;
    return [
      for (final (i, d) in drivers.indexed)
        DriverMatch(
          driver: d,
          fare: fare,
          trip: trip,
          reasons: [
            request.returnTrip ? 'Same driver every day, both directions' : 'Same driver on every ride',
            i == 0
                ? 'Already drives near $area at this time'
                : 'Has a free slot at ${_slotWord(request.pickupTime)} on your days',
            'If ${d.firstName} can\'t make it, a backup driver is confirmed the night before',
          ],
        ),
    ];
  }

  @override
  Future<Commute> confirmCommute({
    required CommuteRequest request,
    required DriverMatch match,
    required PaymentMethod usualPayment,
  }) async {
    await _wait();
    _ids++;
    final now = _now();
    request = _resolve(request, now);
    final acceptNow = driverResponse == DemoDriverResponse.acceptNow;
    _confirmedAt = now;
    _commute = Commute(
      id: 'cm_$_ids',
      request: request,
      driver: acceptNow ? match.driver.withPhone(_demoDriverPhone) : match.driver,
      fare: match.fare,
      usualPayment: usualPayment,
      status: acceptNow ? CommuteStatus.active : CommuteStatus.awaitingDriver,
      respondBy: acceptNow ? null : now.add(const Duration(hours: 12)),
      trip: estimateRoadTrip(request.pickup, request.drop),
    );
    if (acceptNow) _commuteActive();
    return _commute!;
  }

  /// Like the backend, the server side decides the pickup time and the start
  /// date; what the app worked out for display is not trusted.
  CommuteRequest _resolve(CommuteRequest r, DateTime now) {
    final trip = estimateRoadTrip(r.pickup, r.drop);
    return CommuteRequest(
      pickup: r.pickup,
      drop: r.drop,
      repeat: r.repeat,
      weekdays: r.weekdays,
      timing: r.timing,
      time: r.time,
      pickupTime: r.timing == TimingMode.pickupAt ? r.time : pickupForArrival(r.time, trip.minutes),
      returnTrip: r.returnTrip,
      returnTime: r.returnTime,
      startDate: firstRideDate(dateOnly(now), r.weekdays),
      rideType: r.rideType,
      preferences: r.preferences,
    );
  }

  /// The driver answers now, whatever [driverResponse] says. For tests.
  void driverAccepts() => _answer(DemoDriverResponse.acceptLater);
  void driverDeclines() => _answer(DemoDriverResponse.decline);
  void driverNeverAnswers() => _answer(DemoDriverResponse.noAnswer);

  void _answer(DemoDriverResponse response) {
    final c = _commute;
    if (c == null || c.status != CommuteStatus.awaitingDriver) return;
    switch (response) {
      case DemoDriverResponse.acceptNow:
      case DemoDriverResponse.acceptLater:
        _commute = Commute(
          id: c.id,
          request: c.request,
          driver: c.driver.withPhone(_demoDriverPhone),
          fare: c.fare,
          usualPayment: c.usualPayment,
          status: CommuteStatus.active,
          trip: c.trip,
        );
        _commuteActive();
      case DemoDriverResponse.decline:
        _declinedBy.add(c.driver.id);
        _commute = c.withStatus(CommuteStatus.declined, reason: DeclineReason.declined);
      case DemoDriverResponse.noAnswer:
        _declinedBy.add(c.driver.id);
        _commute = c.withStatus(CommuteStatus.declined, reason: DeclineReason.noAnswer);
    }
  }

  /// Plays out a delayed driver response once its time has come.
  void _catchUp() {
    final at = _confirmedAt;
    if (at == null || driverResponse == DemoDriverResponse.acceptNow) return;
    if (_now().difference(at) >= driverResponseDelay) _answer(driverResponse);
  }

  @override
  Future<void> joinWaitlist(CommuteRequest request) async {
    await _wait();
    waitlist.add(request);
  }

  @override
  Future<HomeData> home() async {
    await _wait();
    _catchUp();
    return HomeData(
      profile: _profile ?? const RiderProfile(phone: ''),
      commute: _commute,
      recentRides: const [],
    );
  }

  static String _slotWord(int minutes) => minutes < 720 ? 'that time in the morning' : 'that time';

  // ---------------------------------------------------------------- flow R2

  /// Creates two weeks of rides, as the backend does when a commute becomes active.
  void _commuteActive() {
    final c = _commute!;
    final r = c.request;
    final today = dateOnly(_now());
    var day = r.startDate.isAfter(today) ? r.startDate : today;
    final last = today.add(const Duration(days: 14));
    while (!day.isAfter(last)) {
      if (r.weekdays.contains(day.weekday)) {
        _addRide(c, day, RideDirection.outbound, r.pickupTime, r.pickup, r.drop);
        if (r.returnTrip) _addRide(c, day, RideDirection.returnLeg, r.returnTime, r.drop, r.pickup);
      }
      day = DateTime(day.year, day.month, day.day + 1);
    }
    final after = demoRideAfter;
    if (after != null) playNextRide(at: _now().add(after), speed: rideSpeed);
  }

  void _addRide(Commute c, DateTime day, RideDirection dir, int minute, Place pickup, Place drop) {
    final pickupAt = DateTime(day.year, day.month, day.day, minute ~/ 60, minute % 60);
    if (!pickupAt.isAfter(_now())) return;
    final id = 'ride_${dateKey(day)}_${dir.name}';
    _rides[id] = Ride(
      id: id,
      date: day,
      direction: dir,
      state: RideState.scheduled,
      pickup: pickup,
      drop: drop,
      pickupAt: pickupAt,
      windowStart: pickupAt.subtract(const Duration(minutes: 5)),
      windowEnd: pickupAt.add(const Duration(minutes: 5)),
      tripMinutes: estimateRoadTrip(pickup, drop).minutes,
      fare: c.fare,
      rideType: c.request.rideType,
      driver: c.driver,
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

/// How the demo driver answers a confirmed commute (flow D2).
enum DemoDriverResponse {
  /// Accepts at once: the commute is active straight after confirming.
  acceptNow,

  /// Shows "Waiting for …", then accepts after the delay.
  acceptLater,

  /// Shows "Waiting for …", then declines after the delay.
  decline,

  /// Shows "Waiting for …", then the request lapses after the delay.
  noAnswer,
}

const _demoDriverPhone = '9000000010';

class RideTypeConfig {
  const RideTypeConfig({required this.allowed, required this.defaultType});
  final List<RideType> allowed;
  final RideType? defaultType;
}

const defaultRideTypeConfig = <BookingFlow, RideTypeConfig>{
  BookingFlow.oneTime: RideTypeConfig(allowed: RideType.values, defaultType: RideType.auto),
  BookingFlow.recurringWeekday: RideTypeConfig(allowed: RideType.values, defaultType: RideType.cabSedan),
  BookingFlow.recurringWeekly: RideTypeConfig(allowed: RideType.values, defaultType: RideType.cabSedan),
};

const _driversOnRoute = <RideType, int>{
  RideType.bike: 3,
  RideType.auto: 4,
  RideType.cabHatchback: 2,
  RideType.cabSedan: 5,
  RideType.cabSuv: 1,
};

const demoPlaces = <Place>[
  Place(id: 'home', title: 'Home', detail: '14th Main, HSR Layout', lat: 12.9121, lng: 77.6446),
  Place(id: 'etv', title: 'Embassy Tech Village', detail: 'Gate 3', lat: 12.9406, lng: 77.6964),
  Place(id: 'manyata', title: 'Manyata Tech Park', detail: 'Hebbal', lat: 13.0475, lng: 77.6203),
  Place(id: 'itpl', title: 'ITPL', detail: 'Whitefield', lat: 12.9866, lng: 77.7378),
  Place(id: 'forum', title: 'Forum Mall', detail: 'Koramangala', lat: 12.9346, lng: 77.6113),
  Place(id: 'indiranagar', title: 'Indiranagar Metro', detail: '100 Feet Road', lat: 12.9784, lng: 77.6408),
  Place(id: 'airport', title: 'Kempegowda Airport', detail: 'Terminal 2', lat: 13.1989, lng: 77.7068),
];

const _driversByType = <RideType, List<Driver>>{
  RideType.bike: [
    Driver(id: 'd_arjun', name: 'Arjun Rao', vehicle: 'Black Splendor · helmet', plate: 'KA 51 EX 2210', rating: 4.8, upiId: 'arjun.rao@okhdfc'),
    Driver(id: 'd_vinay', name: 'Vinay S', vehicle: 'Red Pulsar · helmet', plate: 'KA 03 HY 7781', rating: 4.7, upiId: 'vinay.s@ybl'),
    Driver(id: 'd_kiran', name: 'Kiran Gowda', vehicle: 'Blue Activa · helmet', plate: 'KA 05 KP 9012', rating: 4.6, upiId: 'kiran.g@okaxis'),
  ],
  RideType.auto: [
    Driver(id: 'd_manju', name: 'Manjunath B', vehicle: 'Green auto · 3 seats', plate: 'KA 01 AC 4420', rating: 4.8, upiId: 'manju.b@ybl'),
    Driver(id: 'd_raju', name: 'Raju K', vehicle: 'Green auto · 3 seats', plate: 'KA 05 AD 1189', rating: 4.7, upiId: 'raju.k@okicici'),
    Driver(id: 'd_salim', name: 'Salim Pasha', vehicle: 'Green auto · 3 seats', plate: 'KA 02 AB 7765', rating: 4.6, upiId: 'salim.p@ybl'),
  ],
  RideType.cabHatchback: [
    Driver(id: 'd_naveen', name: 'Naveen P', vehicle: 'Grey Swift · AC · 4 seats', plate: 'KA 03 MB 5521', rating: 4.8, upiId: 'naveen.p@okhdfc'),
    Driver(id: 'd_sunil', name: 'Sunil R', vehicle: 'White WagonR · AC · 4 seats', plate: 'KA 04 MC 3390', rating: 4.7, upiId: 'sunil.r@ybl'),
    Driver(id: 'd_mohan', name: 'Mohan D', vehicle: 'Red i20 · AC · 4 seats', plate: 'KA 51 MD 1102', rating: 4.6, upiId: 'mohan.d@okaxis'),
  ],
  RideType.cabSedan: [
    Driver(id: 'd_ravi', name: 'Ravi Kumar', vehicle: 'White Dzire · AC · 4 seats', plate: 'KA 01 AB 1234', rating: 4.9, upiId: 'ravi.kumar@okbank'),
    Driver(id: 'd_suresh', name: 'Suresh M', vehicle: 'Silver Etios · AC · 4 seats', plate: 'KA 05 MN 4410', rating: 4.8, upiId: 'suresh.m@ybl'),
    Driver(id: 'd_imran', name: 'Imran K', vehicle: 'Silver Honda City · AC · 4 seats', plate: 'KA 05 MK 2290', rating: 4.8, upiId: 'imran.k@okaxis'),
  ],
  RideType.cabSuv: [
    Driver(id: 'd_prakash', name: 'Prakash N', vehicle: 'White Ertiga · AC · 6 seats', plate: 'KA 01 MP 8890', rating: 4.8, upiId: 'prakash.n@okhdfc'),
    Driver(id: 'd_joseph', name: 'Joseph A', vehicle: 'Grey Innova · AC · 7 seats', plate: 'KA 02 MJ 3345', rating: 4.7, upiId: 'joseph.a@ybl'),
    Driver(id: 'd_ganesh', name: 'Ganesh T', vehicle: 'White XL6 · AC · 6 seats', plate: 'KA 53 MG 6610', rating: 4.6, upiId: 'ganesh.t@okaxis'),
  ],
};
