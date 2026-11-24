import 'dart:typed_data';

import 'package:driver/app.dart';
import 'package:driver/app_scope.dart';
import 'package:driver/core/api.dart';
import 'package:driver/core/clock.dart';
import 'package:driver/core/fake_api.dart';
import 'package:driver/core/models.dart';
import 'package:driver/core/image_source.dart';
import 'package:driver/ui/tokens.dart';
import 'package:driver/ui/widgets.dart' show labelKey;
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

// Every D1 test imports the screens through this file, so if a path differs
// only these lines change.
import 'package:driver/features/onboarding/sign_up_screen.dart';
import 'package:driver/features/onboarding/documents_screen.dart';

export 'package:driver/core/image_source.dart';
export 'package:driver/features/onboarding/sign_up_screen.dart';
export 'package:driver/features/onboarding/documents_screen.dart';
export 'package:driver/features/onboarding/vehicle_screen.dart';
export 'package:driver/features/onboarding/day_off_screen.dart';
export 'package:driver/features/onboarding/verification_screen.dart';
export 'package:driver/features/onboarding/plan_screen.dart';
export 'package:driver/features/onboarding/pay_screen.dart';

/// Drives flow D1 (spec "D1: Driver Registration, Verification and
/// Subscription") the way a new driver would. Shared by the D1 widget tests,
/// the D1 flow test and the on-device tests in integration_test/.
///
/// NAMES THE D1 TESTS RELY ON (agreed with the implementer; everything else
/// is found by the spec's copy):
///
/// Screens (package:driver/features/onboarding/<snake_name>.dart), each with
/// a no-argument constructor that loads its own state from the API in
/// AppScope (driver.get / plans.get) every time it opens:
///   SignUpScreen (D1.1), DocumentsScreen (D1.2), VehicleScreen (D1.3),
///   DayOffScreen (D1.4), VerificationScreen (D1.5), PlanScreen (D1.6),
///   PayScreen (D1.7).
/// Navigation: SignUp → Documents ("Continue to vehicle") → Vehicle
///   ("Continue") → DayOff ("Submit for review") → Verification ("Choose your
///   plan") → Plan ("Continue to payment") → Pay. Verification's "Fix now"
///   opens the item's screen (Documents or Vehicle).
/// Start: the first screen without a session either is SignUpScreen or has a
///   link whose label contains "Sign up" (key `sign_up`) that opens it.
///   With a restored session StartScreen routes by driver status:
///   submitted / changes_needed / approved / rejected → VerificationScreen;
///   live → DriverShell on Requests with the green "You're live" banner.
///
/// core/image_source.dart:
///   enum ImageOrigin { camera, gallery }
///   class PickedImage { PickedImage({required Uint8List bytes, String contentType = 'image/jpeg', String name}) }
///   abstract class ImageSource { Future<PickedImage?> pick(ImageOrigin origin); }  // null: cancelled
///   Injected as DriverApp({..., ImageSource? imageSource}) and
///   AppScope({..., ImageSource? imageSource}). Tests use [TestImageSource].
///
/// DriverApi (core/api.dart), implemented by FakeDriverApi and HttpDriverApi:
///   Future<D> driverSignup({required String name, required String city, required bool termsAccepted}); // POST /v1/driver/signup
///   Future<D> me();                                         // GET /v1/driver (404 not_registered)
///   Future<U> uploadUrl(String kind, String contentType);   // POST /v1/driver/upload-url
///   Future<I> uploadDocument(String kind, Uint8List bytes, String contentType);
///       // upload-url, PUT the bytes to upload_url (content-type header), then
///       // PUT /v1/driver/documents/{kind} {storage_path}; returns the item
///   Future<I> setUpi(String upiId);                         // PUT /v1/driver/upi
///   Future<V> setVehicle({required String type, String? cabClass, required String makeModel,
///       required int year, required String plate, required String colour,
///       required bool ac, required bool cngOrElectric});    // PUT /v1/driver/vehicle
///   Future<D> setDayOff(int? weekday);                      // PUT /v1/driver/day-off (null: no fixed day off)
///   Future<D> submitForReview();                            // POST /v1/driver/submit
///   Future<Q> plan();                                       // GET /v1/driver/plan
///   Future<P> claimPayment({DateTime? periodStart});        // POST /v1/driver/payments
/// Model fields (the type names D/U/I/V/Q/P are the implementer's choice):
///   D (driver): id, name, phone, city, status: DriverStatus, rejectReason, reference,
///     upiId, dayOff (int?, ISO), dayOffSet, subscriptionUntil (DateTime?), eligible,
///     items: List<I>, documentsAdded, documentsVerified, missing: List<String>,
///     vehicle: V?, latestPayment: P?
///   enum DriverStatus { signingUp, submitted, changesNeeded, approved, live, paused, rejected }
///   I (item): kind (String, API kind e.g. 'licence', 'upi', 'vehicle_front'), label,
///     state: ItemState, rejectReason, storagePath (String?), value (String?, UPI)
///   enum ItemState { missing, uploaded, verified, rejected }
///   U (upload ticket): kind, uploadUrl, method, storagePath, contentType, maxBytes, expiresAt
///   V (vehicle): type, cabClass, makeModel, year, plate, colour, ac, cngOrElectric,
///     reviewState: ItemState, rejectReason
///   Q (plan quote): label, vehicleType, monthlyPrice, amountDue, periodStart, periodEnd,
///     daysLeft, daysInMonth, reference, payeeUpi, latestPayment: P?
///   P (payment): id, periodStart, periodEnd, amount, reference, status: PaymentStatus,
///     claimedAt, rejectReason
///   enum PaymentStatus { claimed, confirmed, rejected }
///   Errors are ApiException(code, message, state: ...) with the codes of API.md (D1).
///
/// FakeDriverApi (core/fake_api.dart), D1 behaviour:
///   Any number but demoPhone signs in as a new driver; driverSignup creates
///   it, `signing_up` (reference DRV-nnnnn; riderPhone is 409
///   phone_in_use_as_rider; city 'blr' or 'Bengaluru', else 422
///   city_not_served; 422 invalid_name under 2 characters; 422
///   terms_not_accepted; a second signup is 409 already_registered).
///   Uploads: JPEG/PNG only (422 bad_content_type), at most 5 MB (413
///   file_too_large); storage paths driver-docs/<id>/<kind>. UPI pattern
///   name@handle (422 bad_upi). Plates upper-cased and unique (the seeded
///   live Ravi, demoPhone, holds KA 05 AB 1234); locking, incomplete,
///   not_approved, already_claimed as in API.md. Plan: auto 1199 a month,
///   payee UPI shown; amount due = price × days left (today included) ÷ days
///   in the month, rounded up. offers() is 403 not_eligible unless live.
///   failNextUploads (int): the next n uploads fail ("Upload failed. Try again.").
///   Admin hooks (synchronous, as the admin tool would do it):
///     adminApprove()                          every item verified, status approved
///     adminRequestChanges(String item, String reason)  that item rejected (item may be
///                                             'vehicle'), every other verified, changes_needed
///     adminReject(String reason)              status rejected with the reason
///     adminConfirmPayment()                   the claimed payment confirmed, live,
///                                             subscription_until = the period's end
///     adminRejectPayment(String reason)       the claimed payment rejected; can claim again
///
/// Keys (labelKey of the label, i.e. snake_case without articles):
///   buttons: get_code, verify, already_registered_log_in, continue_to_vehicle,
///     continue, submit_for_review, no_fixed_day_off, fix_now, submit_again,
///     choose_your_plan, contact_support, continue_to_payment, ive_paid,
///     checking_your_payment, copy, use_this_photo, upload, replace, add
///   rows and tiles: doc.<kind> for the seven document rows and the three
///     vehicle photo tiles (doc.vehicle_front / doc.vehicle_back / doc.vehicle_interior)
///   day chips: day.1 … day.7 (ISO weekday), else found by their letter
///   text fields: labelKey of the field's label (full_name_as_on_your_licence,
///     mobile_number, make_and_model, year, number_plate, colour), else found
///     by the label text.
///   The robot falls back to visible text wherever it can.

