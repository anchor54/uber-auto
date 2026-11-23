/// Domain models shared by the rider app, its fake backend and tests.
library;

enum RideType { bike, auto, cabHatchback, cabSedan, cabSuv }

extension RideTypeInfo on RideType {
  /// Key used by the backend (`vehicle_type`, ride-type config).
  String get configKey => switch (this) {
        RideType.bike => 'bike',
        RideType.auto => 'auto',
        RideType.cabHatchback => 'cab_hatchback',
        RideType.cabSedan => 'cab_sedan',
        RideType.cabSuv => 'cab_suv',
      };

  /// "Cab · Sedan": the type with its group, as on R1.4 and Home.
  String get label => switch (this) {
        RideType.bike => 'Bike',
        RideType.auto => 'Auto',
        RideType.cabHatchback => 'Cab · Hatchback',
        RideType.cabSedan => 'Cab · Sedan',
        RideType.cabSuv => 'Cab · SUV',
      };

  /// "Sedan": the row title on R1.3, and "{ride type} drivers" in sentences.
  String get shortLabel => switch (this) {
        RideType.bike => 'Bike',
        RideType.auto => 'Auto',
        RideType.cabHatchback => 'Hatchback',
        RideType.cabSedan => 'Sedan',
        RideType.cabSuv => 'SUV',
      };

  /// Lower-case noun used in sentences, e.g. "every sedan driver".
  String get noun => switch (this) {
        RideType.bike => 'bike',
        RideType.auto => 'auto',
        RideType.cabHatchback => 'hatchback',
        RideType.cabSedan => 'sedan',
        RideType.cabSuv => 'SUV',
      };

  String get description => switch (this) {
        RideType.bike => '1 rider · helmet provided',
        RideType.auto => 'Up to 3 riders · open sides',
        RideType.cabHatchback => '4 seats · AC',
        RideType.cabSedan => '4 seats · AC · laptop bag fits',
        RideType.cabSuv => '6 seats · AC · big boot',
      };

  bool get isCab => this == RideType.cabHatchback || this == RideType.cabSedan || this == RideType.cabSuv;

  static RideType fromKey(String key) => RideType.values.firstWhere((t) => t.configKey == key);

  /// Null for a type this app version doesn't know.
  static RideType? tryKey(String? key) {
    for (final t in RideType.values) {
      if (t.configKey == key) return t;
    }
    return null;
  }
}

/// The only city until multi-city sign-up exists (the backend's `city`).
const defaultCity = 'blr';

/// "Bengaluru" for the backend's city code.
String cityName(String code) => switch (code) {
      'blr' => 'Bengaluru',
      _ => code,
    };

/// R1.2's two-way choice. Only the app uses it: the backend gets one slot per day.
enum CommuteRepeat { everyWeekday, chosenDays }

enum TimingMode { pickupAt, arriveBy }

enum PaymentMethod { upi, cash }

extension PaymentMethodInfo on PaymentMethod {
  String get label => switch (this) {
        PaymentMethod.upi => 'UPI',
        PaymentMethod.cash => 'Cash',
      };
}

enum RidePreference { acOn, quiet, womanDriver }

extension RidePreferenceInfo on RidePreference {
  String get label => switch (this) {
        RidePreference.acOn => 'AC on',
        RidePreference.quiet => 'Quiet ride',
        RidePreference.womanDriver => 'Woman driver',
      };
}

class Place {
  const Place({
    required this.id,
    required this.title,
    this.detail = '',
    required this.lat,
    required this.lng,
  });

  final String id;

  /// Short name shown in summaries, e.g. "Home" or "Embassy Tech Village".
  /// Sent to the backend as the place's `label`.
  final String title;

  /// Extra detail, e.g. "14th Main, HSR Layout" or "Gate 3".
  final String detail;
  final double lat;
  final double lng;

  String get display {
    if (detail.isEmpty) return title;
    return title == 'Home' || title == 'Office' ? '$title · $detail' : '$title, $detail';
  }

  @override
  bool operator ==(Object other) => other is Place && other.id == id;

  @override
  int get hashCode => id.hashCode;
}

class TripEstimate {
  const TripEstimate({required this.distanceKm, required this.minutes, this.polyline});
  final double distanceKm;
  final int minutes;

  /// The road route (encoded polyline) when the maps provider gives one.
  final String? polyline;
}

/// One row on R1.3: a ride type the config allows for regular rides in the
/// rider's city, with its suggested fare per ride for this route.
class RideTypeOption {
  const RideTypeOption({required this.type, required this.fare});

  final RideType type;

  /// Suggested fare per ride, in whole rupees.
  final int fare;
}

