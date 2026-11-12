import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/fake_api.dart';
import 'package:rider/core/fares.dart';
import 'package:rider/core/models.dart';

void main() {
  final home = demoPlaces.firstWhere((p) => p.id == 'home');
  final etv = demoPlaces.firstWhere((p) => p.id == 'etv');

  test('demo commute is about 12 km and 38 minutes', () {
    final trip = estimateRoadTrip(home, etv);
    expect(trip.distanceKm, 11.9);
    expect(trip.minutes, 38);
  });

  test('fares per ride type for the demo commute', () {
    expect(suggestedFare(RideType.bike, 11.9), 90);
    expect(suggestedFare(RideType.auto, 11.9), 160);
    expect(suggestedFare(RideType.cabHatchback, 11.9), 215);
    expect(suggestedFare(RideType.cabSedan, 11.9), 250);
    expect(suggestedFare(RideType.cabSuv, 11.9), 330);
  });

  test('short trips pay the minimum fare', () {
    expect(suggestedFare(RideType.bike, 1), 35);
    expect(suggestedFare(RideType.cabSedan, 1), 110);
  });

  test('fares are always multiples of five', () {
    for (final type in RideType.values) {
      for (var km = 0.5; km < 40; km += 0.7) {
        expect(suggestedFare(type, km) % 5, 0, reason: '$type at $km km');
      }
    }
  });

  test('cab fares rise with the class', () {
    expect(suggestedFare(RideType.cabHatchback, 10), lessThan(suggestedFare(RideType.cabSedan, 10)));
    expect(suggestedFare(RideType.cabSedan, 10), lessThan(suggestedFare(RideType.cabSuv, 10)));
  });
}
