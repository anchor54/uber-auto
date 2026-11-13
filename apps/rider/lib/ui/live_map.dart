import 'dart:math';

import 'package:flutter/material.dart';

import '../core/models.dart';
import 'tokens.dart';

/// R1's drawn map with the pickup, the drop-off and the driver placed from
/// real coordinates, until a maps SDK is chosen. The three points are fitted
/// into the box; the route is a straight line, like the trip estimate.
class LiveRideMap extends StatelessWidget {
  const LiveRideMap({
    super.key,
    required this.pickup,
    required this.drop,
    this.driver,
    this.towardsDrop = false,
    this.height = 220,
    this.label,
  });

  final Place pickup;
  final Place drop;

  /// The driver's latest position; null hides the marker.
  final GeoPoint? driver;

  /// In the ride: highlight pickup → drop. Before: driver → pickup.
  final bool towardsDrop;
  final double height;
  final String? label;

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(16),
      child: SizedBox(
        height: height,
        width: double.infinity,
        child: Stack(
          children: [
            Positioned.fill(
              child: CustomPaint(
                key: const Key('ride.map'),
                painter: _LiveMapPainter(pickup: pickup, drop: drop, driver: driver, towardsDrop: towardsDrop),
              ),
            ),
            if (label != null)
              Positioned(
                left: 12,
                top: 12,
                child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                  decoration: BoxDecoration(color: Colors.white, borderRadius: BorderRadius.circular(8)),
                  child: Text(label!, style: AppText.body(12, weight: FontWeight.w600)),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _LiveMapPainter extends CustomPainter {
  _LiveMapPainter({required this.pickup, required this.drop, required this.driver, required this.towardsDrop});

  final Place pickup;
  final Place drop;
  final GeoPoint? driver;
  final bool towardsDrop;

  @override
  void paint(Canvas canvas, Size size) {
    canvas.drawRect(Offset.zero & size, Paint()..color = AppColors.mapGround);
    final road = Paint()
      ..color = Colors.white
      ..strokeWidth = 7;
    for (var y = size.height * 0.2; y < size.height; y += size.height * 0.3) {
      canvas.drawLine(Offset(0, y), Offset(size.width, y), road);
    }
    for (var x = size.width * 0.15; x < size.width; x += size.width * 0.28) {
      canvas.drawLine(Offset(x, 0), Offset(x, size.height), road);
    }

    final points = [GeoPoint(pickup.lat, pickup.lng), GeoPoint(drop.lat, drop.lng), if (driver != null) driver!];
    final minLat = points.map((p) => p.lat).reduce(min), maxLat = points.map((p) => p.lat).reduce(max);
    final minLng = points.map((p) => p.lng).reduce(min), maxLng = points.map((p) => p.lng).reduce(max);
    final spanLat = max(maxLat - minLat, 0.005), spanLng = max(maxLng - minLng, 0.005);
    const pad = 28.0;
    Offset at(GeoPoint p) => Offset(
          pad + (p.lng - minLng) / spanLng * (size.width - 2 * pad),
          pad + (maxLat - p.lat) / spanLat * (size.height - 2 * pad),
        );

    final a = at(points[0]), b = at(points[1]);
    final route = Paint()
      ..color = AppColors.green.withValues(alpha: towardsDrop ? 1 : 0.35)
      ..strokeWidth = 5
      ..strokeCap = StrokeCap.round;
    canvas.drawLine(a, b, route);
    final d = driver;
    if (d != null && !towardsDrop) {
      canvas.drawLine(
        at(d),
        a,
        Paint()
          ..color = AppColors.ink
          ..strokeWidth = 3
          ..strokeCap = StrokeCap.round,
      );
    }
    // Pickup: green ring. Drop-off: dark square.
    canvas.drawCircle(a, 9, Paint()..color = Colors.white);
    canvas.drawCircle(a, 9, Paint()
      ..color = AppColors.green
      ..style = PaintingStyle.stroke
      ..strokeWidth = 4);
    canvas.drawRect(Rect.fromCenter(center: b, width: 14, height: 14), Paint()..color = AppColors.ink);
    if (d != null) {
      final c = at(d);
      canvas.drawCircle(c, 13, Paint()..color = AppColors.yellow);
      canvas.drawCircle(c, 13, Paint()
        ..color = AppColors.ink
        ..style = PaintingStyle.stroke
        ..strokeWidth = 2);
      canvas.drawCircle(c, 4, Paint()..color = AppColors.ink);
    }
  }

  @override
  bool shouldRepaint(_LiveMapPainter old) =>
      old.driver?.lat != driver?.lat || old.driver?.lng != driver?.lng || old.towardsDrop != towardsDrop;
}