/// Sat 21 Nov 2026, 10:00: the spec's running example (Ravi signs up).
final d1Now = DateTime(2026, 11, 21, 10);

/// A number that isn't the demo driver's: a new driver.
const signUpPhone = '9800000201';
const signUpName = 'Ravi Kumar';
const signUpUpi = 'ravi@okaxis';

/// The seven documents of D1.2, in order.
const documentKinds = ['profile_photo', 'id_proof', 'licence', 'rc', 'insurance', 'permit', 'upi'];

/// The six documents that are photos.
const fileDocumentKinds = ['profile_photo', 'id_proof', 'licence', 'rc', 'insurance', 'permit'];

const vehiclePhotoKinds = ['vehicle_front', 'vehicle_back', 'vehicle_interior'];

const documentNames = {
  'profile_photo': 'Profile photo',
  'id_proof': 'ID proof',
  'licence': 'Driving licence',
  'rc': 'Vehicle RC',
  'insurance': 'Insurance',
  'permit': 'Permit',
  'upi': 'UPI ID',
};

const documentHints = {
  'profile_photo': 'A clear selfie, no sunglasses',
  'id_proof': 'Aadhaar or passport',
  'licence': 'Front and back, valid for commercial use',
  'rc': 'Registration certificate',
  'insurance': 'Commercial policy in force',
  'permit': 'Taxi or contract-carriage permit',
  'upi': 'Shown to riders so they can pay you',
};

