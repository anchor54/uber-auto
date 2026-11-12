import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/models.dart';

CommuteRequest _request({Set<RidePreference> prefs = const {}, RideType type = RideType.cabSedan}) => CommuteRequest(
      pickup: demoPlaces.first,
      drop: demoPlaces[1],
      repeat: CommuteRepeat.everyWeekday,
      weekdays: const {1, 2, 3, 4, 5},
      timing: TimingMode.pickupAt,
      time: 520,
      pickupTime: 520,
      returnTrip: true,
      returnTime: 1110,
      startDate: DateTime(2026, 10, 22),
      rideType: type,
      preferences: prefs,
    );

void main() {
  late FakeRiderApi api;
  setUp(() => api = FakeRiderApi());

  group('OTP', () {
    test('rejects numbers that are not Indian mobiles', () {
      expect(() => api.sendOtp('12345'), throwsA(isA<ApiException>()));
      expect(() => api.sendOtp('1886054321'), throwsA(isA<ApiException>()));
    });

    test('accepts only the demo code', () async {
      await api.sendOtp('9886054321');
      expect(() => api.verifyOtp('9886054321', '000000'), throwsA(isA<ApiException>()));
      final profile = await api.verifyOtp('9886054321', FakeRiderApi.demoOtp);
      expect(profile.phone, '9886054321');
    });
  });

  test('place search matches title or detail, case-insensitive', () async {
    expect((await api.searchPlaces('hsr')).map((p) => p.id), ['home']);
    expect((await api.searchPlaces('EMBASSY')).map((p) => p.id), ['etv']);
    expect(await api.searchPlaces(''), demoPlaces);
    expect(await api.searchPlaces('nowhere'), isEmpty);
  });

  group('booking options follow the ride-type config', () {
    test('default config offers every type, sedan first choice for commutes', () async {
      final o = await api.bookingOptions(
        flow: BookingFlow.recurringWeekday,
        pickup: demoPlaces[0],
        drop: demoPlaces[1],
        pickupTime: 520,
      );
      expect(o.options.map((e) => e.type), RideType.values);
      expect(o.defaultType, RideType.cabSedan);
      expect(o.optionFor(RideType.cabSedan)!.fare, 250);
    });

    test('a city config can switch ride types off per flow', () async {
      final limited = FakeRiderApi(config: {
        ...defaultRideTypeConfig,
        BookingFlow.recurringWeekly: const RideTypeConfig(
          allowed: [RideType.auto, RideType.cabSedan],
          defaultType: RideType.auto,
        ),
      });
      final weekly = await limited.bookingOptions(
        flow: BookingFlow.recurringWeekly,
        pickup: demoPlaces[0],
        drop: demoPlaces[1],
        pickupTime: 520,
      );
      expect(weekly.options.map((e) => e.type), [RideType.auto, RideType.cabSedan]);
      expect(weekly.defaultType, RideType.auto);
    });
  });

  group('matching', () {
    test('returns three drivers of the chosen type, best first', () async {
      final matches = await api.findDrivers(_request());
      expect(matches, hasLength(3));
      expect(matches.first.driver.name, 'Ravi Kumar');
      expect(matches.first.fare, 250);
      expect(matches.first.reasons.first, 'Same driver every day, both directions');
    });

    test('the woman-driver preference finds nobody in demo mode', () async {
      expect(await api.findDrivers(_request(prefs: {RidePreference.womanDriver})), isEmpty);
    });
  });

  test('confirming stores the commute and Home shows it', () async {
    final match = (await api.findDrivers(_request())).first;
    await api.confirmCommute(request: _request(), match: match, usualPayment: PaymentMethod.cash);
    final home = await api.home();
    expect(home.commute, isNotNull);
    expect(home.commute!.driver.name, 'Ravi Kumar');
    expect(home.commute!.usualPayment, PaymentMethod.cash);
    expect(home.commute!.status, CommuteStatus.active);
  });

  test('waitlist keeps the request', () async {
    await api.joinWaitlist(_request());
    expect(api.waitlist, hasLength(1));
  });
}
