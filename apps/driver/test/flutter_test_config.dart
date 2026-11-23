import 'dart:async';
import 'dart:io';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

/// Loads the app's real fonts so widget tests measure text like the app
/// does and screenshots show real glyphs instead of boxes.
Future<void> testExecutable(FutureOr<void> Function() testMain) async {
  TestWidgetsFlutterBinding.ensureInitialized();
  const families = {
    'Inter': ['Regular', 'Medium', 'SemiBold', 'Bold'],
    'SpaceGrotesk': ['Medium', 'SemiBold', 'Bold'],
    'Arrows': ['Regular', 'Bold'],
  };
  for (final entry in families.entries) {
    final loader = FontLoader(entry.key);
    for (final weight in entry.value) {
      final bytes = File('assets/fonts/${entry.key}-$weight.ttf').readAsBytesSync();
      loader.addFont(Future.value(bytes.buffer.asByteData()));
    }
    await loader.load();
  }
  await testMain();
}
