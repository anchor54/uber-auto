import 'dart:async';
import 'dart:typed_data';

import 'api.dart';
import 'models.dart';
import 'schedule.dart';

/// In-memory backend used in demo mode and in every test, on the injected
/// clock (tests move the clock instead of waiting).
///
/// The demo story, for Ravi Kumar (auto), as in the backend's spec seed:
/// * The OTP is always [demoOtp]; any 10-digit number signs in as Ravi.
/// * With [seed], two offers are open from the clock's now: Priya's regular
///   ride (Koramangala → Whitefield, Mon/Wed/Fri 08:30 from next Monday, no
///   return, a [seriesWindow] window) and a one-ride offer (Kavya, tomorrow
///   09:00, a [rideWindow] window).
/// * When a window passes, the demo pick runs: if Ravi was interested he is
///   picked, the offer's rides join his week (14 days ahead, flagged new),
///   and a "picked" result shows. An offer he wasn't interested in just
///   leaves the list.
/// * Marking or withdrawing after the window: 409 `offer_closed`.
/// * Test hooks: [goesToAnotherDriver], [riderWithdraws], [closeWindows].
///
/// D1 (sign-up and verification):
/// * [demoPhone] is Ravi, already live. Any other 10-digit number is a new
///   driver: the code signs in with `driverExists` false, and
///   [driverSignup] creates the driver (`signing_up`, reference DRV-20417
///   for the first one). [riderPhone] belongs to a rider: 409
///   `phone_in_use_as_rider`.
/// * Uploads, UPI, vehicle, day off and submit follow the backend's rules
///   (locking, `incomplete`, `plate_in_use` against Ravi's KA 05 AB 1234…).
/// * The admin is played by hooks on the signed-in driver: [adminApprove],
///   [adminRequestChanges], [adminReject], [adminConfirmPayment],
///   [adminRejectPayment]. Each sends a push on [pushes], as the backend
///   would. [failNextUploads] makes uploads fail.
/// * With [autoReviewAfter] (demo mode), a submitted driver is approved
///   and a claimed payment confirmed that long after, by a timer and also
///   whenever the driver is read after that time. Null in tests: nothing
///   happens unless a test calls a hook.
class FakeDriverApi implements DriverApi {
  FakeDriverApi({
    this.latency = Duration.zero,
    DateTime Function()? now,
    bool seed = true,
    this.seriesWindow = const Duration(minutes: 15),
    this.rideWindow = const Duration(minutes: 5),
    this.autoReviewAfter,
  }) : _now = now ?? DateTime.now {
    _seedRavi();
    if (seed) _seed();
  }

  static const demoOtp = '123456';
  static const demoPhone = '9000000201';
  static const demoName = 'Ravi Kumar';
  static const demoVehicleType = 'auto';

  /// A rider's number (Priya): signing up with it is 409 phone_in_use_as_rider.
  static const riderPhone = '9000000101';

  /// Where drivers pay the subscription.
  static const payeeUpi = 'commuteride@icici';

  /// Demo mode: how long the pretend admin takes to approve and to confirm a
  /// payment. Null: only the admin hooks change anything.
  final Duration? autoReviewAfter;

  final Duration latency;
  final Duration seriesWindow;
  final Duration rideWindow;
  final DateTime Function() _now;

  DriverProfile? _profile;
  final List<_FakeOffer> _offers = [];
  final List<WeekRide> _rides = [];
  final List<String> pushTokens = [];

  /// Shared by every instance, so offer ids never repeat across fresh
  /// backends (a test that remembers ids it used would otherwise loop).
  static int _ids = 0;

  Future<void> _wait() => latency == Duration.zero ? Future<void>.value() : Future<void>.delayed(latency);

