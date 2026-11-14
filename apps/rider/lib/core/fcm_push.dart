import 'dart:async';

import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/foundation.dart';

import 'push.dart';

/// The Firebase project the app's pushes come through: recurring-ride-share,
/// Android app com.uberauto.rider (Firebase console → Project settings →
/// Your apps). These are client settings that ship inside every APK, not
/// secrets, so they live here instead of in a google-services.json; the
/// key that can send pushes is the backend's service account.
///
/// A build can point at another project with --dart-define=FIREBASE_API_KEY,
/// FIREBASE_APP_ID, FIREBASE_SENDER_ID and FIREBASE_PROJECT_ID. An empty value
/// (CI passes one when its variable isn't set) keeps the default.
const _defaults = (
  apiKey: 'AIzaSyDPqWI4ts55tXYOJOcmW0Pmh9KTPLfW6ps',
  appId: '1:495111828309:android:9b6ea7f950f9662a7775dc',
  senderId: '495111828309',
  projectId: 'recurring-ride-share',
);

String _or(String given, String fallback) => given.isEmpty ? fallback : given;

final _apiKey = _or(const String.fromEnvironment('FIREBASE_API_KEY'), _defaults.apiKey);
final _appId = _or(const String.fromEnvironment('FIREBASE_APP_ID'), _defaults.appId);
final _senderId = _or(const String.fromEnvironment('FIREBASE_SENDER_ID'), _defaults.senderId);
final _projectId = _or(const String.fromEnvironment('FIREBASE_PROJECT_ID'), _defaults.projectId);

/// Whether push can run here. Only the Android app is registered in the
/// project, and FCM on the web would need a service worker the web build
/// doesn't have.
bool get firebaseConfigured => !kIsWeb && defaultTargetPlatform == TargetPlatform.android;

/// Push through Firebase Cloud Messaging. While the app is in the
/// background, Android shows the backend's notification itself; tapping it
/// opens the app with the ride's id. While the app is open, pushes arrive as
/// [events] and the screen asks for the ride straight away.
class FcmPushService implements PushService {
  FcmPushService._(this._messaging);

  final FirebaseMessaging _messaging;
  final _events = StreamController<PushEvent>.broadcast();
  bool _launchTaken = false;

  /// Starts Firebase; null when this build has no Firebase project or it fails
  /// to start, so the app falls back to [NoPush].
  static Future<FcmPushService?> start() async {
    if (!firebaseConfigured) return null;
    try {
      await Firebase.initializeApp(
        options: FirebaseOptions(apiKey: _apiKey, appId: _appId, messagingSenderId: _senderId, projectId: _projectId),
      );
      final service = FcmPushService._(FirebaseMessaging.instance);
      FirebaseMessaging.onMessage.listen((m) => service._add(m, opened: false));
      FirebaseMessaging.onMessageOpenedApp.listen((m) => service._add(m, opened: true));
      return service;
    } catch (e) {
      debugPrint('Push unavailable: $e');
      return null;
    }
  }

  void _add(RemoteMessage m, {required bool opened}) {
    final e = PushEvent.fromData(m.data, opened: opened);
    if (e != null) _events.add(e);
  }

  @override
  Future<String?> token() async {
    try {
      final settings = await _messaging.requestPermission();
      if (settings.authorizationStatus == AuthorizationStatus.denied) return null;
      // The registration token the backend's FCM HTTP v1 sender addresses
      // (message.token). Deprecated in favour of register()/onRegistered,
      // which yields an installation id for a different send target; switch
      // the backend and this together.
      // ignore: deprecated_member_use
      return await _messaging.getToken();
    } catch (e) {
      debugPrint('No push token: $e');
      return null;
    }
  }

  @override
  String get platform => defaultTargetPlatform == TargetPlatform.iOS ? 'ios' : 'android';

  @override
  Stream<PushEvent> get events => _events.stream;

  @override
  Future<PushEvent?> takeLaunchEvent() async {
    if (_launchTaken) return null;
    _launchTaken = true;
    final m = await _messaging.getInitialMessage();
    return m == null ? null : PushEvent.fromData(m.data, opened: true);
  }
}