/// One leg of a series on one weekday, as the rider asks for it.
class SlotRequest {
  const SlotRequest({
    required this.weekday,
    this.direction = RideDirection.outbound,
    this.timing = TimingMode.pickupAt,
    required this.time,
  });

  /// ISO weekday, 1 = Monday … 7 = Sunday.
  final int weekday;
  final RideDirection direction;
  final TimingMode timing;

  /// Minutes after midnight: the pickup time, or the arrive-by time.
  final int time;
}

/// Everything the rider chose while setting up a regular ride: the body of
/// series.preview, series.create and series.retry.
class CommuteRequest {
  const CommuteRequest({
    required this.pickup,
    required this.drop,
    required this.slots,
    required this.rideType,
    this.preferences = const {},
    this.usualPayment = PaymentMethod.upi,
    this.startDate,
  });

  final Place pickup;
  final Place drop;

  /// One per weekday and direction ("Same time every day" sends the same
  /// time for every day).
  final List<SlotRequest> slots;
  final RideType rideType;
  final Set<RidePreference> preferences;
  final PaymentMethod usualPayment;

  /// The earliest start wanted, or null: the first chosen weekday from tomorrow.
  final DateTime? startDate;

  /// ISO weekdays with an outbound slot.
  Set<int> get weekdays => {for (final s in slots) if (s.direction == RideDirection.outbound) s.weekday};

  bool get returnTrip => slots.any((s) => s.direction == RideDirection.returnLeg);
}

/// What the backend works out from a request (series.preview); nothing is saved.
class SeriesPreview {
  const SeriesPreview({
    required this.rideType,
    required this.distanceKm,
    required this.travelMinutes,
    required this.startDate,
    required this.suggestedFare,
    required this.ridesPerMonth,
    required this.slots,
  });

  final RideType rideType;
  final double distanceKm;
  final int travelMinutes;
  final DateTime startDate;
  final int suggestedFare;
  final int ridesPerMonth;
  final List<SeriesSlot> slots;
}

/// Where a series is. Only the server moves it.
enum SeriesStatus { findingDriver, active, paused, noDriverFound, ended }

/// A round of offers to drivers: open, closing (the window passed, the pick
/// is running) or closed.
enum OfferStatus { open, closing, closed }

enum OfferOutcome { assigned, noFit, cancelled }

/// The latest round of a series' request. The rider app shows no rounds;
/// they are here for tests and support.
class SeriesOffer {
  const SeriesOffer({
    required this.id,
    required this.round,
    required this.status,
    required this.openedAt,
    required this.expiresAt,
    this.outcome,
  });

  final String id;
  final int round;
  final OfferStatus status;
  final DateTime openedAt;
  final DateTime expiresAt;
  final OfferOutcome? outcome;
}

/// One weekday and direction of a series, with its driver once picked.
class SeriesSlot {
  const SeriesSlot({
    this.id,
    required this.weekday,
    required this.direction,
    required this.timing,
    this.arriveByTime,
    required this.pickupTime,
    this.estimatedDropTime,
    this.driver,
    this.active = true,
  });

  final String? id;

  /// ISO weekday, 1 = Monday … 7 = Sunday.
  final int weekday;
  final RideDirection direction;
  final TimingMode timing;

  /// Minutes after midnight, for an arrive-by slot.
  final int? arriveByTime;

  /// Minutes after midnight. For arrive-by, worked out by the server.
  final int pickupTime;
  final int? estimatedDropTime;
  final Driver? driver;
  final bool active;

  /// The time the rider entered: arrive-by time, or the pickup time.
  int get enteredTime => timing == TimingMode.arriveBy ? (arriveByTime ?? pickupTime) : pickupTime;

  SeriesSlot withDriver(Driver? d) => SeriesSlot(
        id: id,
        weekday: weekday,
        direction: direction,
        timing: timing,
        arriveByTime: arriveByTime,
        pickupTime: pickupTime,
        estimatedDropTime: estimatedDropTime,
        driver: d,
        active: active,
      );
}

/// A regular ride: the rider's request and, once picked, its drivers.
class Series {
  const Series({
    required this.id,
    required this.status,
    required this.rideType,
    required this.preferences,
    required this.pickup,
    required this.drop,
    required this.startDate,
    required this.distanceKm,
    required this.travelMinutes,
    required this.suggestedFare,
    required this.usualPayment,
    required this.requestedAt,
    required this.slots,
    this.city = 'blr',
    this.version = 1,
    this.offer,
  });

  final String id;
  final SeriesStatus status;
  final String city;
  final RideType rideType;
  final Set<RidePreference> preferences;
  final Place pickup;
  final Place drop;
  final DateTime startDate;
  final double distanceKm;
  final int travelMinutes;
  final int suggestedFare;
  final PaymentMethod usualPayment;
  final DateTime requestedAt;
  final int version;
  final List<SeriesSlot> slots;