  void _seed() {
    final now = _now();
    final today = dateOnly(now);
    // Next week's Monday, as in the spec seed (Thu 19 Nov → Mon 23 Nov).
    final start = weekStart(addDays(today, 7));
    addSeriesOffer(
      rider: const OfferRider(firstName: 'Priya', name: 'Priya S.', rating: 4.8),
      pickup: const OfferPlace(label: 'Koramangala', lat: 12.9352, lng: 77.6245),
      drop: const OfferPlace(label: 'Whitefield', lat: 12.9698, lng: 77.7178),
      weekdays: const {1, 3, 5},
      pickupTime: 8 * 60 + 30,
      travelMinutes: 51,
      distanceKm: 15.1,
      fare: 197,
      startDate: start,
    );
    final tomorrow = addDays(today, 1);
    _offers.add(_FakeOffer(
      id: 'of_ride_${++_ids}',
      kind: OfferKind.ride,
      seriesId: 'sr_kavya',
      openedAt: now,
      expiresAt: now.add(rideWindow),
      rider: const OfferRider(firstName: 'Kavya', name: 'Kavya N.', rating: 4.7),
      pickup: const OfferPlace(label: 'Indiranagar', lat: 12.9784, lng: 77.6408),
      drop: const OfferPlace(label: 'Manyata Tech Park', lat: 13.0475, lng: 77.6203),
      startDate: tomorrow,
      distanceKm: 11.2,
      travelMinutes: 38,
      fare: 155,
      ridesPerMonth: 1,
      payment: PaymentMethod.cash,
      slots: [OfferSlot(weekday: tomorrow.weekday, direction: RideDirection.outbound, pickupTime: 9 * 60, estimatedDropTime: 9 * 60 + 38)],
    ));
  }

  /// Opens a new series offer to this driver now (demo and tests).
  Offer addSeriesOffer({
    required OfferRider rider,
    required OfferPlace pickup,
    required OfferPlace drop,
    required Set<int> weekdays,
    required int pickupTime,
    int? returnTime,
    required int travelMinutes,
    required double distanceKm,
    required int fare,
    required DateTime startDate,
    PaymentMethod payment = PaymentMethod.upi,
  }) {
    final now = _now();
    final days = weekdays.toList()..sort();
    final slots = [
      for (final d in days) OfferSlot(weekday: d, direction: RideDirection.outbound, pickupTime: pickupTime, estimatedDropTime: pickupTime + travelMinutes),
      if (returnTime != null)
        for (final d in days)
          OfferSlot(weekday: d, direction: RideDirection.returnLeg, pickupTime: returnTime, estimatedDropTime: returnTime + travelMinutes),
    ];
    final o = _FakeOffer(
      id: 'of_${++_ids}',
      kind: OfferKind.series,
      seriesId: 'sr_$_ids',
      openedAt: now,
      expiresAt: now.add(seriesWindow),
      rider: rider,
      pickup: pickup,
      drop: drop,
      startDate: startDate,
      distanceKm: distanceKm,
      travelMinutes: travelMinutes,
      fare: fare,
      ridesPerMonth: (slots.length * 30 / 7).round(),
      payment: payment,
      slots: slots,
    );
    _offers.add(o);
    return _view(o, now);
  }

  @override
  Future<DriverProfile?> restoreSession() async => _profile;

  @override
  Future<void> sendOtp(String phone) async {
    await _wait();
    if (!RegExp(r'^[6-9]\d{9}$').hasMatch(phone)) {
      throw const ApiException('invalid_phone', 'Enter a 10-digit mobile number starting with 6, 7, 8 or 9');
    }
  }

  @override
  Future<DriverProfile> verifyOtp(String phone, String code) async {
    await _wait();
    if (code != demoOtp) {
      throw const ApiException('invalid_code', "That code isn't right. Check the SMS and try again.");
    }
    _phone = phone;
    final d = _drivers[phone];
    return _profile = DriverProfile(
      phone: phone,
      name: d?.name,
      vehicleType: d?.vehicle?.rideType,
      driverExists: d != null,
    );
  }

  /// Runs the sweep: closes windows that have passed and makes the demo pick.
  void _catchUp() {
    final now = _now();
    for (final o in _offers) {
      if (o.closedAt != null || o.expiresAt.isAfter(now)) continue;
      _close(o, o.expiresAt, o.interested ? 'picked' : null);
    }
  }

  void _close(_FakeOffer o, DateTime at, String? result, {String outcome = 'assigned'}) {
    o.closedAt = at;
    o.outcome = outcome;
    o.result = result;
    if (result == 'picked') _win(o);
  }

  /// The won offer's rides, 14 days ahead from now, flagged new.
  void _win(_FakeOffer o) {
    final now = _now();
    final last = addDays(dateOnly(now), 14);
    var day = o.startDate.isAfter(dateOnly(now)) ? o.startDate : addDays(dateOnly(now), 1);
    while (!day.isAfter(last)) {
      for (final s in o.slots) {
        if (s.weekday != day.weekday) continue;
        _rides.add(WeekRide(
          id: 'ride_${o.id}_${dateKeyOf(day)}_${s.direction.name}',
          seriesId: o.seriesId,
          date: day,
          weekday: day.weekday,
          direction: s.direction,
          pickupTime: s.pickupTime,
          dropTime: s.estimatedDropTime,
          riderName: o.riderFullName,
          fare: o.fare,
          isNew: true,
          pickupLabel: s.direction == RideDirection.outbound ? o.pickup.label : o.drop.label,
          dropLabel: s.direction == RideDirection.outbound ? o.drop.label : o.pickup.label,
        ));
      }
      if (o.kind == OfferKind.ride) break;
      day = addDays(day, 1);
    }
    o.wonAt = now;
  }

