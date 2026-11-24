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
  const DriverProfile({required this.phone, this.name, this.vehicleType, this.driverExists = true});
  final String phone;
  final String? name;

  /// e.g. "auto"; null when the backend didn't say.
  final String? vehicleType;

  /// False after the code for a new number: the driver signs up (D1.1)
  /// before anything else (auth.verify `driver_exists`).
  final bool driverExists;

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

// ---------------------------------------------------------------------------
// D1: driver registration, verification and subscription (backend/API.md,
// "Driver sign-up and verification (D1)").
// ---------------------------------------------------------------------------

/// drivers.status.
enum DriverStatus { signingUp, submitted, changesNeeded, approved, live, paused, rejected }

extension DriverStatusWire on DriverStatus {
  /// "signing_up", "changes_needed", …
  String get wire => switch (this) {
        DriverStatus.signingUp => 'signing_up',
        DriverStatus.changesNeeded => 'changes_needed',
        _ => name,
      };
}

DriverStatus driverStatusFrom(Object? s) => switch (s) {
        'submitted' => DriverStatus.submitted,
        'changes_needed' => DriverStatus.changesNeeded,
        'approved' => DriverStatus.approved,
        'live' => DriverStatus.live,
        'paused' => DriverStatus.paused,
        'rejected' => DriverStatus.rejected,
        _ => DriverStatus.signingUp,
      };

/// An item's review state. `missing`: not added yet.
enum ItemState { missing, uploaded, verified, rejected }

ItemState itemStateFrom(Object? s) => switch (s) {
      'uploaded' => ItemState.uploaded,
      'verified' => ItemState.verified,
      'rejected' => ItemState.rejected,
      _ => ItemState.missing,
    };

/// The eleven item kinds: seven documents (UPI included), the vehicle and
/// its three photos.
abstract final class ItemKind {
  static const profilePhoto = 'profile_photo';
  static const idProof = 'id_proof';
  static const licence = 'licence';
  static const rc = 'rc';
  static const insurance = 'insurance';
  static const permit = 'permit';
  static const upi = 'upi';
  static const vehicle = 'vehicle';
  static const vehicleFront = 'vehicle_front';
  static const vehicleBack = 'vehicle_back';
  static const vehicleInterior = 'vehicle_interior';

  /// Not an item, but submit lists it as missing until it is chosen.
  static const dayOff = 'day_off';

  /// The seven rows of D1.2, in order.
  static const documents = [profilePhoto, idProof, licence, rc, insurance, permit, upi];

  /// The three photo tiles of D1.3.
  static const vehiclePhotos = [vehicleFront, vehicleBack, vehicleInterior];

  /// Everything D1.3 covers.
  static const vehicleItems = [vehicle, vehicleFront, vehicleBack, vehicleInterior];

  static const all = [...documents, ...vehicleItems];

  /// Items that are a file (everything but the UPI ID and vehicle details).
  static bool isFile(String kind) => kind != upi && kind != vehicle;

  static String label(String kind) => switch (kind) {
        profilePhoto => 'Profile photo',
        idProof => 'ID proof',
        licence => 'Driving licence',
        rc => 'Vehicle RC',
        insurance => 'Insurance',
        permit => 'Permit',
        upi => 'UPI ID',
        vehicle => 'Vehicle details',
        vehicleFront => 'Vehicle front photo',
        vehicleBack => 'Vehicle back photo',
        vehicleInterior => 'Vehicle interior photo',
        dayOff => 'Day off',
        _ => kind,
      };
}

/// One of the driver's items (driver.get `items`).
class DriverItem {
  const DriverItem({
    required this.kind,
    String? label,
    this.state = ItemState.missing,
    this.rejectReason,
    this.value,
    this.storagePath,
    this.uploadedAt,
  }) : label = label ?? '';

  final String kind;
  final String label;
  final ItemState state;
  final String? rejectReason;

  /// The UPI ID, for the `upi` item.
  final String? value;
  final String? storagePath;
  final DateTime? uploadedAt;

  String get displayLabel => label.isEmpty ? ItemKind.label(kind) : label;
}

/// The driver's vehicle (driver.set_vehicle, driver.get `vehicle`).
class VehicleDetails {
  const VehicleDetails({
    required this.type,
    this.cabClass,
    this.makeModel = '',
    this.year,
    this.plate = '',
    this.colour = '',
    this.ac = false,
    this.cngOrElectric = false,
    this.reviewState = ItemState.missing,
    this.rejectReason,
  });

  /// `bike`, `auto` or `cab`.
  final String type;

  /// `hatchback`, `sedan` or `suv` for cabs, else null.
  final String? cabClass;
  final String makeModel;
  final int? year;
  final String plate;
  final String colour;
  final bool ac;
  final bool cngOrElectric;
  final ItemState reviewState;
  final String? rejectReason;

  /// The ride type the driver gets offers for: "auto", "cab_sedan".
  String get rideType => type == 'cab' && cabClass != null ? 'cab_$cabClass' : type;
}

/// The one plan for a vehicle type.
class DriverPlan {
  const DriverPlan({required this.vehicleType, this.cabClass, required this.label, required this.monthlyPrice});
  final String vehicleType;
  final String? cabClass;

  /// "Auto plan", "Sedan plan".
  final String label;

  /// Rupees a month.
  final int monthlyPrice;

  String get rideType => vehicleType == 'cab' && cabClass != null && cabClass!.isNotEmpty ? 'cab_$cabClass' : vehicleType;
}