/// A small JPEG as the store sniffs it (FF D8 FF), padded to [size] bytes.
Uint8List jpegBytes([int size = 256]) {
  final b = Uint8List(size);
  b[0] = 0xFF;
  b[1] = 0xD8;
  b[2] = 0xFF;
  b[3] = 0xE0;
  return b;
}

/// Just over the 5 MB limit.
Uint8List oversizeJpeg() => jpegBytes(5 * 1024 * 1024 + 1);

/// The image picker in tests: hands back [next] (a small JPEG by default),
/// or nothing when [cancel] is set. Records where each photo came from.
class TestImageSource implements ImageSource {
  TestImageSource({this.next});

  Uint8List? next;
  bool cancel = false;
  final List<ImageOrigin> asked = [];

  @override
  Future<PickedImage?> pick(ImageOrigin origin) async {
    asked.add(origin);
    if (cancel) return null;
    return PickedImage(bytes: next ?? jpegBytes(), contentType: 'image/jpeg', name: 'photo.jpg');
  }
}

// ---------------------------------------------------------------------------
// Getting the demo backend into a state, the way the API would.
// Zero-latency backends only (or call them inside tester.runAsync).

/// Signs in with [signUpPhone] and registers [name] in Bengaluru.
Future<void> d1SignedUp(FakeDriverApi api, {String name = signUpName}) async {
  await api.sendOtp(signUpPhone);
  await api.verifyOtp(signUpPhone, FakeDriverApi.demoOtp);
  await api.driverSignup(name: name, city: 'blr', termsAccepted: true);
}

/// Uploads [kinds] and, with [upi], sets the UPI ID.
Future<void> d1AddDocuments(FakeDriverApi api, {Iterable<String> kinds = fileDocumentKinds, bool upi = true}) async {
  for (final k in kinds) {
    await api.uploadDocument(k, jpegBytes(), 'image/jpeg');
  }
  if (upi) await api.setUpi(signUpUpi);
}

