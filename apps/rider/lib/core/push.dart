/// Push notifications (flow R2): the device token the backend sends to.
///
/// Behind an interface so demo mode and tests need no Firebase. The real
/// implementation uses Firebase Cloud Messaging once a Firebase project
/// exists; until then the app runs on [NoPush] and the rider sees ride
/// changes on the screen within 5 seconds instead of as a notification.
abstract class PushService {
  /// This device's push token, or null when push isn't available.
  Future<String?> token();

  /// 'android', 'ios' or 'web'.
  String get platform;
}

class NoPush implements PushService {
  const NoPush();

  @override
  Future<String?> token() async => null;

  @override
  String get platform => 'android';
}
