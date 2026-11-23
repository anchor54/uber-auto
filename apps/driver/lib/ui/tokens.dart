import 'package:flutter/material.dart';

/// Colours from the driver design: dark ground, yellow for actions and
/// countdowns, green for good news (spec "Look and feel").
abstract final class AppColors {
  static const ground = Color(0xFF12161A);
  static const card = Color(0xFF1D2329);
  static const cardRaised = Color(0xFF252D34);
  static const ink = Color(0xFFF4F5F2);
  static const muted = Color(0xFF9AA3AB);
  static const line = Color(0xFF323B43);
  static const divider = Color(0xFF2A3239);
  static const yellow = Color(0xFFF5C451);
  static const onYellow = Color(0xFF12161A);
  static const green = Color(0xFF8FE0B9);
  static const greenDark = Color(0xFF173A2C);
  static const red = Color(0xFFFF8A7A);
  static const mapGround = Color(0xFF232B31);
  static const mapRoad = Color(0xFF323C44);
}

abstract final class AppText {
  static const inter = 'Inter';
  static const grotesk = 'SpaceGrotesk';

  /// Characters the bundled fonts lack (arrows) come from here.
  static const fallback = ['Arrows'];

  static TextStyle display(
    double size, {
    Color color = AppColors.ink,
    FontWeight weight = FontWeight.w600,
    double letterSpacing = -0.5,
    double height = 1.15,
  }) =>
      TextStyle(
        fontFamily: grotesk,
        fontFamilyFallback: fallback,
        fontSize: size,
        fontWeight: weight,
        color: color,
        letterSpacing: letterSpacing,
        height: height,
      );

  static TextStyle body(
    double size, {
    FontWeight weight = FontWeight.w400,
    Color color = AppColors.ink,
    double? height,
    double? letterSpacing,
  }) =>
      TextStyle(
        fontFamily: inter,
        fontFamilyFallback: fallback,
        fontSize: size,
        fontWeight: weight,
        color: color,
        height: height,
        letterSpacing: letterSpacing,
      );
}

ThemeData buildDriverTheme() {
  final base = ThemeData(
    useMaterial3: true,
    brightness: Brightness.dark,
    fontFamily: AppText.inter,
    fontFamilyFallback: AppText.fallback,
    scaffoldBackgroundColor: AppColors.ground,
    colorScheme: ColorScheme.fromSeed(
      seedColor: AppColors.yellow,
      brightness: Brightness.dark,
      primary: AppColors.yellow,
      surface: AppColors.card,
    ),
    splashFactory: InkRipple.splashFactory,
  );
  return base.copyWith(
    snackBarTheme: const SnackBarThemeData(
      behavior: SnackBarBehavior.floating,
      backgroundColor: AppColors.cardRaised,
      contentTextStyle: TextStyle(fontFamily: AppText.inter, fontSize: 14, color: AppColors.ink),
    ),
  );
}