/// The new driver's auto, KA 05 CD 5678 (typed in lower case), with its
/// three photos. (The demo backend's seeded Ravi holds KA 05 AB 1234.)
Future<void> d1AddVehicle(FakeDriverApi api, {bool photos = true}) async {
  await api.setVehicle(
    type: 'auto',
    cabClass: null,
    makeModel: 'Bajaj RE',
    year: 2021,
    plate: 'ka 05 cd 5678',
    colour: 'Green',
    ac: false,
    cngOrElectric: true,
  );
  if (photos) {
    for (final k in vehiclePhotoKinds) {
      await api.uploadDocument(k, jpegBytes(), 'image/jpeg');
    }
  }
}

/// Every item present and Sunday as the day off; not yet submitted.
Future<void> d1Complete(FakeDriverApi api) async {
  await d1SignedUp(api);
  await d1AddDocuments(api);
  await d1AddVehicle(api);
  await api.setDayOff(7);
}

Future<void> d1Submitted(FakeDriverApi api) async {
  await d1Complete(api);
  await api.submitForReview();
}

Future<void> d1Approved(FakeDriverApi api) async {
  await d1Submitted(api);
  api.adminApprove();
}

Future<void> d1Claimed(FakeDriverApi api) async {
  await d1Approved(api);
  await api.claimPayment();
}

Future<void> d1Live(FakeDriverApi api) async {
  await d1Claimed(api);
  api.adminConfirmPayment();
}

// ---------------------------------------------------------------------------
// Pumping.

class D1Harness {
  D1Harness(this.backend, this.clock, this.images, {this.demoMode = true});

  /// The demo backend in widget tests; HttpDriverApi in the on-device e2e test.
  final DriverApi backend;
  final Clock clock;
  final TestImageSource images;
  final bool demoMode;

  /// The demo backend, with its admin hooks.
  FakeDriverApi get api => backend as FakeDriverApi;
}

D1Harness _harness(WidgetTester tester, Size size, DateTime? now, FakeDriverApi Function(DateTime Function() now)? backend) {
  tester.view.physicalSize = size * 3;
  tester.view.devicePixelRatio = 3;
  addTearDown(tester.view.reset);
  final clock = ManualClock(now ?? d1Now);
  final api = backend?.call(clock.now) ?? FakeDriverApi(now: clock.now, seed: false);
  return D1Harness(api, clock, TestImageSource());
}

/// One D1 screen on its own, over the demo backend after [setup] (run
/// outside fake time). The backend has no seeded offers unless [backend]
/// says otherwise.
Future<D1Harness> pumpD1Screen(
  WidgetTester tester,
  Widget Function() screen, {
  Future<void> Function(FakeDriverApi api)? setup,
  FakeDriverApi Function(DateTime Function() now)? backend,
  Size size = const Size(390, 844),
  DateTime? now,
}) async {
  final h = _harness(tester, size, now, backend);
  if (setup != null) await tester.runAsync(() => setup(h.api));
  await tester.pumpWidget(AppScope(
    api: h.api,
    clock: h.clock,
    demoMode: true,
    imageSource: h.images,
    child: MaterialApp(debugShowCheckedModeBanner: false, theme: buildDriverTheme(), home: screen()),
  ));
  await D1Robot(tester, h).settle();
  return h;
}

/// The whole app over the demo backend after [setup].
Future<D1Harness> pumpD1App(
  WidgetTester tester, {
  Future<void> Function(FakeDriverApi api)? setup,
  FakeDriverApi Function(DateTime Function() now)? backend,
  Size size = const Size(390, 844),
  DateTime? now,
}) async {
  final h = _harness(tester, size, now, backend);
  if (setup != null) await tester.runAsync(() => setup(h.api));
  await tester.pumpWidget(DriverApp(api: h.backend, clock: h.clock, imageSource: h.images));
  await D1Robot(tester, h).settle();
  return h;
}

/// Unmounts the app so timers stop.
Future<void> leaveD1(WidgetTester tester) async {
  await tester.pumpWidget(const SizedBox());
  await tester.pump(const Duration(seconds: 1));
}