  static String dateKeyOf(DateTime d) =>
      '${d.year}${d.month.toString().padLeft(2, '0')}${d.day.toString().padLeft(2, '0')}';

  /// The pick gives [offerId] to another driver now.
  void goesToAnotherDriver(String offerId) {
    final o = _find(offerId);
    if (o.closedAt != null) return;
    _close(o, _now(), o.interested ? 'not_picked' : null);
  }

  /// The rider cancels the request now.
  void riderWithdraws(String offerId) {
    final o = _find(offerId);
    if (o.closedAt != null) return;
    _close(o, _now(), null, outcome: 'cancelled');
  }

  /// Closes every open window now, as if it had passed: the demo pick runs.
  void closeWindows() {
    final now = _now();
    for (final o in _offers) {
      if (o.closedAt == null) _close(o, now, o.interested ? 'picked' : null);
    }
  }

  _FakeOffer _find(String id) {
    for (final o in _offers) {
      if (o.id == id) return o;
    }
    throw const ApiException('not_found', 'No such offer');
  }

  Offer _view(_FakeOffer o, DateTime now) {
    final open = o.closedAt == null && now.isBefore(o.expiresAt);
    final left = open ? o.expiresAt.difference(now).inSeconds : 0;
    return Offer(
      id: o.id,
      kind: o.kind,
      seriesId: o.seriesId,
      round: 1,
      status: o.closedAt != null ? OfferStatus.closed : (open ? OfferStatus.open : OfferStatus.closing),
      outcome: o.outcome,
      openedAt: o.openedAt,
      expiresAt: o.expiresAt,
      secondsLeft: left,
      fetchedAt: now,
      canRespond: open,
      interested: o.interested,
      interestedAt: o.interestedAt,
      dismissed: o.dismissed,
      result: o.result,
      rider: o.rider,
      vehicleType: _current?.vehicle?.rideType ?? demoVehicleType,
      pickup: o.pickup,
      drop: o.drop,
      startDate: o.startDate,
      distanceKm: o.distanceKm,
      travelMinutes: o.travelMinutes,
      farePerRide: o.fare,
      ridesPerMonth: o.ridesPerMonth,
      farePerMonth: o.ridesPerMonth * o.fare,
      usualPayment: o.payment,
      slots: o.slots,
    );
  }

  @override
  Future<OfferList> offers() async {
    await _wait();
    _catchUpReview();
    final d = _current;
    if (d != null && d.status != DriverStatus.live) {
      throw ApiException('not_eligible', 'Requests show once you are live.', state: _driverView(d));
    }
    _catchUp();
    final now = _now();
    final open = [for (final o in _offers) if (o.closedAt == null && !o.dismissed) o]
      ..sort((a, b) => b.openedAt.compareTo(a.openedAt));
    final results = <OfferResult>[];
    for (final o in _offers.reversed) {
      final closed = o.closedAt;
      if (closed == null || !o.interested || now.difference(closed) >= const Duration(hours: 24)) continue;
      final (result, message) = switch ((o.outcome, o.result)) {
        ('cancelled', _) => ('rider_withdrew', 'The rider withdrew this request'),
        (_, 'picked') => ('picked', 'You got it.'),
        _ => ('went_to_another_driver', 'Went to another driver'),
      };
      results.add(OfferResult(offerId: o.id, seriesId: o.seriesId, result: result, message: message, closedAt: closed));
    }
    return OfferList(offers: [for (final o in open) _view(o, now)], results: results);
  }

  @override
  Future<Offer> offer(String id) async {
    await _wait();
    _catchUp();
    return _view(_find(id), _now());
  }

  void _respond(String id, void Function(_FakeOffer o) change) {
    _catchUp();
    final o = _find(id);
    final now = _now();
    if (o.closedAt != null || !now.isBefore(o.expiresAt)) {
      throw ApiException('offer_closed', 'This request has closed.', state: _view(o, now));
    }
    change(o);
  }

  @override
  Future<void> markInterested(String id) async {
    await _wait();
    _respond(id, (o) {
      if (o.interested) return;
      o.interested = true;
      o.interestedAt = _now();
    });
  }

