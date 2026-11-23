import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import 'api.dart';
import 'api_json.dart';
import 'models.dart';
import 'session_store.dart';

/// [RiderApi] over HTTP against the backend in backend/.
///
/// Keeps the session token in [store] so the rider stays signed in. Any 401
/// clears the saved token: the next launch starts at Welcome.
class HttpRiderApi implements RiderApi {
  HttpRiderApi({
    required String baseUrl,
    required this.store,
    http.Client? client,
    this.timeout = const Duration(seconds: 20),
  })  : baseUrl = baseUrl.endsWith('/') ? baseUrl.substring(0, baseUrl.length - 1) : baseUrl,
        _client = client ?? http.Client();

  final String baseUrl;
  final SessionStore store;
  final Duration timeout;
  final http.Client _client;
  String? _token;

  static const _offline = ApiException('network', "Can't reach the server. Check your connection and try again.");

  Future<Map<String, String>> _headers({bool json = false}) async {
    _token ??= await store.read();
    return {
      if (json) 'content-type': 'application/json',
      if (_token != null) 'authorization': 'Bearer $_token',
    };
  }

  Future<Object?> _send(String method, String path, {Object? body, Map<String, String>? query}) async {
    final uri = Uri.parse('$baseUrl$path').replace(queryParameters: query);
    final request = http.Request(method, uri)..headers.addAll(await _headers(json: body != null));
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
    throw ApiException(
      (err['code'] as String?) ?? (err['error'] as String?) ?? 'http_${res.statusCode}',
      (err['message'] as String?) ?? 'Something went wrong. Try again.',
      // A series in the state (e.g. 409 already_assigned) comes back as a Series.
      state: state is Map<String, Object?> && state.containsKey('slots') && state.containsKey('rider_id') ? _seriesOrRaw(state) : state,
    );
  }

  static Object _seriesOrRaw(Map<String, Object?> j) {
    try {
      return seriesFromJson(j);
    } catch (_) {
      return j;
    }
  }

  Future<Map<String, Object?>> _get(String path, {Map<String, String>? query}) async =>
      (await _send('GET', path, query: query)) as Map<String, Object?>;

  Future<Object?> _post(String path, Object body) => _send('POST', path, body: body);

  @override
  Future<RiderProfile?> restoreSession() async {
    if ((await store.read()) == null) return null;
    try {
      return riderFromJson(await _get('/v1/me'));
    } on ApiException catch (e) {
      // Offline at launch: keep the token and let Home retry. Only a
      // rejected session sends the rider back to Welcome.
      if (e.code == 'network') rethrow;
      return null;
    }
  }

  @override
  Future<void> sendOtp(String phone) async {
    await _post('/v1/auth/otp', {'phone': phone});
  }

  @override
  Future<RiderProfile> verifyOtp(String phone, String code) async {
    final res = (await _post('/v1/auth/verify', {'phone': phone, 'code': code})) as Map<String, Object?>;
    _token = res['token'] as String;
    await store.write(_token!);
    return riderFromJson((res['user'] ?? res['rider']) as Map<String, Object?>);
  }

  @override
  Future<List<Place>> searchPlaces(String query) async {
    final res = await _get('/v1/places', query: {'q': query});
    return [for (final p in res['places'] as List) placeFromJson(p as Map<String, Object?>)];
  }

  /// The config's ride types, each priced by a preview of [request] with that
  /// type. A type the backend refuses for this route is left out.
  @override
  Future<List<RideTypeOption>> rideTypeOptions(CommuteRequest request) async {
    final res = await _get('/v1/ride-types', query: {'flow': 'recurring'});
    final types = [
      for (final t in res['ride_types'] as List)
        if (RideTypeInfo.tryKey((t as Map<String, Object?>)['ride_type'] as String?) case final type?) type,
    ];
    final fares = await Future.wait([
      for (final type in types)
        previewSeries(CommuteRequest(
          pickup: request.pickup,
          drop: request.drop,
          slots: request.slots,
          rideType: type,
          preferences: request.preferences,
          usualPayment: request.usualPayment,
          startDate: request.startDate,
        )).then<int?>((p) => p.suggestedFare).catchError((Object e) {
          if (e is ApiException && e.code == 'ride_type_not_allowed') return null;
          throw e;
        }),
    ]);
    return [
      for (var i = 0; i < types.length; i++)
        if (fares[i] != null) RideTypeOption(type: types[i], fare: fares[i]!),
    ];
  }

  @override
  Future<SeriesPreview> previewSeries(CommuteRequest request) async =>
      seriesPreviewFromJson((await _post('/v1/series/preview', commuteRequestToJson(request))) as Map<String, Object?>);

  @override
  Future<Series> createSeries(CommuteRequest request) async =>
      seriesFromJson((await _post('/v1/series', commuteRequestToJson(request))) as Map<String, Object?>);

  @override
  Future<Series> getSeries(String id) async => seriesFromJson(await _get('/v1/series/$id'));

  @override
  Future<List<Series>> listSeries() async {
    final res = await _get('/v1/series');
    return [for (final s in res['series'] as List) seriesFromJson(s as Map<String, Object?>)];
  }

  @override
  Future<void> cancelRequest(String id) async {
    await _post('/v1/series/$id/cancel-request', const <String, Object?>{});
  }

  @override
  Future<Series> retrySeries(String id, {CommuteRequest? changes}) async => seriesFromJson(
      (await _post('/v1/series/$id/retry', changes == null ? const <String, Object?>{} : commuteRequestToJson(changes))) as Map<String, Object?>);

  @override
  Future<HomeData> home() async {
    final results = await Future.wait([_get('/v1/me'), listSeries()]);
    final series = results[1] as List<Series>;
    return HomeData(
      profile: riderFromJson(results[0] as Map<String, Object?>),
      series: [for (final s in series) if (s.status != SeriesStatus.ended) s],
    );
  }

  // ---- flow R2 ----

  List<Ride> _rides(Map<String, Object?> res) => [for (final r in res['rides'] as List) rideFromJson(r as Map<String, Object?>)];

  @override
  Future<List<Ride>> upcomingRides({int days = 7}) async => _rides(await _get('/v1/rides/upcoming', query: {'days': '$days'}));

  @override
  Future<Ride> ride(String id) async => rideFromJson(await _get('/v1/rides/$id'));

  @override
  Future<LiveRide> liveRide(String id) async => liveRideFromJson(await _get('/v1/rides/$id/live'));

  @override
  Future<Ride> runningLate(String id) async => rideFromJson((await _post('/v1/rides/$id/running-late', const {})) as Map<String, Object?>);

  @override
  Future<PaymentStatus> markPayment(String id, PaymentMethod method) async {
    final res = (await _post('/v1/rides/$id/payment', {'method': paymentKey(method)})) as Map<String, Object?>;
    return paymentStatusFrom(res['paymentStatus'] as String);
  }

  @override
  Future<void> rateRide(String id, RideRating rating) async {
    await _post('/v1/rides/$id/rating', ratingToJson(rating));
  }

  @override
  Future<List<Ride>> unsettledRides() async => _rides(await _get('/v1/rides/unsettled'));

  @override
  Future<void> registerDevice(String pushToken, String platform) async {
    await _send('PUT', '/v1/devices', body: {'fcm_token': pushToken, 'platform': platform});
  }
}
