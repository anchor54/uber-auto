import 'dart:async';

/// Push notifications (flow R2): the device token the backend sends to, and
/// what arrives.
///
/// Behind an interface so demo mode and tests need no Firebase. Real builds
/// given a Firebase project use [FcmPushService] (lib/core/fcm_push.dart);
/// everything else runs on [NoPush], and the rider sees ride changes on the
/// screen within 5 seconds instead of as a notification.
abstract class PushService {
  /// This device's push token, or null when push isn't available. May ask
  /// the rider for permission to show notifications.
  Future<String?> token();

  /// 'android', 'ios' or 'web'.
  String get platform;

  /// Pushes as they happen: a notification the rider tapped, or one that
  /// arrived while the app was open.
  Stream<PushEvent> get events;

  /// The notification the rider tapped to launch the app, once; null after
  /// the first call or when the app was opened some other way.
  Future<PushEvent?> takeLaunchEvent();
}

/// One push about a ride. [kind] is the backend's notification kind
/// (driver_on_the_way, driver_arrived, …).
class PushEvent {
  const PushEvent({required this.kind, required this.rideId, required this.opened});

  final String kind;
  final String rideId;

  /// True when the rider tapped it; false when it arrived with the app open.
  final bool opened;

  /// Reads the data the backend sends with every push: {kind, rideId}.
  static PushEvent? fromData(Map<String, dynamic> data, {required bool opened}) {
    final rideId = data['rideId'];
    final kind = data['kind'];
    if (rideId is! String || rideId.isEmpty) return null;
    return PushEvent(kind: kind is String ? kind : '', rideId: rideId, opened: opened);
  }
}

class NoPush implements PushService {
  const NoPush();

  @override
  Future<String?> token() async => null;

  @override
  String get platform => 'android';

  @override
  Stream<PushEvent> get events => const Stream.empty();

  @override
  Future<PushEvent?> takeLaunchEvent() async => null;
}

/// A push service tests drive by hand.
class FakePush implements PushService {
  FakePush({this.deviceToken = 'fake-push-token-0001', this.launchEvent});

  final String? deviceToken;
  PushEvent? launchEvent;
  final _events = StreamController<PushEvent>.broadcast();

  @override
  Future<String?> token() async => deviceToken;

  @override
  String get platform => 'android';

  @override
  Stream<PushEvent> get events => _events.stream;

  @override
  Future<PushEvent?> takeLaunchEvent() async {
    final e = launchEvent;
    launchEvent = null;
    return e;
  }

  /// Delivers [e] as if FCM had.
  void deliver(PushEvent e) => _events.add(e);
}