  @override
  Future<void> withdrawInterest(String id) async {
    await _wait();
    _respond(id, (o) {
      o.interested = false;
      o.interestedAt = null;
    });
  }

  @override
  Future<void> dismissOffer(String id) async {
    await _wait();
    _catchUp();
    final o = _find(id);
    o.dismissed = true;
    if (o.closedAt == null) {
      o.interested = false;
      o.interestedAt = null;
    }
  }

  @override
  Future<DriverWeek> week({DateTime? start}) async {
    await _wait();
    _catchUp();
    final now = _now();
    final from = dateOnly(start ?? weekStart(now));
    final to = addDays(from, 6);
    final rides = [
      for (final r in _rides)
        if (!r.date.isBefore(from) && !r.date.isAfter(to))
          WeekRide(
            id: r.id,
            seriesId: r.seriesId,
            date: r.date,
            weekday: r.weekday,
            direction: r.direction,
            pickupTime: r.pickupTime,
            dropTime: r.dropTime,
            riderName: r.riderName,
            fare: r.fare,
            isNew: _isNew(r, now),
            pickupLabel: r.pickupLabel,
            dropLabel: r.dropLabel,
          ),
    ]..sort((a, b) => a.date != b.date ? a.date.compareTo(b.date) : a.pickupTime.compareTo(b.pickupTime));
    return DriverWeek(weekStart: from, weekEnd: to, rides: rides);
  }

  bool _isNew(WeekRide r, DateTime now) {
    for (final o in _offers) {
      if (o.seriesId == r.seriesId && o.wonAt != null) return now.difference(o.wonAt!) < const Duration(hours: 24);
    }
    return false;
  }

  // ---------------------------------------------------------------------------
  // D1: sign-up, documents, vehicle, day off, review, plan, payment.
  // ---------------------------------------------------------------------------

  /// Drivers by phone.
  final Map<String, _FakeDriver> _drivers = {};

  /// The signed-in number.
  String? _phone;
  _FakeDriver? _lastCreated;
  int _references = 20417;
  final _pushes = StreamController<String>.broadcast();

  /// How many of the next uploads fail ("Upload failed. Try again.").
  int failNextUploads = 0;

  /// Kinds uploaded, in order (tests).
  final List<String> uploadedKinds = [];

  _FakeDriver? get _current => _phone == null ? null : _drivers[_phone];

  /// The driver the admin hooks act on: the signed-in one, else the newest.
  _FakeDriver get _target {
    final d = _current ?? _lastCreated;
    if (d == null) throw StateError('No driver to review: sign in or sign up first');
    return d;
  }

  void _seedRavi() {
    final now = _now();
    final ravi = _FakeDriver(id: 'drv_ravi', phone: demoPhone, name: demoName, city: 'blr', reference: 'DRV-10003')
      ..status = DriverStatus.live
      ..upiId = 'ravi@okaxis'
      ..dayOffSet = true
      ..subscriptionUntil = DateTime(now.year, now.month + 2, 0)
      ..vehicle = const VehicleDetails(
        type: demoVehicleType,
        makeModel: 'Bajaj RE',
        year: 2021,
        plate: 'KA 05 AB 1234',
        colour: 'Green',
        cngOrElectric: true,
        reviewState: ItemState.verified,
      );
    for (final k in ItemKind.all) {
      ravi.items[k] = _FakeItem()..state = ItemState.verified;
    }
    _drivers[demoPhone] = ravi;
  }

  static const _plans = {
    'bike': ('Bike plan', 799),
    'auto': ('Auto plan', 1199),
    'cab_hatchback': ('Hatchback plan', 1999),
    'cab_sedan': ('Sedan plan', 2299),
    'cab_suv': ('SUV plan', 2799),
  };

  DriverPlan? _planFor(VehicleDetails? v) {
    if (v == null) return null;
    final p = _plans[v.rideType];
    if (p == null) return null;
    return DriverPlan(vehicleType: v.type, cabClass: v.cabClass, label: p.$1, monthlyPrice: p.$2);
  }

  _FakeDriver _signedInDriver() {
    if (_phone == null) throw const ApiException('unauthorized', 'Sign in first');
    final d = _drivers[_phone];
    if (d == null) throw const ApiException('not_registered', 'Sign up as a driver first.');
    return d;
  }