/// "Pick the day you never drive." chips: M T W T F S S.
const dayLetters = ['M', 'T', 'W', 'T', 'F', 'S', 'S'];

/// Matches "₹1199" and "₹1,199".
RegExp rupees(int amount) {
  final s = amount.toString();
  if (s.length <= 3) return RegExp('₹$s(?![0-9,])');
  final head = s.substring(0, s.length - 3);
  final tail = s.substring(s.length - 3);
  final grouped = head.replaceAllMapped(RegExp(r'(\d)(?=(\d\d)+$)'), (m) => '${m[1]},?');
  return RegExp('₹$grouped,?$tail(?![0-9])');
}

class D1Robot {
  D1Robot(this.tester, this.h);

  final WidgetTester tester;
  final D1Harness h;

  /// The demo backend (widget tests only).
  FakeDriverApi get api => h.api;

  /// Whichever backend the app runs on.
  DriverApi get backend => h.backend;

  Finder key(String k) => find.byKey(Key(k));

  /// The key a button with [label] carries.
  Finder label(String label) => find.byKey(labelKey(label));

  /// Runs a backend call outside fake time (the demo latency waits on a
  /// real timer).
  Future<T> call<T>(Future<T> Function() f) async => (await tester.runAsync(f)) as T;

  /// Pumps frames until nothing more is scheduled, at most ~5 s of fake
  /// time past [minimum]. Unlike pumpAndSettle it never throws on a spinner
  /// that keeps going (e.g. a waiting state).
  ///
  /// It always pumps [minimum] first, so calls to a demo backend with
  /// latency (a fake-time timer that schedules no frame) get to finish.
  Future<void> settle({Duration? minimum}) async {
    const step = Duration(milliseconds: 100);
    final floor = (minimum ?? _minimumWait).inMilliseconds ~/ step.inMilliseconds;
    for (var i = 0; i < floor + 50; i++) {
      await tester.pump(step);
      if (i >= floor && !tester.binding.hasScheduledFrame) break;
    }
  }

  /// No latency: two frames. A slow demo backend, or the real one: long
  /// enough for a few calls in a row.
  Duration get _minimumWait {
    final b = h.backend;
    if (b is FakeDriverApi && b.latency == Duration.zero) return const Duration(milliseconds: 200);
    if (b is FakeDriverApi && b.latency * 5 > const Duration(milliseconds: 1500)) return b.latency * 5;
    return const Duration(milliseconds: 1500);
  }

  /// The first of [options] that shows within ~3 s of fake time; else the
  /// first option (so a failing expect names it).
  Future<Finder> firstOf(List<Finder> options) async {
    for (var i = 0; i < 30; i++) {
      for (final f in options) {
        if (f.evaluate().isNotEmpty) return f;
      }
      await tester.pump(const Duration(milliseconds: 100));
    }
    return options.first;
  }

  bool shows(Finder f) => f.evaluate().isNotEmpty;

  Future<void> scrollTo(Finder f) async {
    for (var i = 0; i < 20 && f.evaluate().isEmpty; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }
    if (f.evaluate().isEmpty) {
      // The screen's vertical list (not a horizontal row of chips).
      final s = find.byWidgetPredicate((w) => w is Scrollable && axisDirectionToAxis(w.axisDirection) == Axis.vertical);
      if (s.evaluate().isNotEmpty) await tester.scrollUntilVisible(f, 200, scrollable: s.first);
    }
    await tester.ensureVisible(f.first);
    await settle();
  }

  Future<void> tapFinder(Finder f) async {
    await scrollTo(f);
    await tester.tap(f.first, warnIfMissed: false);
    await settle();
  }

  Future<void> tap(String k) => tapFinder(key(k));

  /// Taps the button with [text]: by its label key, else by its text.
  Future<void> tapLabel(String text) async => tapFinder(await firstOf([label(text), find.text(text)]));

