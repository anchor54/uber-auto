import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/models.dart';
import 'package:rider/ui/ride_map.dart';

/// What each map phase draws: one route at a time.
void main() {
  const pickup = Place(id: 'p', title: 'Home', lat: 12.90, lng: 77.60);
  const drop = Place(id: 'd', title: 'Office', lat: 12.94, lng: 77.70);
  // Encoded [(12.90, 77.60), (12.92, 77.60), (12.94, 77.70)]: north, then east.
  const commute = '_pvmA_gsxM_|B?_|B_pR';
  // Encoded [(12.92, 77.65), (12.90, 77.65), (12.90, 77.60)]: south, then west.
  const approach = '_mzmAo_}xM~{B??nwH';

  List<(double, double)> pts(List<GeoPoint> p) => [for (final x in p) (x.lat, x.lng)];

  test('the encoded test routes decode as described', () {
    final c = MapGeometry.of(phase: MapPhase.preview, pickup: pickup, drop: drop, route: commute);
    expect(pts(c.line), [(12.90, 77.60), (12.92, 77.60), (12.94, 77.70)]);
  });

  test('preview: the commute route with both pins, no driver', () {
    final g = MapGeometry.of(phase: MapPhase.preview, pickup: pickup, drop: drop, route: commute);
    expect(g.pins.map((e) => e.$2), [true, false]);
    expect(g.driver, isNull);
    expect(g.lineIsFaint, isFalse);
  });

  test('coming to the rider: only the approach road and the pickup, no commute route or drop-off', () {
    const driver = GeoPoint(12.92, 77.65);
    final g = MapGeometry.of(
      phase: MapPhase.approach,
      pickup: pickup,
      drop: drop,
      route: commute,
      approachRoute: approach,
      driver: driver,
    );
    expect(pts(g.line), [(12.92, 77.65), (12.90, 77.65), (12.90, 77.60)]);
    expect(g.pins.map((e) => e.$2), [true], reason: 'pickup only');
    expect(g.lineIsFaint, isTrue);
    expect(g.all.any((p) => p.lng == drop.lng), isFalse, reason: 'the drop-off is not in view');
  });

  test('coming to the rider: the part already driven is gone', () {
    final g = MapGeometry.of(phase: MapPhase.approach, pickup: pickup, drop: drop, approachRoute: approach, driver: const GeoPoint(12.90, 77.63));
    expect(g.line.first.lat, closeTo(12.90, 1e-9));
    expect(g.line.first.lng, closeTo(77.63, 1e-9));
    expect(pts(g.line.sublist(1)), [(12.90, 77.60)]);
  });

  test('coming to the rider without a road route: a straight line from the driver; nothing before a position', () {
    const driver = GeoPoint(12.92, 77.65);
    final g = MapGeometry.of(phase: MapPhase.approach, pickup: pickup, drop: drop, driver: driver);
    expect(pts(g.line), [(12.92, 77.65), (12.90, 77.60)]);
    expect(MapGeometry.of(phase: MapPhase.approach, pickup: pickup, drop: drop).line, isEmpty);
  });

  test('riding: only the commute route still ahead and the drop-off, no approach or pickup', () {
    final g = MapGeometry.of(
      phase: MapPhase.ride,
      pickup: pickup,
      drop: drop,
      route: commute,
      approachRoute: approach,
      driver: const GeoPoint(12.93, 77.65),
    );
    expect(g.pins.map((e) => e.$2), [false], reason: 'drop-off only');
    expect(g.line.first.lat, greaterThan(12.92), reason: 'the first (northbound) leg is behind');
    expect(pts([g.line.last]), [(12.94, 77.70)]);
    expect(g.lineIsFaint, isFalse);
  });
}