  Driver _driverView(_FakeDriver d) {
    final today = dateOnly(_now());
    final items = <DriverItem>[];
    final missing = <String>[];
    for (final k in ItemKind.all) {
      final i = d.items[k] ?? _FakeItem();
      items.add(DriverItem(
        kind: k,
        label: ItemKind.label(k),
        state: i.state,
        rejectReason: i.reason,
        value: k == ItemKind.upi ? d.upiId : null,
        storagePath: i.path,
        uploadedAt: i.uploadedAt,
      ));
      if (i.state == ItemState.missing || i.state == ItemState.rejected) missing.add(k);
    }
    if (!d.dayOffSet) missing.add(ItemKind.dayOff);
    final v = d.vehicle;
    final vi = d.items[ItemKind.vehicle];
    final until = d.subscriptionUntil;
    return Driver(
      id: d.id,
      name: d.name,
      phone: d.phone,
      city: d.city,
      status: d.status,
      rejectReason: d.rejectReason,
      reference: d.reference,
      upiId: d.upiId,
      dayOff: d.dayOff,
      dayOffSet: d.dayOffSet,
      subscriptionUntil: until,
      eligible: d.status == DriverStatus.live && until != null && !until.isBefore(today),
      items: items,
      missing: missing,
      vehicle: v == null
          ? null
          : VehicleDetails(
              type: v.type,
              cabClass: v.cabClass,
              makeModel: v.makeModel,
              year: v.year,
              plate: v.plate,
              colour: v.colour,
              ac: v.ac,
              cngOrElectric: v.cngOrElectric,
              reviewState: vi?.state ?? ItemState.uploaded,
              rejectReason: vi?.reason,
            ),
      plan: _planFor(v),
      latestPayment: d.payments.isEmpty ? null : d.payments.last,
    );
  }

  DriverItem _itemView(_FakeDriver d, String kind) => _driverView(d).item(kind);

  void _checkEditable(_FakeDriver d, String kind) {
    final editable = switch (d.status) {
      DriverStatus.signingUp => true,
      DriverStatus.changesNeeded => (d.items[kind]?.state ?? ItemState.missing) != ItemState.verified,
      _ => false,
    };
    if (!editable) throw ApiException('locked', "This can't be changed now.", state: _driverView(d));
  }

  void _push(String kind) => _pushes.add(kind);

  @override
  Stream<String> get pushes => _pushes.stream;

  @override
  Future<Driver> driverSignup({required String name, required String city, required bool termsAccepted}) async {
    await _wait();
    final phone = _phone;
    if (phone == null) throw const ApiException('unauthorized', 'Sign in first');
    if (phone == riderPhone) {
      throw const ApiException('phone_in_use_as_rider', 'This number has a rider account. Use another number to drive.');
    }
    if (_drivers.containsKey(phone)) throw const ApiException('already_registered', "You're already registered. Log in instead.");
    final n = name.trim().replaceAll(RegExp(r'\s+'), ' ');
    if (n.length < 2) throw const ApiException('invalid_name', 'Enter your full name as on your licence.');
    final c = city.trim().toLowerCase();
    if (c != 'blr' && c != 'bengaluru') throw const ApiException('city_not_served', "We don't drive in that city yet.");
    if (!termsAccepted) throw const ApiException('terms_not_accepted', 'Agree to the driver terms to continue.');
    final d = _FakeDriver(id: 'drv_${++_ids}', phone: phone, name: n, city: 'blr', reference: 'DRV-${_references++}');
    _drivers[phone] = d;
    _lastCreated = d;
    _profile = DriverProfile(phone: phone, name: n);
    return _driverView(d);
  }

  @override
  Future<Driver> me() async {
    await _wait();
    _catchUpReview();
    return _driverView(_signedInDriver());
  }

  UploadTarget _uploadTarget(_FakeDriver d, String kind, String contentType) {
    if (!ItemKind.all.contains(kind) || !ItemKind.isFile(kind)) {
      throw ApiException('bad_kind', 'Unknown item: $kind', state: _driverView(d));
    }
    if (contentType != 'image/jpeg' && contentType != 'image/png') {
      throw ApiException('bad_content_type', 'Use a JPEG or PNG photo.', state: _driverView(d));
    }
    _checkEditable(d, kind);
    final path = 'driver-docs/${d.id}/$kind';
    return UploadTarget(
      kind: kind,
      uploadUrl: 'https://storage.demo/$path?op=put',
      storagePath: path,
      contentType: contentType,
      expiresAt: _now().add(const Duration(minutes: 10)),
    );
  }

  @override
  Future<UploadTarget> uploadUrl(String kind, String contentType) async {
    await _wait();
    return _uploadTarget(_signedInDriver(), kind, contentType);
  }

