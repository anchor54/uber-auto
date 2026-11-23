import 'models.dart';

/// Everything the rider app needs from the backend.
///
/// [FakeRiderApi] implements this with demo data and [HttpRiderApi] talks to
/// the real backend (backend/, see backend/API.md). Screens only see this
/// interface.
abstract class RiderApi {
  /// The signed-in rider if a saved session is still valid, else null.
  /// Called once at startup so a rider stays signed in across restarts.
  Future<RiderProfile?> restoreSession();

  /// Sends a one-time code to [phone] (10 digits, no country code).
  Future<void> sendOtp(String phone);

  /// Throws [ApiException] with code `invalid_code` when the code is wrong.
  Future<RiderProfile> verifyOtp(String phone, String code);

  Future<List<Place>> searchPlaces(String query);

  /// R1.3: the ride types allowed for regular rides in the rider's city, in
  /// config order, each with its suggested fare for [request]'s route.
  Future<List<RideTypeOption>> rideTypeOptions(CommuteRequest request);

  // ---- flow R1: the series request ----

  /// series.preview: start date, fare, per-slot pickup times. Nothing is saved.
  /// 422 `start_too_soon`, `ride_type_not_allowed`, `invalid_time`.
  Future<SeriesPreview> previewSeries(CommuteRequest request);

  /// series.create: the series, `finding_driver` with round 1 open (or
  /// `no_driver_found` straight away when no driver is eligible).
  Future<Series> createSeries(CommuteRequest request);

  /// series.get. 403 `forbidden`, 404 `not_found`.
  Future<Series> getSeries(String id);

  /// The rider's series, newest first.
  Future<List<Series>> listSeries();

  /// series.cancel_request: ends a `finding_driver` or `no_driver_found`
  /// series. Once a driver is assigned: [ApiException] `already_assigned`
  /// whose [ApiException.state] is the [Series] with its driver.
  Future<void> cancelRequest(String id);

  /// series.retry from `no_driver_found`, optionally with new times or ride
  /// type ([changes] rewrites every slot). 409 `not_retryable` otherwise.
  Future<Series> retrySeries(String id, {CommuteRequest? changes});

  /// Home: the rider and their series that aren't ended.
  Future<HomeData> home();

  // ---- flow R2: the daily ride (reworked with R2) ----

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
