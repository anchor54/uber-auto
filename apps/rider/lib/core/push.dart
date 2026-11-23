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

/// One push about a ride or a series. [kind] is the backend's notification
/// kind (series_active, series_no_driver, driver_on_the_way, …).
class PushEvent {
  const PushEvent({required this.kind, this.rideId, this.seriesId, required this.opened});

  final String kind;

  /// The ride the push is about (flow R2), or null.
  final String? rideId;

  /// The series the push is about (flow R1: driver confirmed, no driver yet), or null.
  final String? seriesId;

  /// True when the rider tapped it; false when it arrived with the app open.
  final bool opened;

  /// A result of a series request: Home reads the series again.
  bool get isSeriesResult => seriesId != null && rideId == null;

  /// Reads the data the backend sends with every push: `kind` plus
  /// `series_id` or a ride id.
  static PushEvent? fromData(Map<String, dynamic> data, {required bool opened}) {
    String? read(String a, String b) {
      final v = data[a] ?? data[b];
      return v is String && v.isNotEmpty ? v : null;
    }

    final rideId = read('ride_id', 'rideId');
    final seriesId = read('series_id', 'seriesId');
    if (rideId == null && seriesId == null) return null;
    final kind = data['kind'];
    return PushEvent(kind: kind is String ? kind : '', rideId: rideId, seriesId: seriesId, opened: opened);
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
