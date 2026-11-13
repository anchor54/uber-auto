import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/models.dart';
import 'package:rider/core/route_geometry.dart';

void main() {
  // An L: north, then east.
  const route = [GeoPoint(12.90, 77.60), GeoPoint(12.91, 77.60), GeoPoint(12.91, 77.62)];
  List<(double, double)> pts(List<GeoPoint> p) => [for (final x in p) (x.lat, x.lng)];

  test('at the start, the whole route is ahead', () {
    expect(pts(remainingRoute(route, const GeoPoint(12.90, 77.60))), pts(route));
  });

  test('partway up the first leg, it starts where the driver is', () {
    final left = remainingRoute(route, const GeoPoint(12.905, 77.6001));
    expect(left.first.lat, closeTo(12.905, 1e-6));
    expect(left.first.lng, closeTo(77.60, 1e-6), reason: 'snapped onto the route');
    expect(pts(left.sublist(1)), pts(route.sublist(1)));
  });

  test('on the second leg, the first leg is gone', () {
    final left = remainingRoute(route, const GeoPoint(12.91, 77.61));
    expect(left, hasLength(2));
    expect(left.last.lng, 77.62);
  });

  test('at the end, only the end is left', () {
    final left = remainingRoute(route, const GeoPoint(12.91, 77.62));
    expect(left.first.lng, closeTo(77.62, 1e-9));
  });
}
