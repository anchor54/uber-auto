import 'models.dart';

/// Everything the driver app needs from the backend.
///
/// [FakeDriverApi] implements this with a demo story and [HttpDriverApi]
/// talks to the real backend (backend/API.md, "Driver (D2)"). Screens only
/// see this interface.
abstract class DriverApi {
  /// The signed-in driver if a saved session is still valid, else null.
  Future<DriverProfile?> restoreSession();

  /// Sends a one-time code to [phone] (10 digits). 403 `not_a_driver` for a
  /// number that isn't a driver's.
  Future<void> sendOtp(String phone);

  /// Throws [ApiException] `invalid_code` when the code is wrong.
  Future<DriverProfile> verifyOtp(String phone, String code);

  /// offers.list: open offers this driver received (newest first), plus
  /// result cards for requests they were interested in, kept 24 hours.
  /// 403 `not_eligible` unless the driver is live with an active plan.
  Future<OfferList> offers();

  /// offer.get. 404 unless the driver received it.
  Future<Offer> offer(String id);

  /// offer.mark_interested. After the window: 409 `offer_closed` whose
  /// [ApiException.state] is the [Offer] as it is now.
  Future<void> markInterested(String id);

  /// offer.withdraw_interest. After the window: 409 `offer_closed`.
  Future<void> withdrawInterest(String id);

  /// offer.dismiss ("Not for me"): the request stops showing, for good.
  Future<void> dismissOffer(String id);

  /// rides.week: the driver's rides for 7 days from [start] (default: this
  /// week's Monday).
  Future<DriverWeek> week({DateTime? start});

  /// Registers this device's push token.
  Future<void> registerDevice(String pushToken, String platform);
}
