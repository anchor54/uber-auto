import 'package:flutter/material.dart';

import 'app.dart';
import 'core/fake_api.dart';
import 'core/http_api.dart';
import 'core/session_store.dart';

/// The backend to use, chosen when the app is built:
///
///   flutter run                                              → demo mode
///   flutter run --dart-define=API_BASE_URL=https://api.host  → real backend
///
/// Demo mode runs on the built-in fake backend (lib/core/fake_api.dart).
const apiBaseUrl = String.fromEnvironment('API_BASE_URL');

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  if (apiBaseUrl.isEmpty) {
    // Demo windows are short so the whole story plays in a couple of
    // minutes: say "I'm interested" on Priya's request and you get it when
    // its 90-second window closes.
    runApp(DriverApp(
      api: FakeDriverApi(
        latency: const Duration(milliseconds: 400),
        seriesWindow: const Duration(seconds: 90),
        rideWindow: const Duration(seconds: 60),
      ),
    ));
  } else {
    runApp(DriverApp(api: HttpDriverApi(baseUrl: apiBaseUrl, store: SecureSessionStore()), demoMode: false));
  }
}
