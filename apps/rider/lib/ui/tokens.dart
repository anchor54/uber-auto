import 'package:flutter/material.dart';

/// Colours from the rider design (Rider App spec, design system section).
abstract final class AppColors {
  static const ground = Color(0xFFF3F4EF);
  static const surface = Color(0xFFFFFFFF);
  static const ink = Color(0xFF12161A);
  static const muted = Color(0xFF5A6168);
  static const line = Color(0xFFD3D7CF);
  static const divider = Color(0xFFECEEE8);
  static const cardBorder = Color(0xFFE2E4DE);
  static const inputFill = Color(0xFFFAFBF8);
  static const chip = Color(0xFFF0F1EC);
  static const green = Color(0xFF0E6B50);
  static const greenDark = Color(0xFF0A4F3B);
  static const greenTint = Color(0xFFDCEFE6);
  static const yellow = Color(0xFFF5C451);
  static const darkPanel = Color(0xFF232A31);
  static const darkMuted = Color(0xFFC9CED2);
  static const orange = Color(0xFFC2570C);
  static const orangeTint = Color(0xFFFBE3CF);
  static const orangeText = Color(0xFF8A3A06);
  static const outline = Color(0xFF8A9198);
  static const red = Color(0xFFB42318);
  static const mapGround = Color(0xFFE4E8E0);
  static const mapPark = Color(0xFFCFE3D2);
  static const welcomeRoad = Color(0xFF1B7C60);
  static const welcomeRoadThin = Color(0xFF16765A);
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

ThemeData buildRiderTheme() {
  final base = ThemeData(
    useMaterial3: true,
    fontFamily: AppText.inter,
    fontFamilyFallback: AppText.fallback,
    scaffoldBackgroundColor: AppColors.ground,
    colorScheme: ColorScheme.fromSeed(
      seedColor: AppColors.green,
      primary: AppColors.green,
      surface: AppColors.surface,
    ),
    splashFactory: InkRipple.splashFactory,
  );
  return base.copyWith(
    snackBarTheme: const SnackBarThemeData(
      behavior: SnackBarBehavior.floating,
      backgroundColor: AppColors.ink,
      contentTextStyle: TextStyle(fontFamily: AppText.inter, fontSize: 14, color: Colors.white),
    ),
    switchTheme: SwitchThemeData(
      thumbColor: WidgetStateProperty.all(Colors.white),
      trackColor: WidgetStateProperty.resolveWith(
        (s) => s.contains(WidgetState.selected) ? AppColors.green : AppColors.line,
      ),
      trackOutlineColor: WidgetStateProperty.all(Colors.transparent),
    ),
  );
}
