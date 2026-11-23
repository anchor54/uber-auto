import 'dart:async';
import 'dart:convert';

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
    // A 409 offer_closed carries the offer as it is now.
    if (state is Map<String, Object?> && state.containsKey('can_respond')) {
      try {
        parsed = offerFromJson(state, fetchedAt: _now());
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
    return driverFromJson(res['user'] as Map<String, Object?>);
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

  @override
  Future<void> registerDevice(String pushToken, String platform) async {
    await _send('PUT', '/v1/devices', body: {'fcm_token': pushToken, 'platform': platform});
  }
}
