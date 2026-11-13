import 'package:flutter/widgets.dart';
import 'package:path_drawing/path_drawing.dart';

/// Small line icons drawn from the SVG paths used on the design canvas.
/// Drawn with a painter (not an icon font) so screenshots in tests match
/// the app exactly.
class AppIconData {
  const AppIconData({
    required this.width,
    required this.height,
    required this.parts,
    this.strokeWidth = 2,
  });

  final double width;
  final double height;
  final List<IconPart> parts;
  final double strokeWidth;
}

sealed class IconPart {
  const IconPart();
}

class PathPart extends IconPart {
  const PathPart(this.d, {this.filled = false});
  final String d;
  final bool filled;
}

class CirclePart extends IconPart {
  const CirclePart(this.cx, this.cy, this.r, {this.fillWhite = false});
  final double cx;
  final double cy;
  final double r;
  final bool fillWhite;
}

class RectPart extends IconPart {
  const RectPart(this.x, this.y, this.w, this.h, {this.rx = 0});
  final double x;
  final double y;
  final double w;
  final double h;
  final double rx;
}

abstract final class AppIcons {
  static const back = AppIconData(width: 24, height: 24, parts: [PathPart('M15 18l-6-6 6-6')]);
  static const chevronRight = AppIconData(width: 24, height: 24, parts: [PathPart('M9 6l6 6-6 6')]);
  static const check = AppIconData(width: 24, height: 24, strokeWidth: 2.4, parts: [PathPart('M5 12l5 5 9-10')]);
  static const search = AppIconData(width: 24, height: 24, strokeWidth: 2.2, parts: [CirclePart(11, 11, 7), PathPart('M20 20l-4-4')]);
  static const home = AppIconData(width: 24, height: 24, parts: [PathPart('M3 11l9-8 9 8'), PathPart('M5 10v10h14V10')]);
  static const calendar = AppIconData(width: 24, height: 24, parts: [RectPart(3, 5, 18, 16, rx: 2), PathPart('M3 10h18M8 3v4M16 3v4')]);
  static const user = AppIconData(width: 24, height: 24, parts: [CirclePart(12, 8, 4), PathPart('M4 21c1.5-4 4.5-6 8-6s6.5 2 8 6')]);
  static const repeat = AppIconData(width: 24, height: 24, parts: [
    PathPart('M17 2l4 4-4 4'),
    PathPart('M3 11V9a3 3 0 0 1 3-3h15'),
    PathPart('M7 22l-4-4 4-4'),
    PathPart('M21 13v2a3 3 0 0 1-3 3H3'),
  ]);
  static const pin = AppIconData(width: 24, height: 24, parts: [
    PathPart('M12 21s-7-6.2-7-11a7 7 0 0 1 14 0c0 4.8-7 11-7 11z'),
    CirclePart(12, 10, 2.5),
  ]);
  static const clock = AppIconData(width: 24, height: 24, parts: [CirclePart(12, 12, 9), PathPart('M12 7v5l3 2')]);
  static const star = AppIconData(width: 24, height: 24, strokeWidth: 1.8, parts: [PathPart(_starPath)]);
  static const starFilled = AppIconData(width: 24, height: 24, strokeWidth: 1.8, parts: [PathPart(_starPath, filled: true)]);
  static const _starPath = 'M12 3l2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1L3.2 9.5l6.1-.9z';

  static const bike = AppIconData(width: 48, height: 32, parts: [
    CirclePart(10, 22, 6),
    CirclePart(38, 22, 6),
    PathPart('M10 22l8-12h10l10 12M18 10h-4M28 10l-4 12h-6'),
  ]);
  static const auto = AppIconData(width: 48, height: 32, parts: [
    PathPart('M8 24V12a6 6 0 0 1 6-6h14l8 10h4a2 2 0 0 1 2 2v6z'),
    PathPart('M8 14h22'),
    CirclePart(14, 25, 3.5, fillWhite: true),
    CirclePart(36, 25, 3.5, fillWhite: true),
  ]);
  static const hatchback = AppIconData(width: 56, height: 32, parts: [
    PathPart('M6 22v-6l6-8h20l8 8h8a2 2 0 0 1 2 2v4z'),
    CirclePart(15, 24, 4, fillWhite: true),
    CirclePart(41, 24, 4, fillWhite: true),
  ]);
  static const sedan = AppIconData(width: 56, height: 32, parts: [
    PathPart('M3 22v-5l4-1 7-8h18l9 8 10 1a2 2 0 0 1 2 2v3z'),
    CirclePart(14, 24, 4, fillWhite: true),
    CirclePart(42, 24, 4, fillWhite: true),
  ]);
  static const suv = AppIconData(width: 56, height: 32, parts: [
    PathPart('M4 23V12l4-6h28l8 7h6a2 2 0 0 1 2 2v8z'),
    PathPart('M8 13h34'),
    CirclePart(15, 24, 4, fillWhite: true),
    CirclePart(41, 24, 4, fillWhite: true),
  ]);
}

class AppIcon extends StatelessWidget {
  const AppIcon(this.data, {super.key, this.size = 22, this.color = const Color(0xFF12161A), this.semanticLabel});

  final AppIconData data;

  /// Width in logical pixels; height follows the icon's aspect ratio.
  final double size;
  final Color color;
  final String? semanticLabel;

  @override
  Widget build(BuildContext context) {
    final h = size * data.height / data.width;
    final icon = CustomPaint(size: Size(size, h), painter: _IconPainter(data, color));
    if (semanticLabel == null) return ExcludeSemantics(child: icon);
    return Semantics(label: semanticLabel, child: icon);
  }
}

class _IconPainter extends CustomPainter {
  _IconPainter(this.data, this.color);
  final AppIconData data;
  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    canvas.save();
    canvas.scale(size.width / data.width, size.height / data.height);
    final stroke = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = data.strokeWidth
      ..strokeCap = StrokeCap.round
      ..strokeJoin = StrokeJoin.round
      ..isAntiAlias = true;
    final white = Paint()..color = const Color(0xFFFFFFFF);
    final solid = Paint()..color = color;
    for (final part in data.parts) {
      switch (part) {
        case PathPart(:final d, :final filled):
          final path = parseSvgPathData(d);
          if (filled) canvas.drawPath(path, solid);
          canvas.drawPath(path, stroke);
        case CirclePart(:final cx, :final cy, :final r, :final fillWhite):
          if (fillWhite) canvas.drawCircle(Offset(cx, cy), r, white);
          canvas.drawCircle(Offset(cx, cy), r, stroke);
        case RectPart(:final x, :final y, :final w, :final h, :final rx):
          canvas.drawRRect(RRect.fromRectAndRadius(Rect.fromLTWH(x, y, w, h), Radius.circular(rx)), stroke);
      }
    }
    canvas.restore();
  }

  @override
  bool shouldRepaint(_IconPainter old) => old.data != data || old.color != color;
}