  /// The latest round, whatever its status.
  final SeriesOffer? offer;

  List<SeriesSlot> get outbound => [for (final s in slots) if (s.direction == RideDirection.outbound) s]
    ..sort((a, b) => a.weekday.compareTo(b.weekday));

  List<SeriesSlot> get returns => [for (final s in slots) if (s.direction == RideDirection.returnLeg) s]
    ..sort((a, b) => a.weekday.compareTo(b.weekday));

  /// ISO weekdays with an outbound slot.
  Set<int> get weekdays => {for (final s in outbound) s.weekday};

  bool get returnTrip => slots.any((s) => s.direction == RideDirection.returnLeg);

  SeriesSlot? slotFor(int weekday, RideDirection direction) {
    for (final s in slots) {
      if (s.weekday == weekday && s.direction == direction) return s;
    }
    return null;
  }

  /// The request that made this series, e.g. to change it and try again.
  CommuteRequest toRequest() => CommuteRequest(
        pickup: pickup,
        drop: drop,
        slots: [
          for (final s in slots) SlotRequest(weekday: s.weekday, direction: s.direction, timing: s.timing, time: s.enteredTime),
        ],
        rideType: rideType,
        preferences: {...preferences},
        usualPayment: usualPayment,
      );

  Series copyWith({
    SeriesStatus? status,
    List<SeriesSlot>? slots,
    SeriesOffer? offer,
    bool clearOffer = false,
    DateTime? startDate,
    RideType? rideType,
    Set<RidePreference>? preferences,
    int? suggestedFare,
    int? travelMinutes,
    double? distanceKm,
    DateTime? requestedAt,
    PaymentMethod? usualPayment,
  }) =>
      Series(
        id: id,
        status: status ?? this.status,
        city: city,
        rideType: rideType ?? this.rideType,
        preferences: preferences ?? this.preferences,
        pickup: pickup,
        drop: drop,
        startDate: startDate ?? this.startDate,
        distanceKm: distanceKm ?? this.distanceKm,
        travelMinutes: travelMinutes ?? this.travelMinutes,
        suggestedFare: suggestedFare ?? this.suggestedFare,
        usualPayment: usualPayment ?? this.usualPayment,
        requestedAt: requestedAt ?? this.requestedAt,
        version: version + 1,
        slots: slots ?? this.slots,
        offer: clearOffer ? null : (offer ?? this.offer),
      );
}

class Driver {
  const Driver({
    required this.id,
    required this.name,
    required this.vehicle,
    required this.plate,
    required this.rating,
    this.upiId = '',
    this.phone,
    this.rideType,
  });

  final String id;
  final String name;

  /// For the rider's Call button once a ride is under way. Null when the
  /// backend doesn't share it.
  final String? phone;

  /// e.g. "Green auto · 3 seats" (the backend's `vehicle_desc`).
  final String vehicle;

  /// e.g. "KA 05 AB 1234" (the backend's `vehicle_number`).
  final String plate;
  final double rating;
  final String upiId;
  final RideType? rideType;

  String get firstName => name.split(' ').first;

  String get initials {
    final parts = name.split(' ').where((p) => p.isNotEmpty).toList();
    if (parts.isEmpty) return '?';
    if (parts.length == 1) return parts.first.substring(0, 1).toUpperCase();
    return (parts.first.substring(0, 1) + parts.last.substring(0, 1)).toUpperCase();
  }

  /// "Green auto" from "Green auto · 3 seats".
  String get car => vehicle.split(' · ').first;

  Driver withPhone(String? value) =>
      Driver(id: id, name: name, vehicle: vehicle, plate: plate, rating: rating, upiId: upiId, phone: value, rideType: rideType);
}

class RiderProfile {
  const RiderProfile({required this.phone, this.name});
  final String phone;
  final String? name;

  /// "Priya" from "Priya Sharma"; null when the name isn't known.
  String? get firstName {
    final n = name?.trim();
    if (n == null || n.isEmpty) return null;
    return n.split(' ').first;
  }
}

class HomeData {
  const HomeData({required this.profile, required this.series});
  final RiderProfile profile;

  /// The rider's series that aren't ended, newest first.
  final List<Series> series;
}

class ApiException implements Exception {
  const ApiException(this.code, this.message, {this.state});
  final String code;
  final String message;

  /// The object as it is now (e.g. the series after a 409), when the server sent it.
  final Object? state;

  @override
  String toString() => 'ApiException($code): $message';
}

// ---------------------------------------------------------------- flow R2

/// Where a day's ride is. Only the server moves it.
enum RideState { scheduled, onTheWay, arrived, inRide, completed, noShow, skipped, cancelled }

