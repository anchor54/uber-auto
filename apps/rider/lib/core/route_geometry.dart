import 'dart:math';

import 'models.dart';

/// The part of [route] still ahead of [at]: from the closest point on the
/// route to its end. So the map shows only what's left to drive.
List<GeoPoint> remainingRoute(List<GeoPoint> route, GeoPoint at) {
  if (route.length < 2) return route;
  // Flat projection around [at]: plenty accurate over a city.
  final kx = cos(at.lat * pi / 180);
  var bestD = double.infinity;
  var bestI = 1;
  var bestT = 0.0;
  for (var i = 1; i < route.length; i++) {
    final ax = (route[i - 1].lng - at.lng) * kx, ay = route[i - 1].lat - at.lat;
    final bx = (route[i].lng - at.lng) * kx, by = route[i].lat - at.lat;
    final dx = bx - ax, dy = by - ay;
    final len2 = dx * dx + dy * dy;
    final t = len2 == 0 ? 0.0 : (-(ax * dx + ay * dy) / len2).clamp(0.0, 1.0);
    final px = ax + t * dx, py = ay + t * dy;
    final d = px * px + py * py;
    if (d < bestD) {
      bestD = d;
      bestI = i;
      bestT = t;
    }
  }
  final a = route[bestI - 1], b = route[bestI];
  final onRoute = GeoPoint(a.lat + (b.lat - a.lat) * bestT, a.lng + (b.lng - a.lng) * bestT);
  return [onRoute, ...route.sublist(bestI)];
}
