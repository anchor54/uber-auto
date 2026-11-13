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
    this.rounded = true,
    this.overlap = EdgeInsets.zero,
  });

  /// Rounded corners for a map in a card; false for a full-bleed map.
  final bool rounded;

  /// What covers the map's edges; the drawing keeps clear of it.
  final EdgeInsets overlap;

  final Place pickup;
  final Place drop;

  /// The driver's latest position; null hides the marker.
  final GeoPoint? driver;

  /// In the ride: driver → drop-off. Before: driver → pickup. One route only.
  final bool towardsDrop;
  final double height;
  final String? label;

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(rounded ? 16 : 0),
      child: SizedBox(
        height: height,
        width: double.infinity,
        child: Stack(
          children: [
            Positioned.fill(
              child: CustomPaint(
                key: const Key('ride.map'),
                painter: _LiveMapPainter(pickup: pickup, drop: drop, driver: driver, towardsDrop: towardsDrop, overlap: overlap),
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
  _LiveMapPainter({required this.pickup, required this.drop, required this.driver, required this.towardsDrop, required this.overlap});

  final Place pickup;
  final Place drop;
  final GeoPoint? driver;
  final bool towardsDrop;
  final EdgeInsets overlap;

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

    // One phase at a time, like the real map: coming to the rider (driver →
    // pickup) or riding (driver → drop-off). Never both routes at once.
    final p = GeoPoint(pickup.lat, pickup.lng), dp = GeoPoint(drop.lat, drop.lng);
    final d = driver;
    final pin = towardsDrop ? dp : p;
    final from = d ?? (towardsDrop ? p : null);
    final points = [pin, if (from != null) from];
    final minLat = points.map((x) => x.lat).reduce(min), maxLat = points.map((x) => x.lat).reduce(max);
    final minLng = points.map((x) => x.lng).reduce(min), maxLng = points.map((x) => x.lng).reduce(max);
    final spanLat = max(maxLat - minLat, 0.005), spanLng = max(maxLng - minLng, 0.005);
    const pad = 28.0;
    final box = overlap.deflateRect(Offset.zero & size).deflate(pad);
    Offset at(GeoPoint x) => Offset(
          box.left + (x.lng - minLng) / spanLng * box.width,
          box.top + (maxLat - x.lat) / spanLat * box.height,
        );

    if (from != null) {
      canvas.drawLine(
        at(from),
        at(pin),
        Paint()
          ..color = towardsDrop ? AppColors.green : AppColors.ink
          ..strokeWidth = towardsDrop ? 5 : 3
          ..strokeCap = StrokeCap.round,
      );
    }
    final c = at(pin);
    if (towardsDrop) {
      // Drop-off: dark square.
      canvas.drawRect(Rect.fromCenter(center: c, width: 14, height: 14), Paint()..color = AppColors.ink);
    } else {
      // Pickup: green ring.
      canvas.drawCircle(c, 9, Paint()..color = Colors.white);
      canvas.drawCircle(c, 9, Paint()
        ..color = AppColors.green
        ..style = PaintingStyle.stroke
        ..strokeWidth = 4);
    }
    if (d != null) {
      final dc = at(d);
      canvas.drawCircle(dc, 13, Paint()..color = AppColors.yellow);
      canvas.drawCircle(dc, 13, Paint()
        ..color = AppColors.ink
        ..style = PaintingStyle.stroke
        ..strokeWidth = 2);
      canvas.drawCircle(dc, 4, Paint()..color = AppColors.ink);
    }
  }

  @override
  bool shouldRepaint(_LiveMapPainter old) =>
      old.driver?.lat != driver?.lat || old.driver?.lng != driver?.lng || old.towardsDrop != towardsDrop || old.overlap != overlap;
}