  @override
  Future<DriverItem> uploadDocument(String kind, Uint8List bytes, String contentType) async {
    await _wait();
    final d = _signedInDriver();
    final target = _uploadTarget(d, kind, contentType);
    if (bytes.length > target.maxBytes) {
      throw const ApiException('file_too_large', 'That photo is over 5 MB. Try a smaller one.');
    }
    if (failNextUploads > 0) {
      failNextUploads--;
      throw const ApiException('upload_failed', 'Upload failed. Try again.');
    }
    d.items[kind] = _FakeItem()
      ..state = ItemState.uploaded
      ..path = target.storagePath
      ..uploadedAt = _now()
      ..bytes = bytes;
    uploadedKinds.add(kind);
    return _itemView(d, kind);
  }

  static final _upiPattern = RegExp(r'^[a-zA-Z0-9][a-zA-Z0-9._-]{1,255}@[a-zA-Z][a-zA-Z0-9]{1,63}$');

  @override
  Future<DriverItem> setUpi(String upiId) async {
    await _wait();
    final d = _signedInDriver();
    _checkEditable(d, ItemKind.upi);
    final v = upiId.trim();
    if (!_upiPattern.hasMatch(v)) throw ApiException('bad_upi', 'Enter a UPI ID like name@bank.', state: _driverView(d));
    d.upiId = v;
    d.items[ItemKind.upi] = _FakeItem()
      ..state = ItemState.uploaded
      ..uploadedAt = _now();
    return _itemView(d, ItemKind.upi);
  }

  static String _plateKey(String plate) => plate.toUpperCase().replaceAll(RegExp(r'[\s-]'), '');

  @override
  Future<VehicleDetails> setVehicle({
    required String type,
    String? cabClass,
    required String makeModel,
    required int year,
    required String plate,
    required String colour,
    required bool ac,
    required bool cngOrElectric,
  }) async {
    await _wait();
    final d = _signedInDriver();
    _checkEditable(d, ItemKind.vehicle);
    final kind = type.startsWith('cab_') ? 'cab' : type;
    final cls = type.startsWith('cab_') ? (cabClass ?? type.substring(4)) : cabClass;
    if (!const {'bike', 'auto', 'cab'}.contains(kind)) {
      throw ApiException('bad_vehicle_type', 'Choose bike, auto or cab.', state: _driverView(d));
    }
    if (kind == 'cab' && !const {'hatchback', 'sedan', 'suv'}.contains(cls)) {
      throw ApiException('bad_cab_class', 'Choose hatchback, sedan or SUV.', state: _driverView(d));
    }
    final normal = plate.trim().toUpperCase().replaceAll(RegExp(r'\s+'), ' ');
    if (!RegExp(r'^[A-Z0-9][A-Z0-9 -]{3,14}$').hasMatch(normal)) {
      throw ApiException('bad_plate', 'Enter the number plate as on your RC.', state: _driverView(d));
    }
    if (year < 1990 || year > _now().year + 1) {
      throw ApiException('bad_year', 'Enter the year your vehicle was made.', state: _driverView(d));
    }
    for (final other in _drivers.values) {
      final p = other.vehicle?.plate;
      if (other != d && p != null && _plateKey(p) == _plateKey(normal)) {
        throw ApiException('plate_in_use', 'This number plate is already registered. Call support if it is yours.',
            state: _driverView(d));
      }
    }
    d.vehicle = VehicleDetails(
      type: kind,
      cabClass: kind == 'cab' ? cls : null,
      makeModel: makeModel.trim(),
      year: year,
      plate: normal,
      colour: colour.trim(),
      ac: kind == 'cab' && ac,
      cngOrElectric: cngOrElectric,
    );
    d.items[ItemKind.vehicle] = _FakeItem()
      ..state = ItemState.uploaded
      ..uploadedAt = _now();
    return _driverView(d).vehicle!;
  }

  @override
  Future<Driver> setDayOff(int? weekday) async {
    await _wait();
    final d = _signedInDriver();
    if (d.status != DriverStatus.signingUp) throw ApiException('locked', "This can't be changed now.", state: _driverView(d));
    if (weekday != null && (weekday < 1 || weekday > 7)) {
      throw ApiException('bad_weekday', 'Pick a day.', state: _driverView(d));
    }
    d.dayOff = weekday;
    d.dayOffSet = true;
    return _driverView(d);
  }