enum PaymentStatus { claimed, confirmed, rejected }

PaymentStatus paymentStatusFrom(Object? s) => switch (s) {
      'confirmed' => PaymentStatus.confirmed,
      'rejected' => PaymentStatus.rejected,
      _ => PaymentStatus.claimed,
    };

/// A subscription payment the driver says they made ("I've paid").
class SubscriptionPayment {
  const SubscriptionPayment({
    required this.id,
    required this.periodStart,
    required this.periodEnd,
    required this.amount,
    required this.reference,
    required this.status,
    this.claimedAt,
    this.reviewedAt,
    this.rejectReason,
  });

  final String id;
  final DateTime periodStart;
  final DateTime periodEnd;
  final int amount;
  final String reference;
  final PaymentStatus status;
  final DateTime? claimedAt;
  final DateTime? reviewedAt;
  final String? rejectReason;
}

/// The driver as driver.get returns it.
class Driver {
  const Driver({
    required this.id,
    required this.name,
    required this.phone,
    required this.city,
    required this.status,
    this.rejectReason,
    required this.reference,
    this.upiId,
    this.dayOff,
    this.dayOffSet = false,
    this.subscriptionUntil,
    this.eligible = false,
    required this.items,
    this.missing = const [],
    this.vehicle,
    this.plan,
    this.latestPayment,
  });

  final String id;
  final String name;
  final String phone;

  /// City code, e.g. "blr".
  final String city;
  final DriverStatus status;

  /// Why the application was rejected (status `rejected`).
  final String? rejectReason;

  /// "DRV-20417": the reference to put on payments.
  final String reference;
  final String? upiId;

  /// ISO weekday of the weekly day off; null with [dayOffSet] for "No fixed day off".
  final int? dayOff;
  final bool dayOffSet;
  final DateTime? subscriptionUntil;

  /// Receives offers now (live with an active plan).
  final bool eligible;

  /// All eleven items.
  final List<DriverItem> items;

  /// What submit would refuse on (absent or rejected), as kinds.
  final List<String> missing;
  final VehicleDetails? vehicle;
  final DriverPlan? plan;
  final SubscriptionPayment? latestPayment;

  DriverItem item(String kind) =>
      items.firstWhere((i) => i.kind == kind, orElse: () => DriverItem(kind: kind, label: ItemKind.label(kind)));

  /// The seven rows of D1.2.
  List<DriverItem> get documents => [for (final k in ItemKind.documents) item(k)];

  int get documentsAdded => documents.where((i) => i.state != ItemState.missing).length;

  int get documentsVerified => documents.where((i) => i.state == ItemState.verified).length;

  List<DriverItem> get rejectedItems => [for (final i in items) if (i.state == ItemState.rejected) i];

  /// "auto", "cab_sedan"; null before the vehicle is set.
  String? get rideType => vehicle?.rideType ?? plan?.rideType;

  /// Whether the driver may change [kind] now: anything while signing up;
  /// in changes_needed only the rejected items (and ones replaced since).
  bool canEdit(String kind) => switch (status) {
        DriverStatus.signingUp => true,
        DriverStatus.changesNeeded => item(kind).state != ItemState.verified,
        _ => false,
      };

  /// Confirmed payment in the last 24 hours: the "You're live" banner shows.
  bool wentLiveRecently(DateTime now) {
    final p = latestPayment;
    if (status != DriverStatus.live || p == null || p.status != PaymentStatus.confirmed) return false;
    final at = p.reviewedAt ?? p.claimedAt;
    return at != null && now.difference(at) < const Duration(hours: 24);
  }

  DriverProfile get profile => DriverProfile(phone: phone, name: name.isEmpty ? null : name, vehicleType: rideType);
}

/// plans.get: the plan and what to pay now.
class PlanQuote {
  const PlanQuote({
    required this.plan,
    required this.amountDue,
    required this.periodStart,
    required this.periodEnd,
    required this.daysLeft,
    required this.daysInMonth,
    required this.reference,
    required this.payeeUpi,
    required this.status,
    this.latestPayment,
  });

  final DriverPlan plan;
  int get monthlyPrice => plan.monthlyPrice;

  /// "Auto plan".
  String get label => plan.label;

  /// `bike`, `auto` or `cab`.
  String get vehicleType => plan.vehicleType;

  /// Monthly price × days left (today included) ÷ days in the month, rounded up.
  final int amountDue;
  final DateTime periodStart;
  final DateTime periodEnd;
  final int daysLeft;
  final int daysInMonth;
  final String reference;

  /// Where to pay: "commuteride@icici".
  final String payeeUpi;
  final DriverStatus status;
  final SubscriptionPayment? latestPayment;
}

/// driver.upload_url: where to PUT a file.
class UploadTarget {
  const UploadTarget({
    required this.kind,
    required this.uploadUrl,
    this.method = 'PUT',
    required this.storagePath,
    required this.contentType,
    this.maxBytes = maxUploadBytes,
    required this.expiresAt,
  });

  final String kind;
  final String uploadUrl;
  final String method;
  final String storagePath;
  final String contentType;
  final int maxBytes;
  final DateTime expiresAt;
}

/// Files are JPEG or PNG, at most 5 MB.
const maxUploadBytes = 5 * 1024 * 1024;

/// City codes and names drivers can sign up in (app_config `driver_cities`).
const driverCities = [('blr', 'Bengaluru')];

String cityName(String code) {
  for (final (c, n) in driverCities) {
    if (c == code) return n;
  }
  return code;
}
