import 'dart:typed_data';

import 'models.dart';

/// Everything the driver app needs from the backend.
///
/// [FakeDriverApi] implements this with a demo story and [HttpDriverApi]
/// talks to the real backend (backend/API.md, "Driver (D2)" and "Driver
/// sign-up and verification (D1)"). Screens only see this interface.
abstract class DriverApi {
  /// The signed-in driver if a saved session is still valid, else null.
  Future<DriverProfile?> restoreSession();

  /// Sends a one-time code to [phone] (10 digits). 403 `not_a_driver` for a
  /// number that isn't a driver's.
  Future<void> sendOtp(String phone);

  /// Throws [ApiException] `invalid_code` when the code is wrong. A number
  /// with no driver profile yet comes back with
  /// [DriverProfile.driverExists] false: sign up next ([driverSignup]).
  Future<DriverProfile> verifyOtp(String phone, String code);

  // D1: sign-up, documents, vehicle, day off, review, plan, payment.

  /// driver.register (D1.1) for the signed-in number: status `signing_up`.
  /// 409 `phone_in_use_as_rider`, `already_registered`; 422 `invalid_name`,
  /// `city_not_served`, `terms_not_accepted`.
  Future<Driver> driverSignup({required String name, required String city, required bool termsAccepted});

  /// driver.get: the driver, items with states and reasons, vehicle, day
  /// off, plan and latest payment. 404 `not_registered` before sign-up.
  Future<Driver> me();

  /// driver.upload_url: a signed URL to PUT one file to. 409 `locked`,
  /// 422 `bad_content_type`.
  Future<UploadTarget> uploadUrl(String kind, String contentType);

  /// Uploads a photo as [kind] (a document or a vehicle photo): asks for an
  /// upload URL, PUTs [bytes] with [contentType] to it, then records it
  /// (driver.item_uploaded). Returns the item, `uploaded`. 413
  /// `file_too_large` over 5 MB.
  Future<DriverItem> uploadDocument(String kind, Uint8List bytes, String contentType);

  /// driver.set_upi. 422 `bad_upi`, 409 `locked`.
  Future<DriverItem> setUpi(String upiId);

  /// driver.set_vehicle: [type] `bike`, `auto` or `cab` (with [cabClass]
  /// `hatchback`, `sedan` or `suv`). The plate comes back upper-cased.
  /// 409 `plate_in_use`, `locked`; 422 `bad_vehicle_type`, `bad_cab_class`,
  /// `bad_plate`, `bad_year`.
  Future<VehicleDetails> setVehicle({
    required String type,
    String? cabClass,
    required String makeModel,
    required int year,
    required String plate,
    required String colour,
    required bool ac,
    required bool cngOrElectric,
  });

  /// driver.set_day_off: ISO weekday, or null for "No fixed day off".
  Future<Driver> setDayOff(int? weekday);

  /// driver.submit. 409 `incomplete` whose [ApiException.state] is the
  /// [Driver] with its `missing` kinds; 409 `wrong_status`.
  Future<Driver> submitForReview();

  /// plans.get. 409 `not_approved` before approval.
  Future<PlanQuote> plan();

  /// driver.claim_payment ("I've paid"). 409 `already_claimed` whose state
  /// is that [SubscriptionPayment]; 409 `not_approved`.
  Future<SubscriptionPayment> claimPayment({DateTime? periodStart});

  /// Push messages for this driver as they arrive, by kind
  /// (`driver_approved`, `driver_changes_needed`, `driver_rejected`,
  /// `driver_live`, `driver_payment_rejected`). Screens read the driver
  /// again on each. Empty when the app has no push channel.
  Stream<String> get pushes;

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
