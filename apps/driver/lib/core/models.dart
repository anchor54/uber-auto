/// Domain models of the driver app, shared with its fake backend and tests.
library;

enum PaymentMethod { upi, cash }

extension PaymentMethodInfo on PaymentMethod {
  String get label => switch (this) {
        PaymentMethod.upi => 'UPI',
        PaymentMethod.cash => 'Cash',
      };
}

enum RideDirection { outbound, returnLeg }

/// What an offer covers: a whole new series, one weekday of an active
/// series ("Wednesdays only"), or one ride on one date.
enum OfferKind { series, slot, ride }

/// `closing`: the window has passed but the sweep hasn't closed it yet.
enum OfferStatus { open, closing, closed }

/// "auto" -> "auto", "cab_sedan" -> "sedan": the ride type in sentences.
String vehicleNoun(String vehicleType) => switch (vehicleType) {
      'bike' => 'bike',
      'auto' => 'auto',
      'cab_hatchback' => 'hatchback',
      'cab_sedan' => 'sedan',
      'cab_suv' => 'SUV',
      _ => vehicleType.replaceAll('_', ' '),
    };

/// "Auto", "Cab · Sedan": the ride type as a label.
String vehicleLabel(String vehicleType) => switch (vehicleType) {
      'bike' => 'Bike',
      'auto' => 'Auto',
      'cab_hatchback' => 'Cab · Hatchback',
      'cab_sedan' => 'Cab · Sedan',
      'cab_suv' => 'Cab · SUV',
      _ => vehicleType,
    };

/// "ac" -> "AC on".
String preferenceLabel(String key) => switch (key) {
      'ac' || 'ac_on' => 'AC on',
      'quiet' => 'Quiet ride',
      'woman_driver' => 'Woman driver',
      _ => key,
    };

class DriverProfile {
  const DriverProfile({required this.phone, this.name, this.vehicleType});
  final String phone;
  final String? name;

  /// e.g. "auto"; null when the backend didn't say.
  final String? vehicleType;

  String? get firstName {
    final n = name?.trim();
    if (n == null || n.isEmpty) return null;
    return n.split(' ').first;
  }
}

class OfferPlace {
  const OfferPlace({required this.label, required this.lat, required this.lng});
  final String label;
  final double lat;
  final double lng;
}

class OfferRider {
  const OfferRider({required this.firstName, required this.name, required this.rating});

  /// "Priya"
  final String firstName;

  /// "Priya S."
  final String name;
  final double rating;

  String get initials {
    final parts = name.replaceAll('.', '').split(' ').where((p) => p.isNotEmpty).toList();
    if (parts.isEmpty) return '?';
    if (parts.length == 1) return parts.first.substring(0, 1).toUpperCase();
    return (parts.first.substring(0, 1) + parts.last.substring(0, 1)).toUpperCase();
  }
}

/// One weekday and direction of the requested rides.
class OfferSlot {
  const OfferSlot({
    required this.weekday,
    required this.direction,
    required this.pickupTime,
    this.estimatedDropTime,
    this.arriveByTime,
  });

  /// ISO weekday, 1 = Monday … 7 = Sunday.
  final int weekday;
  final RideDirection direction;

  /// Minutes after midnight.
  final int pickupTime;
  final int? estimatedDropTime;
  final int? arriveByTime;
}

/// A request offered to this driver (offers.list, offer.get).
class Offer {
  const Offer({
    required this.id,
    required this.kind,
    this.seriesId,
    required this.round,
    required this.status,
    this.outcome,
    required this.openedAt,
    required this.expiresAt,
    required this.secondsLeft,
    required this.fetchedAt,
    required this.canRespond,
    required this.interested,
    this.interestedAt,
    this.dismissed = false,
    this.result,
    required this.rider,
    required this.vehicleType,
    this.preferences = const [],
    required this.pickup,
    required this.drop,
    required this.startDate,
    required this.distanceKm,
    required this.travelMinutes,
    required this.farePerRide,
    required this.ridesPerMonth,
    required this.farePerMonth,
    required this.usualPayment,
    required this.slots,
  });

  final String id;
  final OfferKind kind;
  final String? seriesId;
  final int round;
  final OfferStatus status;

  /// `assigned`, `no_fit` or `cancelled` once closed.
  final String? outcome;
  final DateTime openedAt;
  final DateTime expiresAt;

  /// Seconds left in the window when this was read, by the server's clock.
  final int secondsLeft;

