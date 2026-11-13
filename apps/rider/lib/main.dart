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
/// Demo mode runs on the built-in fake backend (lib/core/fake_api.dart) and
/// needs no network. Release builds published from CI are demo builds unless
/// CI passes API_BASE_URL.
const apiBaseUrl = String.fromEnvironment('API_BASE_URL');

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  if (apiBaseUrl.isEmpty) {
    // The demo driver takes a few seconds to accept, so "Waiting for …" shows.
    // Then, 20 seconds after accepting, the first ride plays itself out at 20x speed.
    runApp(RiderApp(
      api: FakeRiderApi(
        latency: const Duration(milliseconds: 500),
        driverResponse: DemoDriverResponse.acceptLater,
        demoRideAfter: const Duration(seconds: 20),
        rideSpeed: 20,
      ),
    ));
  } else {
    runApp(RiderApp(api: HttpRiderApi(baseUrl: apiBaseUrl, store: SecureSessionStore()), demoMode: false));
  }
}