  @override
  Future<Driver> submitForReview() async {
    await _wait();
    final d = _signedInDriver();
    if (d.status != DriverStatus.signingUp && d.status != DriverStatus.changesNeeded) {
      throw ApiException('wrong_status', "You've already submitted.", state: _driverView(d));
    }
    final view = _driverView(d);
    if (view.missing.isNotEmpty) {
      throw ApiException('incomplete', 'Add these first: ${view.missing.map(ItemKind.label).join(', ')}.', state: view);
    }
    d.status = DriverStatus.submitted;
    d.submittedAt = _now();
    final after = autoReviewAfter;
    if (after != null) {
      Timer(after, () {
        if (d.status == DriverStatus.submitted) _approve(d);
      });
    }
    return _driverView(d);
  }

  /// The period a claim now would pay for: today to the month's end (the
  /// next month for a live driver).
  (DateTime, DateTime) _period(_FakeDriver d) {
    final today = dateOnly(_now());
    if (d.status == DriverStatus.live) {
      return (DateTime(today.year, today.month + 1, 1), DateTime(today.year, today.month + 2, 0));
    }
    return (today, DateTime(today.year, today.month + 1, 0));
  }

  PlanQuote _quote(_FakeDriver d) {
    const shown = {DriverStatus.approved, DriverStatus.live, DriverStatus.paused};
    final plan = _planFor(d.vehicle);
    if (!shown.contains(d.status) || plan == null) {
      throw ApiException('not_approved', "Your plan shows once you're approved.", state: _driverView(d));
    }
    final (start, end) = _period(d);
    final daysInMonth = DateTime(start.year, start.month + 1, 0).day;
    final daysLeft = end.day - start.day + 1;
    return PlanQuote(
      plan: plan,
      amountDue: (plan.monthlyPrice * daysLeft + daysInMonth - 1) ~/ daysInMonth,
      periodStart: start,
      periodEnd: end,
      daysLeft: daysLeft,
      daysInMonth: daysInMonth,
      reference: d.reference,
      payeeUpi: payeeUpi,
      status: d.status,
      latestPayment: d.payments.isEmpty ? null : d.payments.last,
    );
  }

  @override
  Future<PlanQuote> plan() async {
    await _wait();
    _catchUpReview();
    return _quote(_signedInDriver());
  }

  @override
  Future<SubscriptionPayment> claimPayment({DateTime? periodStart}) async {
    await _wait();
    _catchUpReview();
    final d = _signedInDriver();
    if (d.status != DriverStatus.approved && d.status != DriverStatus.paused) {
      throw ApiException('not_approved', 'Claims open once you are approved.', state: _driverView(d));
    }
    final quote = _quote(d);
    final start = periodStart == null ? quote.periodStart : dateOnly(periodStart);
    for (final p in d.payments) {
      if (p.status == PaymentStatus.claimed || (p.status == PaymentStatus.confirmed && sameDay(p.periodStart, start))) {
        throw ApiException('already_claimed', "You've already told us about this payment.", state: p);
      }
    }
    final p = SubscriptionPayment(
      id: 'pay_${++_ids}',
      periodStart: start,
      periodEnd: quote.periodEnd,
      amount: quote.amountDue,
      reference: d.reference,
      status: PaymentStatus.claimed,
      claimedAt: _now(),
    );
    d.payments.add(p);
    final after = autoReviewAfter;
    if (after != null) {
      Timer(after, () {
        if (identical(d.payments.last, p)) _confirm(d);
      });
    }
    return p;
  }

  /// Demo mode: what the pretend admin has done by now.
  void _catchUpReview() {
    final after = autoReviewAfter;
    final d = _current;
    if (after == null || d == null) return;
    final now = _now();
    final submitted = d.submittedAt;
    if (d.status == DriverStatus.submitted && submitted != null && !now.isBefore(submitted.add(after))) _approve(d);
    final last = d.payments.isEmpty ? null : d.payments.last;
    final claimed = last?.claimedAt;
    if (last != null && last.status == PaymentStatus.claimed && claimed != null && !now.isBefore(claimed.add(after))) {
      _confirm(d);
    }
  }

  void _approve(_FakeDriver d) {
    for (final k in ItemKind.all) {
      (d.items[k] ??= _FakeItem())
        ..state = ItemState.verified
        ..reason = null;
    }
    d.status = DriverStatus.approved;
    _push('driver_approved');
  }

