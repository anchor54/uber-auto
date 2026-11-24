import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:http/http.dart' as http;

import 'api.dart';
import 'api_json.dart';
import 'models.dart';
import 'session_store.dart';

/// [DriverApi] over HTTP against the backend in backend/ (see backend/API.md).
///
/// Keeps the session token in [store] so the driver stays signed in. Any 401
/// clears the saved token: the next launch starts at Welcome.
class HttpDriverApi implements DriverApi {
  HttpDriverApi({
    required String baseUrl,
    required this.store,
    http.Client? client,
    this.timeout = const Duration(seconds: 20),
    DateTime Function()? now,
  })  : baseUrl = baseUrl.endsWith('/') ? baseUrl.substring(0, baseUrl.length - 1) : baseUrl,
        _client = client ?? http.Client(),
        _now = now ?? DateTime.now;

  final String baseUrl;
  final SessionStore store;
  final Duration timeout;
  final http.Client _client;
  final DateTime Function() _now;
  String? _token;

  static const _offline = ApiException('network', "Can't reach the server. Check your connection and try again.");

  Future<Object?> _send(String method, String path, {Object? body, Map<String, String>? query}) async {
    _token ??= await store.read();
    final uri = Uri.parse('$baseUrl$path').replace(queryParameters: query);
    final request = http.Request(method, uri)
      ..headers.addAll({
        if (body != null) 'content-type': 'application/json',
        if (_token != null) 'authorization': 'Bearer $_token',
      });
    if (body != null) request.body = jsonEncode(body);
    final http.Response res;
    try {
      res = await http.Response.fromStream(await _client.send(request).timeout(timeout));
    } on TimeoutException {
      throw _offline;
    } on http.ClientException {
      throw _offline;
    }
    final decoded = res.body.isEmpty ? null : jsonDecode(utf8.decode(res.bodyBytes));
    if (res.statusCode >= 200 && res.statusCode < 300) return decoded;
    if (res.statusCode == 401 && _token != null) {
      _token = null;
      await store.clear();
    }
    final err = decoded is Map<String, Object?> ? decoded : const <String, Object?>{};
    final state = err['state'];
    Object? parsed = state;
    // A 409 offer_closed carries the offer as it is now; D1 errors carry
    // the driver (or, for already_claimed, the payment).
    if (state is Map<String, Object?>) {
      try {
        if (state.containsKey('can_respond')) {
          parsed = offerFromJson(state, fetchedAt: _now());
        } else if (looksLikeDriver(state)) {
          parsed = driverDetailFromJson(state);
        } else if (looksLikePayment(state)) {
          parsed = paymentFromJson(state);
        }
      } catch (_) {
        parsed = state;
      }
    }
    throw ApiException(
      (err['code'] as String?) ?? 'http_${res.statusCode}',
      (err['message'] as String?) ?? 'Something went wrong. Try again.',
      state: parsed,
    );
  }

  Future<Map<String, Object?>> _get(String path, {Map<String, String>? query}) async =>
      (await _send('GET', path, query: query)) as Map<String, Object?>;

  Future<Object?> _post(String path, [Object body = const <String, Object?>{}]) => _send('POST', path, body: body);

  @override
  Future<DriverProfile?> restoreSession() async {
    if ((await store.read()) == null) return null;
    try {
      final me = await _get('/v1/me');
      if (me['role'] != 'driver') return null;
      return driverFromJson(me);
    } on ApiException catch (e) {
      // Offline at launch: keep the token. Only a rejected session signs out.
      if (e.code == 'network') rethrow;
      return null;
    }
  }

  @override
  Future<void> sendOtp(String phone) async {
    await _post('/v1/driver/auth/otp', {'phone': phone});
  }

  @override
  Future<DriverProfile> verifyOtp(String phone, String code) async {
    final res = (await _post('/v1/driver/auth/verify', {'phone': phone, 'code': code})) as Map<String, Object?>;
    _token = res['token'] as String;
    await store.write(_token!);
    return signInFromJson(res);
  }

  @override
  Future<OfferList> offers() async => offerListFromJson(await _get('/v1/offers'), fetchedAt: _now());

  @override
  Future<Offer> offer(String id) async => offerFromJson(await _get('/v1/offers/$id'), fetchedAt: _now());

  @override
  Future<void> markInterested(String id) async {
    await _post('/v1/offers/$id/mark-interested');
  }

  @override
  Future<void> withdrawInterest(String id) async {
    await _post('/v1/offers/$id/withdraw-interest');
  }

  @override
  Future<void> dismissOffer(String id) async {
    await _post('/v1/offers/$id/dismiss');
  }