  Future<void> tapText(String text) => tapFinder(find.text(text).last);

  /// The text field for [fieldLabel]: by key, by its decoration, else the
  /// field just under a label with that text.
  Future<Finder> field(String fieldLabel) async {
    final f = await firstOf([
      find.byKey(labelKey(fieldLabel)),
      find.widgetWithText(TextField, fieldLabel),
      find.widgetWithText(TextFormField, fieldLabel),
      find.text(fieldLabel),
    ]);
    if (f.evaluate().isNotEmpty && f.evaluate().first.widget is Text) return _fieldUnder(f.first);
    return f.first;
  }

  Finder _fieldUnder(Finder labelText) {
    final top = tester.getRect(labelText).top;
    final fields = find.byType(EditableText);
    Finder? best;
    var bestDy = double.infinity;
    final n = fields.evaluate().length;
    for (var i = 0; i < n; i++) {
      final f = fields.at(i);
      final dy = tester.getRect(f).top - top;
      if (dy > -4 && dy < bestDy) {
        best = f;
        bestDy = dy;
      }
    }
    return best ?? fields.first;
  }

  Future<void> typeField(String fieldLabel, String text) async {
    final f = await field(fieldLabel);
    await scrollTo(f);
    await tester.enterText(f, text);
    await settle();
  }

  // --- D1.1 -----------------------------------------------------------------

  /// From a fresh app to D1.1.
  Future<void> openSignUp() async {
    final screen = find.byType(SignUpScreen);
    final link = await firstOf([
      screen,
      key('sign_up'),
      key('new_driver_sign_up'),
      find.textContaining('Sign up'),
      find.textContaining('sign up'),
    ]);
    if (!identical(link, screen)) await tapFinder(link.last);
    expect(screen, findsOneWidget);
  }

  Future<void> selectCity(String city) async {
    final chip = find.ancestor(of: find.text(city), matching: find.byType(ChoiceChip));
    if (shows(chip) && tester.widget<ChoiceChip>(chip.first).selected) return;
    await tapFinder(find.text(city).first);
  }

  Future<void> tickTerms() async {
    final box = find.byType(Checkbox);
    if (shows(box)) {
      if (tester.widget<Checkbox>(box.first).value == true) return;
      await tapFinder(box.first);
      return;
    }
    await tapFinder(await firstOf([key('terms'), label('I agree to the driver terms and to a background check.')]));
  }

  Finder get codeField => find.byType(EditableText).last;

  /// Types the code and, unless the screen verifies on its own, taps Verify.
  Future<void> enterCode(String code) async {
    final f = await firstOf([key('code'), key('otp.code'), key('signup.code'), label('6-digit code'), codeField]);
    await scrollTo(f);
    await tester.enterText(f.first, code);
    await settle();
    if (shows(key('verify')) && !shows(find.byType(DocumentsScreen))) {
      await tapFinder(key('verify'));
    }
  }

  /// D1.1 filled in, up to "Get code".
  Future<void> fillSignUp({String name = signUpName, String phone = signUpPhone, bool terms = true}) async {
    await typeField('Full name as on your licence', name);
    await typeField('Mobile number', phone);
    await selectCity('Bengaluru');
    if (terms) await tickTerms();
  }

  /// D1.1 through to D1.2.
  Future<void> signUp({String name = signUpName, String phone = signUpPhone}) async {
    await openSignUp();
    await fillSignUp(name: name, phone: phone);
    await tap('get_code');
    await enterCode(FakeDriverApi.demoOtp);
    expect(find.text('Your documents'), findsWidgets);
  }

  // --- D1.2 / D1.3 photos --------------------------------------------------

  Finder row(String kind) => key('doc.$kind');

  Finder inRow(String kind, Finder f) => find.descendant(of: row(kind), matching: f);

