import 'package:flutter/material.dart';

import 'tokens.dart';

/// Stylised route map, as drawn on the design canvas. A real map SDK
/// replaces this when maps are wired up; until then it stays a picture.
class RouteMapPreview extends StatelessWidget {
  const RouteMapPreview({super.key, required this.label, this.height = 170});

  final String label;
  final double height;

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(16),
      child: SizedBox(
        height: height,
        width: double.infinity,
        child: Stack(
          children: [
            Positioned.fill(child: CustomPaint(painter: _RoutePainter())),
            Positioned(
              left: 12,
              top: 12,
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                decoration: BoxDecoration(color: Colors.white, borderRadius: BorderRadius.circular(8)),
                child: Text(label, key: const Key('match.trip'), style: AppText.body(12, weight: FontWeight.w600)),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _RoutePainter extends CustomPainter {
  @override
  void paint(Canvas canvas, Size size) {
    // Drawn on a 358 x 170 design grid, stretched to fit.
    final sx = size.width / 358;
    final sy = size.height / 170;
    canvas.drawRect(Offset.zero & size, Paint()..color = AppColors.mapGround);
    final road = Paint()
      ..color = Colors.white
      ..strokeWidth = 9;
    for (final y in [40.0, 95.0, 145.0]) {
      canvas.drawLine(Offset(0, y * sy), Offset(size.width, y * sy), road);
    }
    for (final x in [60.0, 170.0, 280.0]) {
      canvas.drawLine(Offset(x * sx, 0), Offset(x * sx, size.height), road);
    }
    road.strokeWidth = 4;
    canvas.drawLine(Offset(0, 68 * sy), Offset(size.width, 68 * sy), road);
    for (final x in [115.0, 225.0]) {
      canvas.drawLine(Offset(x * sx, 0), Offset(x * sx, size.height), road);
    }
    canvas.drawRRect(
      RRect.fromRectAndRadius(Rect.fromLTWH(180 * sx, 104 * sy, 90 * sx, 34 * sy), const Radius.circular(6)),
      Paint()..color = AppColors.mapPark,
    );
    final route = Path()
      ..moveTo(60 * sx, 145 * sy)
      ..lineTo(60 * sx, 95 * sy)
      ..lineTo(170 * sx, 95 * sy)
      ..lineTo(170 * sx, 40 * sy)
      ..lineTo(290 * sx, 40 * sy);
    canvas.drawPath(
      route,
      Paint()
        ..color = AppColors.green
        ..style = PaintingStyle.stroke
        ..strokeWidth = 6
        ..strokeCap = StrokeCap.round
        ..strokeJoin = StrokeJoin.round,
    );
    final start = Offset(60 * sx, 145 * sy);
    canvas.drawCircle(start, 9, Paint()..color = Colors.white);
    canvas.drawCircle(
      start,
      9,
      Paint()
        ..color = AppColors.green
        ..style = PaintingStyle.stroke
        ..strokeWidth = 4,
    );
    canvas.drawRRect(
      RRect.fromRectAndRadius(Rect.fromCenter(center: Offset(290 * sx, 40 * sy), width: 16, height: 16), const Radius.circular(3)),
      Paint()..color = AppColors.ink,
    );
  }

  @override
  bool shouldRepaint(covariant CustomPainter oldDelegate) => false;
}

/// Green header art on the Welcome screen.
class WelcomeArt extends StatelessWidget {
  const WelcomeArt({super.key, this.height = 300});
  final double height;

  @override
  Widget build(BuildContext context) {
    return SizedBox(height: height, width: double.infinity, child: CustomPaint(painter: _WelcomePainter()));
  }
}

class _WelcomePainter extends CustomPainter {
  @override
  void paint(Canvas canvas, Size size) {
    final sx = size.width / 390;
    final sy = size.height / 300;
    canvas.drawRect(Offset.zero & size, Paint()..color = AppColors.green);
    final wide = Paint()
      ..color = AppColors.welcomeRoad
      ..strokeWidth = 14;
    final thin = Paint()
      ..color = AppColors.welcomeRoadThin
      ..strokeWidth = 6;
    for (final y in [80.0, 190.0]) {
      canvas.drawLine(Offset(0, y * sy), Offset(size.width, y * sy), wide);
    }
    for (final x in [90.0, 250.0]) {
      canvas.drawLine(Offset(x * sx, 0), Offset(x * sx, size.height), wide);
    }
    for (final y in [135.0, 245.0]) {
      canvas.drawLine(Offset(0, y * sy), Offset(size.width, y * sy), thin);
    }
    for (final x in [170.0, 330.0]) {
      canvas.drawLine(Offset(x * sx, 0), Offset(x * sx, size.height), thin);
    }
    final route = Path()
      ..moveTo(40 * sx, 245 * sy)
      ..lineTo(170 * sx, 245 * sy)
      ..lineTo(170 * sx, 135 * sy)
      ..lineTo(250 * sx, 135 * sy)
      ..lineTo(250 * sx, 80 * sy)
      ..lineTo(360 * sx, 80 * sy);
    canvas.drawPath(
      route,
      Paint()
        ..color = AppColors.yellow
        ..style = PaintingStyle.stroke
        ..strokeWidth = 7
        ..strokeCap = StrokeCap.round
        ..strokeJoin = StrokeJoin.round,
    );
    final start = Offset(40 * sx, 245 * sy);
    canvas.drawCircle(start, 12, Paint()..color = AppColors.green);
    canvas.drawCircle(
      start,
      12,
      Paint()
        ..color = AppColors.yellow
        ..style = PaintingStyle.stroke
        ..strokeWidth = 5,
    );
    canvas.drawRRect(
      RRect.fromRectAndRadius(Rect.fromLTWH(350 * sx, 70 * sy, 20, 20), const Radius.circular(4)),
      Paint()..color = Colors.white,
    );
  }

  @override
  bool shouldRepaint(covariant CustomPainter oldDelegate) => false;
}
