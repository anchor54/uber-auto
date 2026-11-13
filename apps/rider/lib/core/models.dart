/// Domain models shared by the rider app, its fake backend and tests.
library;

enum RideType { bike, auto, cabHatchback, cabSedan, cabSuv }

extension RideTypeInfo on RideType {
  /// Key used by the backend ride-type config.
  String get configKey => switch (this) {
        RideType.bike => 'bike',
        RideType.auto => 'auto',
        RideType.cabHatchback => 'cab_hatchback',
        RideType.cabSedan => 'cab_sedan',
        RideType.cabSuv => 'cab_suv',
      };

  String get label => switch (this) {
        RideType.bike => 'Bike',
        RideType.auto => 'Auto',
        RideType.cabHatchback => 'Cab · Hatchback',
        RideType.cabSedan => 'Cab · Sedan',
        RideType.cabSuv => 'Cab · SUV',
      };

  /// Lower-case noun used in sentences, e.g. "5 sedan drivers".
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

  bool get isCab =>
      this == RideType.cabHatchback ||
      this == RideType.cabSedan ||
      this == RideType.cabSuv;

  static RideType fromKey(String key) =>
      RideType.values.firstWhere((t) => t.configKey == key);
}

enum BookingFlow { oneTime, recurringWeekday, recurringWeekly }

extension BookingFlowInfo on BookingFlow {
  String get configKey => switch (this) {
        BookingFlow.oneTime => 'one_time',
        BookingFlow.recurringWeekday => 'recurring_weekday',
        BookingFlow.recurringWeekly => 'recurring_weekly',
      };
}

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
  final String title;

  /// Extra detail, e.g. "14th Main, HSR Layout" or "Gate 3".
  final String detail;
  final double lat;
  final double lng;

  String get display {
    if (detail.isEmpty) return title;
    return title == 'Home' || title == 'Office'
        ? '$title · $detail'
        : '$title, $detail';
  }

  @override
  bool operator ==(Object other) => other is Place && other.id == id;

  @override
  int get hashCode => id.hashCode;
}

class TripEstimate {
  const TripEstimate({required this.distanceKm, required this.minutes});
  final double distanceKm;
  final int minutes;
}

class RideTypeOption {
  const RideTypeOption({
    required this.type,
    required this.fare,
    required this.driversOnRoute,
  });

  final RideType type;

  /// Suggested fare per ride, in whole rupees.
  final int fare;

  /// Drivers of this type who already run the route at this time.
  final int driversOnRoute;
}

class BookingOptions {
  const BookingOptions({
    required this.flow,
    required this.options,
    required this.defaultType,
  });

  final BookingFlow flow;

  /// Only the ride types the backend config allows, in config order.
  final List<RideTypeOption> options;
  final RideType? defaultType;

  RideTypeOption? optionFor(RideType type) {
    for (final o in options) {
      if (o.type == type) return o;
    }
    return null;
  }
}

/// Everything the rider chose while setting up a regular ride.
class CommuteRequest {
  const CommuteRequest({
    required this.pickup,
    required this.drop,
    required this.repeat,
    required this.weekdays,
    required this.timing,
    required this.time,
    required this.pickupTime,
    required this.returnTrip,
    required this.returnTime,
    required this.startDate,
    required this.rideType,
    required this.preferences,
  });

  final Place pickup;
  final Place drop;
  final CommuteRepeat repeat;

  /// ISO weekdays, 1 = Monday … 7 = Sunday.
  final Set<int> weekdays;
  final TimingMode timing;

  /// Minutes after midnight the rider entered (pickup time or arrive-by time).
  final int time;

  /// Minutes after midnight of the morning pickup.
  final int pickupTime;
  final bool returnTrip;
  final int returnTime;
  final DateTime startDate;
  final RideType rideType;
  final Set<RidePreference> preferences;

  BookingFlow get flow => repeat == CommuteRepeat.everyWeekday
      ? BookingFlow.recurringWeekday
      : BookingFlow.recurringWeekly;

