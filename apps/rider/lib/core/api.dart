import 'models.dart';

/// Everything the rider app needs from the backend.
///
/// [FakeRiderApi] implements this with demo data and [HttpRiderApi] talks to
/// the real backend (backend/). Screens only see this interface.
abstract class RiderApi {
  /// The signed-in rider if a saved session is still valid, else null.
  /// Called once at startup so a rider stays signed in across restarts.
  Future<RiderProfile?> restoreSession();

  /// Sends a one-time code to [phone] (10 digits, no country code).
  Future<void> sendOtp(String phone);

  /// Throws [ApiException] with code `invalid_code` when the code is wrong.
  Future<RiderProfile> verifyOtp(String phone, String code);

  Future<List<Place>> searchPlaces(String query);

  Future<TripEstimate> estimateTrip(Place pickup, Place drop);

  /// Ride types allowed for [flow] in the rider's city, with fares per ride.
  Future<BookingOptions> bookingOptions({
    required BookingFlow flow,
    required Place pickup,
    required Place drop,
    required int pickupTime,
  });

  /// Best match first. Empty when no driver can serve the commute.
  Future<List<DriverMatch>> findDrivers(CommuteRequest request);

  Future<Commute> confirmCommute({
    required CommuteRequest request,
    required DriverMatch match,
    required PaymentMethod usualPayment,
  });

  Future<void> joinWaitlist(CommuteRequest request);

  Future<HomeData> home();

  // ---- flow R2: the daily ride ----

  /// The rider's rides from today for [days] days, in pickup order.
  Future<List<Ride>> upcomingRides({int days = 7});

  Future<Ride> ride(String id);

  /// Where the driver is and when they'll arrive. Polled every 5 s while a ride is live.
  Future<LiveRide> liveRide(String id);

  /// "I'm 5 min late": once per ride; the driver waits 10 minutes instead of 5.
  Future<Ride> runningLate(String id);

  /// How the rider paid. Returns the status combining both sides' marks.
  Future<PaymentStatus> markPayment(String id, PaymentMethod method);

  /// Stars, tags, a note. Can be sent again to change it for 7 days.
  Future<void> rateRide(String id, RideRating rating);

  /// Completed rides the rider hasn't marked or rated, newest first.
  Future<List<Ride>> unsettledRides();

  /// Registers this device's push token.
  Future<void> registerDevice(String pushToken, String platform);
}
