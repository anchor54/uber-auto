import 'package:driver/core/fake_api.dart';
import 'package:driver/core/models.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/d1_robot.dart';

/// The driver demo backend plays D1 of backend/API.md ("Driver sign-up and
/// verification") on its clock. Clock: Sat 21 Nov 2026, 10:00 (the spec's
/// running example).
void main() {
  late DateTime now;
  late FakeDriverApi api;

  setUp(() {
    now = d1Now;
    api = FakeDriverApi(now: () => now, seed: false);
  });

  Matcher apiError(String code) => throwsA(isA<ApiException>().having((e) => e.code, 'code', code));

  group('driver.register', () {
    test('signup: signing_up, a DRV-nnnnn reference, every item missing', () async {
      await d1SignedUp(api);
      final d = await api.me();
      expect(d.status, DriverStatus.signingUp);
      expect(d.name, signUpName);
      expect(d.reference, matches(RegExp(r'^DRV-\d{5}$')));
      expect(d.documentsAdded, 0);
      expect(d.documentsVerified, 0);
      expect(d.subscriptionUntil, isNull);
      expect(d.eligible, isFalse);
      final kinds = d.items.map((i) => i.kind).toSet();
      expect(kinds, containsAll([...documentKinds, ...vehiclePhotoKinds]));
      expect(d.items.every((i) => i.state == ItemState.missing), isTrue);
      expect(d.missing, containsAll(documentKinds));
    });

    test('"Bengaluru" is accepted as the city', () async {
      await api.sendOtp(signUpPhone);
      await api.verifyOtp(signUpPhone, FakeDriverApi.demoOtp);
      await api.driverSignup(name: signUpName, city: 'Bengaluru', termsAccepted: true);
      expect((await api.me()).status, DriverStatus.signingUp);
    });

    test('422 terms_not_accepted, invalid_name, city_not_served; 409 already_registered', () async {
      await api.sendOtp(signUpPhone);
      await api.verifyOtp(signUpPhone, FakeDriverApi.demoOtp);
      await expectLater(api.driverSignup(name: signUpName, city: 'blr', termsAccepted: false), apiError('terms_not_accepted'));
      await expectLater(api.driverSignup(name: 'R', city: 'blr', termsAccepted: true), apiError('invalid_name'));
      await expectLater(api.driverSignup(name: signUpName, city: 'Mumbai', termsAccepted: true), apiError('city_not_served'));
      await api.driverSignup(name: signUpName, city: 'blr', termsAccepted: true);
      await expectLater(api.driverSignup(name: signUpName, city: 'blr', termsAccepted: true), apiError('already_registered'));
    });

    test("a rider's number: 409 phone_in_use_as_rider", () async {
      await api.sendOtp(FakeDriverApi.riderPhone);
      await api.verifyOtp(FakeDriverApi.riderPhone, FakeDriverApi.demoOtp);
      await expectLater(api.driverSignup(name: signUpName, city: 'blr', termsAccepted: true), apiError('phone_in_use_as_rider'));
    });

    test('offers are 403 not_eligible until live', () async {
      await d1SignedUp(api);
      await expectLater(api.offers(), apiError('not_eligible'));
    });
  });

  group('items', () {
    setUp(() => d1SignedUp(api));

    test('upload_url: a path under driver-docs/<driver id>/, 5 MB, JPEG or PNG only', () async {
      final d = await api.me();
      final t = await api.uploadUrl('licence', 'image/jpeg');
      expect(t.kind, 'licence');
      expect(t.storagePath, 'driver-docs/${d.id}/licence');
      expect(t.maxBytes, 5242880);
      expect(t.uploadUrl, isNotEmpty);
      await api.uploadUrl('rc', 'image/png');
      await expectLater(api.uploadUrl('licence', 'image/gif'), apiError('bad_content_type'));
    });

    test('an uploaded document is "uploaded" and counts towards {n} of 7 added', () async {
      final item = await api.uploadDocument('licence', jpegBytes(), 'image/jpeg');
      expect(item.kind, 'licence');
      expect(item.state, ItemState.uploaded);
      final d = await api.me();
      expect(d.documentsAdded, 1);
      expect(d.items.singleWhere((i) => i.kind == 'licence').state, ItemState.uploaded);
      expect(d.missing, isNot(contains('licence')));
    });

    test('uploading again replaces the item (same path) until submit', () async {
      await api.uploadDocument('licence', jpegBytes(), 'image/jpeg');
      await api.uploadDocument('licence', jpegBytes(512), 'image/jpeg');
      final d = await api.me();
      expect(d.documentsAdded, 1);
      expect(d.items.where((i) => i.kind == 'licence'), hasLength(1));
    });

    test('over 5 MB: file_too_large, nothing saved', () async {
      await expectLater(api.uploadDocument('licence', oversizeJpeg(), 'image/jpeg'), apiError('file_too_large'));
      final d = await api.me();
      expect(d.items.singleWhere((i) => i.kind == 'licence').state, ItemState.missing);
      expect(d.documentsAdded, 0);
    });

    test('UPI ID: name@handle, counted as the seventh document', () async {
      await expectLater(api.setUpi('ravi'), apiError('bad_upi'));
      await expectLater(api.setUpi('ravi@'), apiError('bad_upi'));
      await api.setUpi(signUpUpi);
      final d = await api.me();
      expect(d.upiId, signUpUpi);
      expect(d.items.singleWhere((i) => i.kind == 'upi').state, isNot(ItemState.missing));
      expect(d.documentsAdded, 1);
    });

    test('all six photos and the UPI ID: 7 of 7 added', () async {
      await d1AddDocuments(api);
      expect((await api.me()).documentsAdded, 7);
    });

    test('vehicle: the plate is upper-cased; the type is saved', () async {
      await d1AddVehicle(api, photos: false);
      final v = (await api.me()).vehicle!;
      expect(v.type, 'auto');
      expect(v.cabClass, isNull);
      expect(v.makeModel, 'Bajaj RE');
      expect(v.year, 2021);
      expect(v.plate, 'KA 05 CD 5678');
      expect(v.colour, 'Green');
      expect(v.ac, isFalse);
      expect(v.cngOrElectric, isTrue);
    });

    test('vehicle: a cab with its class; 422 bad_vehicle_type', () async {
      await api.setVehicle(
          type: 'cab', cabClass: 'sedan', makeModel: 'Maruti Dzire', year: 2022, plate: 'KA01MN4321', colour: 'White', ac: true, cngOrElectric: false);
      final v = (await api.me()).vehicle!;
      expect(v.type, 'cab');
      expect(v.cabClass, 'sedan');
      expect(v.ac, isTrue);
      await expectLater(
        api.setVehicle(type: 'truck', cabClass: null, makeModel: 'Tata', year: 2020, plate: 'KA01AA0001', colour: 'Red', ac: false, cngOrElectric: false),
        apiError('bad_vehicle_type'),
      );
    });

    test("vehicle: another driver's plate, in lower case and without spaces, is 409 plate_in_use", () async {
      await expectLater(
        api.setVehicle(type: 'auto', cabClass: null, makeModel: 'Bajaj RE', year: 2021, plate: 'ka05ab1234', colour: 'Green', ac: false, cngOrElectric: true),
        apiError('plate_in_use'),
      );
      expect((await api.me()).vehicle, isNull);
    });

    test('vehicle photos are items too', () async {
      await d1AddVehicle(api);
      final d = await api.me();
      for (final k in vehiclePhotoKinds) {
        expect(d.items.singleWhere((i) => i.kind == k).state, ItemState.uploaded, reason: k);
      }
    });

    test('day off: one ISO weekday, or none ("No fixed day off")', () async {
      expect((await api.me()).dayOffSet, isFalse);
      await api.setDayOff(7);
      var d = await api.me();
      expect(d.dayOff, 7);
      expect(d.dayOffSet, isTrue);
      await api.setDayOff(3);
      expect((await api.me()).dayOff, 3);
      await api.setDayOff(null);
      d = await api.me();
      expect(d.dayOff, isNull);
      expect(d.dayOffSet, isTrue);
    });
  });

  group('driver.submit', () {
    test('missing items: 409 incomplete listing them; still signing_up', () async {
      await d1SignedUp(api);
      await d1AddDocuments(api, kinds: ['profile_photo', 'id_proof', 'licence', 'rc']);
      await d1AddVehicle(api);
      await api.setDayOff(7);
      await expectLater(api.submitForReview(), apiError('incomplete'));
      final d = await api.me();
      expect(d.status, DriverStatus.signingUp);
      expect(d.missing, containsAll(['insurance', 'permit']));
      expect(d.missing, isNot(contains('licence')));
    });

    test('without a vehicle or a day off: incomplete', () async {
      await d1SignedUp(api);
      await d1AddDocuments(api);
      await expectLater(api.submitForReview(), apiError('incomplete'));
      expect((await api.me()).status, DriverStatus.signingUp);
    });

    test('complete: submitted, and everything is locked', () async {
      await d1Submitted(api);
      expect((await api.me()).status, DriverStatus.submitted);
      await expectLater(api.uploadUrl('licence', 'image/jpeg'), apiError('locked'));
      await expectLater(api.uploadDocument('licence', jpegBytes(), 'image/jpeg'), apiError('locked'));
      await expectLater(api.setUpi('other@okicici'), apiError('locked'));
      await expectLater(
        api.setVehicle(type: 'auto', cabClass: null, makeModel: 'TVS King', year: 2020, plate: 'KA05ZZ9999', colour: 'Black', ac: false, cngOrElectric: false),
        apiError('locked'),
      );
      await expectLater(api.setDayOff(3), apiError('locked'));
      await expectLater(api.submitForReview(), apiError('wrong_status'));
      final d = await api.me();
      expect(d.dayOff, 7);
      expect(d.upiId, signUpUpi);
      expect(d.vehicle!.plate, 'KA 05 CD 5678');
    });

    test('plans.get and claim_payment before approval: 409 not_approved', () async {
      await d1Submitted(api);
      await expectLater(api.plan(), apiError('not_approved'));
      await expectLater(api.claimPayment(), apiError('not_approved'));
    });
  });

  group('review', () {
    setUp(() => d1Submitted(api));

    test('a rejected item: changes_needed with the reason; the rest verified', () async {
      api.adminRequestChanges('licence', 'photo blurred');
      final d = await api.me();
      expect(d.status, DriverStatus.changesNeeded);
      final licence = d.items.singleWhere((i) => i.kind == 'licence');
      expect(licence.state, ItemState.rejected);
      expect(licence.rejectReason, 'photo blurred');
      expect(d.items.where((i) => i.kind != 'licence').every((i) => i.state == ItemState.verified), isTrue);
      expect(d.documentsVerified, 6);
      expect(d.missing, ['licence']);
    });

    test('changes_needed: only the rejected item can be replaced; submit again keeps verified items', () async {
      api.adminRequestChanges('licence', 'photo blurred');
      await expectLater(api.uploadDocument('rc', jpegBytes(), 'image/jpeg'), apiError('locked'));
      await expectLater(api.setUpi('other@okicici'), apiError('locked'));
      await expectLater(api.setDayOff(3), apiError('locked'));
      await api.uploadDocument('licence', jpegBytes(), 'image/jpeg');
      var d = await api.me();
      expect(d.items.singleWhere((i) => i.kind == 'licence').state, ItemState.uploaded);
      expect(d.missing, isEmpty);
      await api.submitForReview();
      d = await api.me();
      expect(d.status, DriverStatus.submitted);
      expect(d.items.singleWhere((i) => i.kind == 'rc').state, ItemState.verified);
      expect(d.documentsVerified, 6);
    });

    test('changes_needed: submit before replacing is 409 incomplete', () async {
      api.adminRequestChanges('licence', 'photo blurred');
      await expectLater(api.submitForReview(), apiError('incomplete'));
      expect((await api.me()).status, DriverStatus.changesNeeded);
    });

    test('a rejected vehicle: its details can be set again', () async {
      api.adminRequestChanges('vehicle', 'plate not readable');
      var d = await api.me();
      expect(d.status, DriverStatus.changesNeeded);
      expect(d.vehicle!.reviewState, ItemState.rejected);
      expect(d.vehicle!.rejectReason, 'plate not readable');
      expect(d.documentsVerified, 7);
      await d1AddVehicle(api, photos: false);
      await expectLater(api.uploadDocument('licence', jpegBytes(), 'image/jpeg'), apiError('locked'));
      await api.submitForReview();
      d = await api.me();
      expect(d.status, DriverStatus.submitted);
    });

    test('approve: approved, every item verified', () async {
      api.adminApprove();
      final d = await api.me();
      expect(d.status, DriverStatus.approved);
      expect(d.documentsVerified, 7);
      expect(d.items.every((i) => i.state == ItemState.verified), isTrue);
      expect(d.vehicle!.reviewState, ItemState.verified);
      expect(d.eligible, isFalse, reason: 'not paid yet');
      await expectLater(api.offers(), apiError('not_eligible'));
    });

    test('reject: rejected for good with the reason; no plan, no claim', () async {
      api.adminReject('Licence not valid for commercial use');
      final d = await api.me();
      expect(d.status, DriverStatus.rejected);
      expect(d.rejectReason, 'Licence not valid for commercial use');
      await expectLater(api.plan(), apiError('not_approved'));
      await expectLater(api.claimPayment(), apiError('not_approved'));
      await expectLater(api.submitForReview(), apiError('wrong_status'));
    });
  });

  group('plan and payment', () {
    setUp(() => d1Approved(api));

    test('plans.get on Sat 21 Nov: the auto plan, 10 of 30 days due now, rounded up', () async {
      final q = await api.plan();
      final d = await api.me();
      expect(q.vehicleType, 'auto');
      expect(q.label, 'Auto plan');
      expect(q.monthlyPrice, 1199);
      expect(q.periodStart, DateTime(2026, 11, 21));
      expect(q.periodEnd, DateTime(2026, 11, 30));
      expect(q.daysLeft, 10);
      expect(q.daysInMonth, 30);
      expect(q.amountDue, (q.monthlyPrice * 10 / 30).ceil());
      expect(q.amountDue, 400);
      expect(q.reference, d.reference);
      expect(q.payeeUpi, contains('@'));
      expect(q.latestPayment, isNull);
    });

    test("I've paid: a claimed payment for the period; twice is 409 already_claimed", () async {
      final q = await api.plan();
      final p = await api.claimPayment();
      expect(p.status, PaymentStatus.claimed);
      expect(p.amount, q.amountDue);
      expect(p.periodStart, DateTime(2026, 11, 21));
      expect(p.periodEnd, DateTime(2026, 11, 30));
      expect(p.reference, q.reference);
      expect(p.claimedAt, isNotNull);
      await expectLater(
        api.claimPayment(),
        throwsA(isA<ApiException>().having((e) => e.code, 'code', 'already_claimed').having((e) => e.state, 'state', isNotNull)),
      );
      final d = await api.me();
      expect(d.status, DriverStatus.approved);
      expect(d.latestPayment!.status, PaymentStatus.claimed);
    });

    test('confirmed: live until 30 Nov, eligible, offers readable', () async {
      await api.claimPayment();
      api.adminConfirmPayment();
      final d = await api.me();
      expect(d.status, DriverStatus.live);
      expect(d.subscriptionUntil, DateTime(2026, 11, 30));
      expect(d.eligible, isTrue);
      expect(d.latestPayment!.status, PaymentStatus.confirmed);
      final list = await api.offers();
      expect(list.offers, isEmpty);
    });

    test('a rejected claim: the reason shows and the driver can claim again', () async {
      await api.claimPayment();
      api.adminRejectPayment('not received');
      var d = await api.me();
      expect(d.status, DriverStatus.approved);
      expect(d.latestPayment!.status, PaymentStatus.rejected);
      expect(d.latestPayment!.rejectReason, 'not received');
      final again = await api.claimPayment();
      expect(again.status, PaymentStatus.claimed);
      api.adminConfirmPayment();
      d = await api.me();
      expect(d.status, DriverStatus.live);
    });
  });

  test('approved on the last day of the month: one day due', () async {
    now = DateTime(2026, 11, 30, 23, 30);
    await d1Approved(api);
    final q = await api.plan();
    expect(q.periodStart, DateTime(2026, 11, 30));
    expect(q.periodEnd, DateTime(2026, 11, 30));
    expect(q.daysLeft, 1);
    expect(q.amountDue, (q.monthlyPrice / 30).ceil());
  });

  test('a 31-day month: 1 Dec is 31 of 31 days, the whole price', () async {
    now = DateTime(2026, 12, 1, 9);
    await d1Approved(api);
    final q = await api.plan();
    expect(q.daysInMonth, 31);
    expect(q.daysLeft, 31);
    expect(q.amountDue, q.monthlyPrice);
    expect(q.periodEnd, DateTime(2026, 12, 31));
  });
}
