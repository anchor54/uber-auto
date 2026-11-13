import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

/// Opens the phone's dialer. Tests replace this to record calls instead.
Future<bool> Function(Uri uri) openUri = (uri) => launchUrl(uri);

/// Dials [number]; tells the rider if this device can't make calls.
Future<void> callNumber(BuildContext context, String number) async {
  final messenger = ScaffoldMessenger.of(context);
  var ok = false;
  try {
    ok = await openUri(Uri(scheme: 'tel', path: number));
  } catch (_) {
    ok = false;
  }
  if (!ok) messenger.showSnackBar(SnackBar(content: Text("Couldn't start a call. Dial $number from your phone.")));
}

/// India's single emergency number.
const emergencyNumber = '112';
