import 'api.dart';
import 'fares.dart';
import 'models.dart';

/// In-memory backend used in demo mode and in every test.
///
/// Behaviour is deterministic so tests and manual testing see the same thing:
/// * The OTP is always [demoOtp].
/// * Choosing the "Woman driver" preference finds no driver, so the
///   "No driver yet" branch can be tested.
/// * Any other request is matched with three drivers of the chosen type.
class FakeRiderApi implements RiderApi {
  FakeRiderApi({
    this.latency = Duration.zero,
    Map<BookingFlow, RideTypeConfig>? config,
  }) : config = config ?? defaultRideTypeConfig;

  static const demoOtp = '123456';

  final Duration latency;

  /// Mirrors the backend ride-type config: allowed types per booking flow.
  final Map<BookingFlow, RideTypeConfig> config;

  RiderProfile? _profile;
  Commute? _commute;

  /// The confirmed commute, for tests to inspect.
  Commute? get commute => _commute;
  final List<CommuteRequest> waitlist = [];
  int _ids = 0;

  Future<void> _wait() =>
      latency == Duration.zero ? Future<void>.value() : Future<void>.delayed(latency);

  @override
  Future<void> sendOtp(String phone) async {
    await _wait();
    if (!RegExp(r'^[6-9]\d{9}$').hasMatch(phone)) {
      throw const ApiException('invalid_phone', 'Enter a 10-digit mobile number');
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
    final drivers = _driversByType[request.rideType] ?? const <Driver>[];
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
    return _commute = Commute(
      id: 'cm_$_ids',
      request: request,
      driver: match.driver,
      fare: match.fare,
      usualPayment: usualPayment,
      // The demo driver accepts straight away.
      status: CommuteStatus.active,
    );
  }

  @override
  Future<void> joinWaitlist(CommuteRequest request) async {
    await _wait();
    waitlist.add(request);
  }

  @override
  Future<HomeData> home() async {
    await _wait();
    return HomeData(
      profile: _profile ?? const RiderProfile(phone: ''),
      commute: _commute,
      recentRides: const [],
    );
  }

  static String _slotWord(int minutes) => minutes < 720 ? 'that time in the morning' : 'that time';
}

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