  /// The chooser after Upload: camera (or gallery), then "Use this photo".
  Future<void> choosePhoto() async {
    final source = await firstOf([
      key('camera'),
      key('take_photo'),
      key('take_a_photo'),
      find.textContaining(RegExp('camera', caseSensitive: false)),
      key('gallery'),
      key('choose_from_gallery'),
      find.textContaining(RegExp('gallery', caseSensitive: false)),
    ]);
    await tapFinder(source.last);
    await tapFinder(await firstOf([key('use_this_photo'), find.text('Use this photo')]));
  }

  /// Taps the row's Upload (or Replace / Fix) and goes through the chooser.
  Future<void> startUpload(String kind) async {
    await scrollTo(row(kind));
    final button = await firstOf([
      for (final k in ['upload', 'replace', 'fix', 'add']) inRow(kind, key(k)),
      for (final t in ['Upload', 'Replace', 'Fix']) inRow(kind, find.text(t)),
      row(kind),
    ]);
    await tapFinder(button.first);
  }

  Future<void> uploadDoc(String kind) async {
    await startUpload(kind);
    await choosePhoto();
  }

  /// D1.2's UPI row: Add, type the ID, save.
  Future<void> addUpi(String upiId) async {
    await scrollTo(row('upi'));
    await tapFinder(await firstOf([inRow('upi', key('add')), inRow('upi', find.text('Add')), row('upi')]));
    final f = await firstOf([key('upi_id'), key('upi.id'), find.byType(EditableText).last]);
    await tester.enterText(f.last, upiId);
    await settle();
    final save = [key('save'), key('save_upi_id'), key('done'), find.text('Save'), find.text('Done')];
    final s = save.where(shows).firstOrNull;
    if (s != null) {
      await tapFinder(s.last);
    } else {
      await tester.testTextInput.receiveAction(TextInputAction.done);
      await settle();
    }
  }

  /// All seven documents through D1.2.
  Future<void> addAllDocuments() async {
    for (final k in fileDocumentKinds) {
      await uploadDoc(k);
    }
    await addUpi(signUpUpi);
  }

  // --- D1.3 ----------------------------------------------------------------

  Future<void> fillVehicle({
    String type = 'Auto',
    String? cabClass,
    String makeModel = 'Bajaj RE',
    String year = '2021',
    String plate = 'ka 05 cd 5678',
    String colour = 'Green',
    bool photos = true,
  }) async {
    await tapFinder(find.text(type).first);
    if (cabClass != null) await tapFinder(find.text(cabClass).first);
    await typeField('Make and model', makeModel);
    await typeField('Year', year);
    await typeField('Number plate', plate);
    await typeField('Colour', colour);
    if (photos) {
      for (final (kind, text) in [('vehicle_front', 'Front'), ('vehicle_back', 'Back'), ('vehicle_interior', 'Interior')]) {
        await tapFinder(await firstOf([row(kind), find.text(text)]));
        await choosePhoto();
      }
    }
  }

  // --- D1.4 ----------------------------------------------------------------

  /// Taps the chip for ISO [weekday], or "No fixed day off" for null.
  Future<void> pickDayOff(int? weekday) async {
    if (weekday == null) {
      await tapLabel('No fixed day off');
      return;
    }
    final letter = dayLetters[weekday - 1];
    final nth = dayLetters.take(weekday - 1).where((l) => l == letter).length;
    await tapFinder(await firstOf([key('day.$weekday'), find.text(letter).at(nth)]));
  }

  // --- Admin side and reopening -------------------------------------------

  /// Opens [screen] afresh on the same backend (as pumpD1Screen does).
  Future<void> reopenScreen(Widget Function() screen) async {
    await tester.pumpWidget(const SizedBox());
    await settle();
    await tester.pumpWidget(AppScope(
      api: h.backend,
      clock: h.clock,
      demoMode: h.demoMode,
      imageSource: h.images,
      child: MaterialApp(debugShowCheckedModeBanner: false, theme: buildDriverTheme(), home: screen()),
    ));
    await settle();
  }

