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
    //
    // Sign in with 9000000201 to be Ravi, already live. Any other number
    // signs up as a new driver (D1): the pretend admin approves 5 seconds
    // after "Submit for review" and confirms 5 seconds after "I've paid".
    runApp(DriverApp(
      api: FakeDriverApi(
        latency: const Duration(milliseconds: 400),
        seriesWindow: const Duration(seconds: 90),
        rideWindow: const Duration(seconds: 60),
        autoReviewAfter: const Duration(seconds: 5),
      ),
      refreshEvery: const Duration(seconds: 3),
    ));
  } else {
    // No push channel yet: waiting screens read the driver every 15 seconds.
    runApp(DriverApp(
      api: HttpDriverApi(baseUrl: apiBaseUrl, store: SecureSessionStore()),
      demoMode: false,
      refreshEvery: const Duration(seconds: 15),
    ));
  }
}