  bool _confirm(_FakeDriver d) {
    final i = d.payments.lastIndexWhere((p) => p.status == PaymentStatus.claimed);
    if (i < 0) return false;
    final p = d.payments[i];
    d.payments[i] = SubscriptionPayment(
      id: p.id,
      periodStart: p.periodStart,
      periodEnd: p.periodEnd,
      amount: p.amount,
      reference: p.reference,
      status: PaymentStatus.confirmed,
      claimedAt: p.claimedAt,
      reviewedAt: _now(),
    );
    d.subscriptionUntil = p.periodEnd;
    if (d.status == DriverStatus.approved || d.status == DriverStatus.paused) d.status = DriverStatus.live;
    _push('driver_live');
    return true;
  }

  /// Admin: verifies every item and approves the driver ("You're approved").
  void adminApprove() => _approve(_target);

  /// Admin: rejects [item] (a kind, e.g. `licence`) with [reason] and
  /// verifies everything else that was uploaded: the driver moves to
  /// `changes_needed`. Call again to reject more items.
  void adminRequestChanges(String item, String reason) {
    final d = _target;
    for (final i in d.items.values) {
      if (i.state == ItemState.uploaded) i.state = ItemState.verified;
    }
    (d.items[item] ??= _FakeItem())
      ..state = ItemState.rejected
      ..reason = reason;
    d.status = DriverStatus.changesNeeded;
    _push('driver_changes_needed');
  }

  /// Admin: turns the application down for good.
  void adminReject(String reason) {
    final d = _target;
    d.status = DriverStatus.rejected;
    d.rejectReason = reason;
    _push('driver_rejected');
  }

  /// Admin: confirms the open payment claim; the driver goes live.
  void adminConfirmPayment() {
    if (!_confirm(_target)) throw StateError('No claimed payment to confirm');
  }

  /// Admin: rejects the open payment claim ("We couldn't find your payment").
  void adminRejectPayment(String reason) {
    final d = _target;
    final i = d.payments.lastIndexWhere((p) => p.status == PaymentStatus.claimed);
    if (i < 0) throw StateError('No claimed payment to reject');
    final p = d.payments[i];
    d.payments[i] = SubscriptionPayment(
      id: p.id,
      periodStart: p.periodStart,
      periodEnd: p.periodEnd,
      amount: p.amount,
      reference: p.reference,
      status: PaymentStatus.rejected,
      claimedAt: p.claimedAt,
      reviewedAt: _now(),
      rejectReason: reason,
    );
    _push('driver_payment_rejected');
  }

  @override
  Future<void> registerDevice(String pushToken, String platform) async {
    await _wait();
    pushTokens.add(pushToken);
  }
}

class _FakeOffer {
  _FakeOffer({
    required this.id,
    required this.kind,
    required this.seriesId,
    required this.openedAt,
    required this.expiresAt,
    required this.rider,
    required this.pickup,
    required this.drop,
    required this.startDate,
    required this.distanceKm,
    required this.travelMinutes,
    required this.fare,
    required this.ridesPerMonth,
    required this.payment,
    required this.slots,
  });

  final String id;
  final OfferKind kind;
  final String seriesId;
  final DateTime openedAt;
  final DateTime expiresAt;
  final OfferRider rider;
  final OfferPlace pickup;
  final OfferPlace drop;
  final DateTime startDate;
  final double distanceKm;
  final int travelMinutes;
  final int fare;
  final int ridesPerMonth;
  final PaymentMethod payment;
  final List<OfferSlot> slots;

  bool interested = false;
  DateTime? interestedAt;
  bool dismissed = false;
  DateTime? closedAt;
  String? outcome;
  String? result;
  DateTime? wonAt;

  /// "Priya Sharma" in the week, from "Priya S." on the offer.
  String get riderFullName => switch (rider.firstName) {
        'Priya' => 'Priya Sharma',
        'Kavya' => 'Kavya Nair',
        _ => rider.name,
      };
}

class _FakeDriver {
  _FakeDriver({required this.id, required this.phone, required this.name, required this.city, required this.reference});

  final String id;
  final String phone;
  final String name;
  final String city;
  final String reference;
  DriverStatus status = DriverStatus.signingUp;
  String? rejectReason;
  String? upiId;
  int? dayOff;
  bool dayOffSet = false;
  DateTime? subscriptionUntil;
  DateTime? submittedAt;
  VehicleDetails? vehicle;
  final Map<String, _FakeItem> items = {};
  final List<SubscriptionPayment> payments = [];
}

class _FakeItem {
  ItemState state = ItemState.missing;
  String? reason;
  String? path;
  DateTime? uploadedAt;
  Uint8List? bytes;
}
