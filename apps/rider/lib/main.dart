import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

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

/// Bump when the backend's map style changes in a way the app must see at once.
const mapStyleVersion = 2;

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  // Draw under the system bars (screens keep clear of them with SafeArea),
  // so a screen can run its colour or map up under the status bar.
  SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);
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
    runApp(RiderApp(
      api: HttpRiderApi(baseUrl: apiBaseUrl, store: SecureSessionStore()),
      demoMode: false,
      // Real maps (Ola Maps through the backend) when it serves a style.
      // The version makes map views fetch the style afresh when it changes.
      mapStyleUrl: '$apiBaseUrl/v1/map/style.json?v=$mapStyleVersion',
    ));
  }
}