  CommuteRequest copyWith({
    int? time,
    int? pickupTime,
    Set<RidePreference>? preferences,
  }) =>
      CommuteRequest(
        pickup: pickup,
        drop: drop,
        repeat: repeat,
        weekdays: weekdays,
        timing: timing,
        time: time ?? this.time,
        pickupTime: pickupTime ?? this.pickupTime,
        returnTrip: returnTrip,
        returnTime: returnTime,
        startDate: startDate,
        rideType: rideType,
        preferences: preferences ?? this.preferences,
      );
}

class Driver {
  const Driver({
    required this.id,
    required this.name,
    required this.vehicle,
    required this.plate,
    required this.rating,
    required this.upiId,
    this.phone,
  });

  final String id;
  final String name;

  /// For the rider's Call button once a ride is under way. Null when the
  /// backend doesn't share it (before the driver accepts).
  final String? phone;

  /// e.g. "White Dzire · AC · 4 seats"
  final String vehicle;
  final String plate;
  final double rating;
  final String upiId;

  String get firstName => name.split(' ').first;

  String get initials {
    final parts = name.split(' ').where((p) => p.isNotEmpty).toList();
    if (parts.length == 1) return parts.first.substring(0, 1).toUpperCase();
    return (parts.first.substring(0, 1) + parts.last.substring(0, 1))
        .toUpperCase();
  }

  /// "White Dzire" from "White Dzire · AC · 4 seats".
  String get car => vehicle.split(' · ').first;

  Driver withPhone(String? value) =>
      Driver(id: id, name: name, vehicle: vehicle, plate: plate, rating: rating, upiId: upiId, phone: value);
}

class DriverMatch {
  const DriverMatch({
    required this.driver,
    required this.fare,
    required this.trip,
    required this.reasons,
    this.proposalId,
    this.expiresAt,
  });

  final Driver driver;
  final int fare;
  final TripEstimate trip;

  /// Why this driver fits, shown as check lines.
  final List<String> reasons;

  /// The backend's id for this offer; confirming sends it back. Null in the
  /// demo backend, which confirms from the match itself.
  final String? proposalId;

  /// When the backend stops honouring this offer.
  final DateTime? expiresAt;
}

/// `awaitingDriver`: confirmed by the rider, waiting for the driver to accept
/// (flow D2). `declined`: the driver said no or didn't answer in time; the
/// rider is asked to pick another driver.
enum CommuteStatus { awaitingDriver, active, paused, cancelled, declined }

/// Why a commute ended up [CommuteStatus.declined].
enum DeclineReason { declined, noAnswer }

class Commute {
  const Commute({
    required this.id,
    required this.request,
    required this.driver,
    required this.fare,
    required this.usualPayment,
    required this.status,
    this.respondBy,
    this.declineReason,
    this.trip,
  });

  final String id;
  final CommuteRequest request;

  /// The trip the server booked with (road distance and minutes). Setting up
  /// the same commute again uses it, so previews match the booking.
  final TripEstimate? trip;
  final Driver driver;
  final int fare;
  final PaymentMethod usualPayment;
  final CommuteStatus status;

  /// While awaiting the driver: when the request lapses if they don't answer.
  final DateTime? respondBy;

  /// Set when [status] is [CommuteStatus.declined].
  final DeclineReason? declineReason;

  Commute withStatus(CommuteStatus s, {DeclineReason? reason}) => Commute(
        id: id,
        request: request,
        driver: driver,
        fare: fare,
        usualPayment: usualPayment,
        status: s,
        respondBy: respondBy,
        declineReason: reason,
        trip: trip,
      );
}

class RiderProfile {
  const RiderProfile({required this.phone, this.name});
  final String phone;
  final String? name;
}

class RecentRide {
  const RecentRide({
    required this.drop,
    required this.rideType,
    required this.date,
  });
  final Place drop;
  final RideType rideType;
  final DateTime date;
}

class HomeData {
  const HomeData({
    required this.profile,
    required this.commute,
    required this.recentRides,
  });
  final RiderProfile profile;
  final Commute? commute;
  final List<RecentRide> recentRides;
}

class ApiException implements Exception {
  const ApiException(this.code, this.message);
  final String code;
  final String message;

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
  });

  final String id;
  final DateTime date;
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
  });

  final RideState state;

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