  /// When this was read, by the app's clock: with [secondsLeft], the
  /// countdown doesn't depend on the phone's clock matching the server's.
  final DateTime fetchedAt;
  final bool canRespond;
  final bool interested;
  final DateTime? interestedAt;
  final bool dismissed;

  /// `picked`, `not_picked`, `clashed`, `withdrew` or `ineligible` once decided.
  final String? result;
  final OfferRider rider;
  final String vehicleType;
  final List<String> preferences;
  final OfferPlace pickup;
  final OfferPlace drop;
  final DateTime startDate;
  final double distanceKm;
  final int travelMinutes;
  final int farePerRide;
  final int ridesPerMonth;
  final int farePerMonth;
  final PaymentMethod usualPayment;
  final List<OfferSlot> slots;

  /// When the window closes, on the app's clock.
  DateTime get closesAt => fetchedAt.add(Duration(seconds: secondsLeft));

  /// Whether the driver can still say they're interested at [now].
  bool openAt(DateTime now) => canRespond && now.isBefore(closesAt);

  List<OfferSlot> get outbound => [for (final s in slots) if (s.direction == RideDirection.outbound) s]
    ..sort((a, b) => a.weekday.compareTo(b.weekday));

  List<OfferSlot> get returns => [for (final s in slots) if (s.direction == RideDirection.returnLeg) s]
    ..sort((a, b) => a.weekday.compareTo(b.weekday));

  Set<int> get weekdays => {for (final s in outbound) s.weekday};

  Offer copyWith({bool? interested, bool? canRespond, OfferStatus? status, String? result}) => Offer(
        id: id,
        kind: kind,
        seriesId: seriesId,
        round: round,
        status: status ?? this.status,
        outcome: outcome,
        openedAt: openedAt,
        expiresAt: expiresAt,
        secondsLeft: secondsLeft,
        fetchedAt: fetchedAt,
        canRespond: canRespond ?? this.canRespond,
        interested: interested ?? this.interested,
        interestedAt: interestedAt,
        dismissed: dismissed,
        result: result ?? this.result,
        rider: rider,
        vehicleType: vehicleType,
        preferences: preferences,
        pickup: pickup,
        drop: drop,
        startDate: startDate,
        distanceKm: distanceKm,
        travelMinutes: travelMinutes,
        farePerRide: farePerRide,
        ridesPerMonth: ridesPerMonth,
        farePerMonth: farePerMonth,
        usualPayment: usualPayment,
        slots: slots,
      );
}

/// A result line on D2.1 for a request the driver was interested in, kept 24 hours.
class OfferResult {
  const OfferResult({required this.offerId, this.seriesId, required this.result, required this.message, this.closedAt});
  final String offerId;
  final String? seriesId;

  /// `picked`, `went_to_another_driver` or `rider_withdrew`.
  final String result;
  final String message;
  final DateTime? closedAt;
}

class OfferList {
  const OfferList({required this.offers, required this.results});

  /// Open offers, newest first.
  final List<Offer> offers;
  final List<OfferResult> results;
}

/// One of the driver's rides (rides.week).
class WeekRide {
  const WeekRide({
    required this.id,
    this.seriesId,
    required this.date,
    required this.weekday,
    required this.direction,
    required this.pickupTime,
    this.dropTime,
    required this.riderName,
    required this.fare,
    required this.isNew,
    this.status = 'assigned',
    this.pickupLabel = '',
    this.dropLabel = '',
  });

  final String id;
  final String? seriesId;
  final DateTime date;
  final int weekday;
  final RideDirection direction;

  /// Minutes after midnight, local.
  final int pickupTime;

  /// Estimated drop-off, minutes after midnight, when known.
  final int? dropTime;
  final String riderName;
  final int fare;

  /// Created in the last 24 hours: a new rider's rides after a win.
  final bool isNew;
  final String status;
  final String pickupLabel;
  final String dropLabel;

  String get riderFirstName => riderName.split(' ').first;
}

class DriverWeek {
  const DriverWeek({required this.weekStart, required this.weekEnd, required this.rides});
  final DateTime weekStart;
  final DateTime weekEnd;
  final List<WeekRide> rides;
}

class ApiException implements Exception {
  const ApiException(this.code, this.message, {this.state});
  final String code;
  final String message;

  /// The object as it is now (e.g. the [Offer] after a 409 offer_closed).
  final Object? state;

  @override
  String toString() => 'ApiException($code): $message';
}
