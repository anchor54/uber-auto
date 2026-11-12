import 'models.dart';

/// Everything the rider app needs from the backend.
///
/// [FakeRiderApi] implements this with demo data; an HTTP implementation
/// talking to the real backend comes later with the same contract.
abstract class RiderApi {
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
}
