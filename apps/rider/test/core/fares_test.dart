import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/fares.dart';
import 'package:rider/core/models.dart';

void main() {
  final home = demoPlaces.firstWhere((p) => p.id == 'home');
  final etv = demoPlaces.firstWhere((p) => p.id == 'etv');

  // The backend's rules (backend/internal/travel): road = straight line x 1.4,
  // minutes at the type's speed rounded up, fare to the nearest rupee.
  test('demo commute is about 9 km and 31 minutes by auto', () {
    final trip = estimateRoadTrip(home, etv);
    expect(trip.distanceKm, 9.0);
    expect(trip.minutes, 31);
  });

  test('fares per ride type for the demo commute', () {
    expect(suggestedFare(RideType.bike, 9.0), 74);
    expect(suggestedFare(RideType.auto, 9.0), 129);
    expect(suggestedFare(RideType.cabHatchback, 9.0), 176);
    expect(suggestedFare(RideType.cabSedan, 9.0), 204);
    expect(suggestedFare(RideType.cabSuv, 9.0), 269);
  });

  test('the spec example: Koramangala to Whitefield by auto is 51 min and Rs 197', () {
    final k = demoPlaces.firstWhere((p) => p.id == 'koramangala');
    final w = demoPlaces.firstWhere((p) => p.id == 'whitefield');
    final km = roadKm(k, w);
    expect(travelMinutes(RideType.auto, km), 51);
    expect(suggestedFare(RideType.auto, km), 197);
  });

  test('short trips pay the minimum fare', () {
    expect(suggestedFare(RideType.bike, 1), 35);
    expect(suggestedFare(RideType.cabSedan, 1), 110);
  });

  test('fares are whole rupees, base plus per km, rounded to the nearest rupee', () {
    expect(suggestedFare(RideType.auto, 10), 140);
    expect(suggestedFare(RideType.auto, 10.04), 140);
    expect(suggestedFare(RideType.auto, 10.05), 141);
  });

  test('cab fares rise with the class', () {
    expect(suggestedFare(RideType.cabHatchback, 10), lessThan(suggestedFare(RideType.cabSedan, 10)));
    expect(suggestedFare(RideType.cabSedan, 10), lessThan(suggestedFare(RideType.cabSuv, 10)));
  });
}