  @override
  Future<DriverWeek> week({DateTime? start}) async =>
      weekFromJson(await _get('/v1/rides/week', query: start == null ? null : {'start': dateKey(start)}));

  // D1

  Future<Map<String, Object?>> _json(String method, String path, [Object? body]) async =>
      (await _send(method, path, body: body ?? const <String, Object?>{})) as Map<String, Object?>;

  @override
  Future<Driver> driverSignup({required String name, required String city, required bool termsAccepted}) async =>
      driverDetailFromJson(await _json('POST', '/v1/driver/signup', {'name': name, 'city': city, 'terms_accepted': termsAccepted}));

  @override
  Future<Driver> me() async => driverDetailFromJson(await _get('/v1/driver'));

  @override
  Future<UploadTarget> uploadUrl(String kind, String contentType) async =>
      uploadTargetFromJson(await _json('POST', '/v1/driver/upload-url', {'kind': kind, 'content_type': contentType}));

  @override
  Future<DriverItem> uploadDocument(String kind, Uint8List bytes, String contentType) async {
    if (bytes.length > maxUploadBytes) {
      throw const ApiException('file_too_large', 'That photo is over 5 MB. Try a smaller one.');
    }
    final target = await uploadUrl(kind, contentType);
    await _putFile(target, bytes);
    return driverItemFromJson(await _json('PUT', '/v1/driver/documents/$kind', {'storage_path': target.storagePath}));
  }

  /// The signed URL, reachable from this device: a link the backend signed
  /// for localhost goes to the host the app talks to (emulators, phones).
  Uri _reachable(String url) {
    final base = Uri.parse(baseUrl);
    final u = Uri.parse(url);
    if (!u.hasScheme) return base.resolveUri(u);
    const local = {'localhost', '127.0.0.1', '0.0.0.0'};
    if (local.contains(u.host) && !local.contains(base.host)) {
      return u.replace(scheme: base.scheme, host: base.host, port: base.port);
    }
    return u;
  }

  Future<void> _putFile(UploadTarget target, Uint8List bytes) async {
    final request = http.Request(target.method, _reachable(target.uploadUrl))
      ..headers['content-type'] = target.contentType
      ..bodyBytes = bytes;
    final http.Response res;
    try {
      res = await http.Response.fromStream(await _client.send(request).timeout(const Duration(seconds: 60)));
    } on TimeoutException {
      throw _offline;
    } on http.ClientException {
      throw _offline;
    }
    if (res.statusCode >= 200 && res.statusCode < 300) return;
    String? code;
    try {
      final err = jsonDecode(utf8.decode(res.bodyBytes));
      if (err is Map<String, Object?>) code = err['code'] as String?;
    } catch (_) {
      // Not JSON: the status decides the message.
    }
    throw ApiException(
      code ?? 'upload_failed',
      switch (res.statusCode) {
        413 => 'That photo is over 5 MB. Try a smaller one.',
        415 => 'Use a JPEG or PNG photo.',
        _ => 'Upload failed. Try again.',
      },
    );
  }

  @override
  Future<DriverItem> setUpi(String upiId) async => driverItemFromJson(await _json('PUT', '/v1/driver/upi', {'upi_id': upiId}));

  @override
  Future<VehicleDetails> setVehicle({
    required String type,
    String? cabClass,
    required String makeModel,
    required int year,
    required String plate,
    required String colour,
    required bool ac,
    required bool cngOrElectric,
  }) async =>
      vehicleFromJson(await _json('PUT', '/v1/driver/vehicle', {
        'vehicle_type': type,
        'cab_class': type == 'cab' ? cabClass : null,
        'make_model': makeModel,
        'year': year,
        'plate': plate,
        'colour': colour,
        'ac': ac,
        'cng_or_electric': cngOrElectric,
      }));

  @override
  Future<Driver> setDayOff(int? weekday) async => driverDetailFromJson(await _json('PUT', '/v1/driver/day-off', {'day_off': weekday}));

  @override
  Future<Driver> submitForReview() async => driverDetailFromJson(await _json('POST', '/v1/driver/submit'));

  @override
  Future<PlanQuote> plan() async => planQuoteFromJson(await _get('/v1/driver/plan'));

  @override
  Future<SubscriptionPayment> claimPayment({DateTime? periodStart}) async => paymentFromJson(
      await _json('POST', '/v1/driver/payments', {if (periodStart != null) 'period_start': dateKey(periodStart)}));

  /// No push channel in this build: screens refresh when opened, on resume
  /// and every [DriverApp.refreshEvery].
  @override
  Stream<String> get pushes => const Stream<String>.empty();

  @override
  Future<void> registerDevice(String pushToken, String platform) async {
    await _send('PUT', '/v1/devices', body: {'fcm_token': pushToken, 'platform': platform});
  }
}
