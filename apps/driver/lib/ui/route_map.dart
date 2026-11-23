import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../core/models.dart';
import 'tokens.dart';

/// A simple dark route picture: pickup and drop-off placed by their
/// coordinates on a street grid. Live maps come with the daily run (D3).
class RouteMap extends StatelessWidget {
  const RouteMap({super.key, required this.pickup, required this.drop, this.height = 150});

  final OfferPlace pickup;
  final OfferPlace drop;
  final double height;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      label: 'Map: ${pickup.label} to ${drop.label}',
      child: ClipRRect(
        borderRadius: BorderRadius.circular(16),
        child: SizedBox(height: height, width: double.infinity, child: CustomPaint(painter: _RoutePainter(pickup, drop))),
      ),
    );
  }
}

class _RoutePainter extends CustomPainter {
  _RoutePainter(this.a, this.b);
  final OfferPlace a;
  final OfferPlace b;

  @override
  void paint(Canvas canvas, Size size) {
    canvas.drawRect(Offset.zero & size, Paint()..color = AppColors.mapGround);
    final road = Paint()
      ..color = AppColors.mapRoad
      ..strokeWidth = 6;
    for (var y = 30.0; y < size.height; y += 50) {
      canvas.drawLine(Offset(0, y), Offset(size.width, y), road);
    }
    for (var x = 40.0; x < size.width; x += 80) {
      canvas.drawLine(Offset(x, 0), Offset(x, size.height), road);
    }
    // Fit both points into the box with a margin; north up.
    final dLng = (b.lng - a.lng).abs();
    final dLat = (b.lat - a.lat).abs();
    final span = math.max(math.max(dLng, dLat), 1e-6);
    Offset at(OfferPlace p) {
      final cx = (a.lng + b.lng) / 2;
      final cy = (a.lat + b.lat) / 2;
      final scale = math.min(size.width, size.height) * 0.7 / span;
      return Offset(size.width / 2 + (p.lng - cx) * scale, size.height / 2 - (p.lat - cy) * scale);
    }

    final p1 = at(a), p2 = at(b);
    final line = Paint()
      ..color = AppColors.yellow
      ..strokeWidth = 4
      ..style = PaintingStyle.stroke
      ..strokeCap = StrokeCap.round;
    final mid = Offset(p1.dx, p2.dy);
    canvas.drawPath(Path()
      ..moveTo(p1.dx, p1.dy)
      ..lineTo(mid.dx, mid.dy)
      ..lineTo(p2.dx, p2.dy), line);
    canvas.drawCircle(p1, 7, Paint()..color = AppColors.green);
    canvas.drawCircle(p2, 7, Paint()..color = AppColors.yellow);
    canvas.drawCircle(p2, 3, Paint()..color = AppColors.ground);
  }

  @override
  bool shouldRepaint(_RoutePainter old) => old.a != a || old.b != b;
}
