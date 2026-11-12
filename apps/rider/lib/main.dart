import 'package:flutter/material.dart';

import 'app.dart';
import 'core/fake_api.dart';

/// Demo mode runs against the built-in fake backend. The real backend client
/// is added once the API is deployed; until then demo mode is the only mode.
void main() {
  runApp(RiderApp(api: FakeRiderApi(latency: const Duration(milliseconds: 500))));
}