extension RideStateInfo on RideState {
  /// The driver is on the way, waiting, or driving: the ride screens are live.
  bool get isLive => this == RideState.onTheWay || this == RideState.arrived || this == RideState.inRide;

  /// Nothing more will happen to this ride.
  bool get isOver => !isLive && this != RideState.scheduled;
}

enum RideDirection { outbound, returnLeg }

/// How the driver says the fare was paid.
enum DriverPaymentMark { upi, cash, notPaid }

/// Worked out by the server from both marks.
enum PaymentStatus { paid, paidByRider, unpaid, disputed, unmarked }

class RideRating {
  const RideRating({required this.stars, this.tags = const [], this.note, this.dontMatch = false});
  final int stars;
  final List<String> tags;
  final String? note;

  /// "Don't match me with this driver again."
  final bool dontMatch;
}

/// One leg of a commute on one day.
class Ride {
  const Ride({
    required this.id,
    required this.date,
    required this.direction,
    required this.state,
    required this.pickup,
    required this.drop,
    required this.pickupAt,
    required this.windowStart,
    required this.windowEnd,
    required this.tripMinutes,
    required this.fare,
    required this.rideType,
    required this.driver,
    this.pin,
    this.pinLocked = false,
    this.waitEndsAt,
    this.riderLateUsed = false,
    this.late = false,
    this.riderPayment,
    this.driverPayment,
    this.paymentStatus = PaymentStatus.unmarked,
    this.rating,
    this.endedAt,
    this.route,
  });

  final String id;
  final DateTime date;

  /// The road route of this leg (encoded polyline), or null: draw a straight line.
  final String? route;
  final RideDirection direction;
  final RideState state;
  final Place pickup;
  final Place drop;
  final DateTime pickupAt;
  final DateTime windowStart;
  final DateTime windowEnd;
  final int tripMinutes;
  final int fare;
  final RideType rideType;
  final Driver driver;

  /// The 4-digit code the rider tells the driver. Shown once the driver departs.
  final String? pin;

  /// Three wrong PINs: the ride can't start without support.
  final bool pinLocked;
  final DateTime? waitEndsAt;
  final bool riderLateUsed;

  /// The system flagged the driver as running late.
  final bool late;
  final PaymentMethod? riderPayment;
  final DriverPaymentMark? driverPayment;
  final PaymentStatus paymentStatus;
  final RideRating? rating;
  final DateTime? endedAt;

  Ride copyWith({
    RideState? state,
    String? pin,
    bool? pinLocked,
    DateTime? waitEndsAt,
    bool? riderLateUsed,
    bool? late,
    PaymentMethod? riderPayment,
    DriverPaymentMark? driverPayment,
    PaymentStatus? paymentStatus,
    RideRating? rating,
    DateTime? endedAt,
    bool clearPin = false,
  }) =>
      Ride(
        id: id,
        date: date,
        direction: direction,
        state: state ?? this.state,
        pickup: pickup,
        drop: drop,
        pickupAt: pickupAt,
        windowStart: windowStart,
        windowEnd: windowEnd,
        tripMinutes: tripMinutes,
        fare: fare,
        rideType: rideType,
        driver: driver,
        pin: clearPin ? null : (pin ?? this.pin),
        pinLocked: pinLocked ?? this.pinLocked,
        waitEndsAt: waitEndsAt ?? this.waitEndsAt,
        riderLateUsed: riderLateUsed ?? this.riderLateUsed,
        late: late ?? this.late,
        riderPayment: riderPayment ?? this.riderPayment,
        driverPayment: driverPayment ?? this.driverPayment,
        paymentStatus: paymentStatus ?? this.paymentStatus,
        rating: rating ?? this.rating,
        endedAt: endedAt ?? this.endedAt,
        route: route,
      );
}

class GeoPoint {
  const GeoPoint(this.lat, this.lng);
  final double lat;
  final double lng;
}

/// What the rider app polls every 5 seconds while a ride is live.
class LiveRide {
  const LiveRide({
    required this.state,
    required this.serverTime,
    this.driverAt,
    this.positionAt,
    this.etaMinutes,
    this.etaAt,
    this.waitEndsAt,
    this.late = false,
    this.pinLocked = false,
    this.approachRoute,
  });

  final RideState state;

  /// While on the way: the driver's road route to the pickup (encoded), or null.
  final String? approachRoute;

  /// The server's clock when it answered, so countdowns don't trust the phone's.
  final DateTime serverTime;

  /// The driver's latest position, and when they sent it.
  final GeoPoint? driverAt;
  final DateTime? positionAt;
  final int? etaMinutes;
  final DateTime? etaAt;
  final DateTime? waitEndsAt;
  final bool late;
  final bool pinLocked;
}