  /// Closes the app and opens it again on the same backend and clock (the
  /// screens read driver.get when they open).
  Future<void> reopenApp() async {
    await tester.pumpWidget(const SizedBox());
    await settle();
    await tester.pumpWidget(DriverApp(api: h.backend, clock: h.clock, imageSource: h.images, demoMode: h.demoMode));
    await settle();
  }
}

/// Flow D1 end to end through the app, as in the spec's story: sign up →
/// seven documents → vehicle → day off → submit → [approve] (the admin) →
/// "You're approved" → plan → I've paid → [confirmPayment] (the admin) →
/// Requests shows "You're live". The app is reopened after each admin step
/// (the push may be lost; the screens read driver.get when they open).
///
/// Shared by test/flows/d1_flow_test.dart (demo backend, admin hooks) and
/// integration_test/ (the demo backend on a device, or the Go backend).
Future<void> d1HappyPath(
  D1Robot robot, {
  required Future<void> Function() approve,
  required Future<void> Function() confirmPayment,
  String phone = signUpPhone,
  String plate = 'ka 05 cd 5678',
  bool demoAmounts = true,
}) async {
  final tester = robot.tester;

  // D1.1
  await robot.signUp(phone: phone);
  expect(find.textContaining('Step 2 of 5'), findsOneWidget);

  // D1.2
  await robot.addAllDocuments();
  await robot.scrollTo(find.textContaining('of 7 added'));
  expect(find.text('7 of 7 added'), findsOneWidget);
  await robot.tap('continue_to_vehicle');

  // D1.3
  expect(find.text('Your vehicle'), findsWidgets);
  await robot.fillVehicle(plate: plate);
  await robot.tap('continue');

  // D1.4
  expect(find.text('Your day off'), findsWidgets);
  await robot.pickDayOff(7);
  await robot.tap('submit_for_review');

  // D1.5, submitted
  expect(find.text("We're checking your details"), findsOneWidget);
  var d = await robot.call(() => robot.backend.me());
  expect(d.status, DriverStatus.submitted);
  expect(d.dayOff, 7);
  expect(d.documentsAdded, 7);
  expect(d.vehicle!.plate, plate.toUpperCase());
  final reference = d.reference;

  // The admin verifies every item and approves.
  await approve();
  await robot.reopenApp();
  expect(find.text("You're approved"), findsOneWidget);
  expect(find.text('7 of 7 verified'), findsOneWidget);

  // D1.6
  await robot.tap('choose_your_plan');
  expect(find.text('Driver subscription'), findsOneWidget);
  expect(find.text('Auto plan'), findsOneWidget);
  await robot.tap('continue_to_payment');

  // D1.7
  expect(find.text('Last step: pay your first month'), findsOneWidget);
  final q = await robot.call(() => robot.backend.plan());
  expect(q.reference, reference);
  if (demoAmounts) {
    expect(q.amountDue, (q.monthlyPrice * 10 / 30).ceil(), reason: '21–30 Nov: 10 of 30 days');
  }
  expect(find.textContaining(rupees(q.amountDue)), findsWidgets);
  await robot.scrollTo(find.text(reference));
  expect(find.text(reference), findsOneWidget);
  await robot.tap('ive_paid');
  expect(find.text('Checking your payment'), findsOneWidget);
  expect(find.textContaining('Paid on'), findsOneWidget);
  d = await robot.call(() => robot.backend.me());
  expect(d.latestPayment!.status, PaymentStatus.claimed);

  // The admin confirms the payment: live.
  await confirmPayment();
  await robot.reopenApp();
  expect(find.textContaining("You're live."), findsOneWidget);
  expect(find.textContaining('New requests for auto drivers show up here.'), findsOneWidget);
  d = await robot.call(() => robot.backend.me());
  expect(d.status, DriverStatus.live);
  expect(d.subscriptionUntil, q.periodEnd);
  expect(tester.takeException(), isNull);
}
