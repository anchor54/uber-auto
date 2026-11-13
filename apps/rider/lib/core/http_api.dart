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
    throw ApiException(
      (err['error'] as String?) ?? 'http_${res.statusCode}',
      (err['message'] as String?) ?? 'Something went wrong. Try again.',
    );
  }

  Future<Map<String, Object?>> _get(String path, {Map<String, String>? query}) async =>
      (await _send('GET', path, query: query)) as Map<String, Object?>;

  Future<Object?> _post(String path, Object body) => _send('POST', path, body: body);

  @override
  Future<RiderProfile?> restoreSession() async {
    if ((await store.read()) == null) return null;
    try {
      return riderFromJson((await _get('/v1/home'))['rider'] as Map<String, Object?>);
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
    return riderFromJson(res['rider'] as Map<String, Object?>);
  }

  @override
  Future<List<Place>> searchPlaces(String query) async {
    final res = await _get('/v1/places', query: {'q': query});
    return [for (final p in res['places'] as List) placeFromJson(p as Map<String, Object?>)];
  }

  @override
  Future<TripEstimate> estimateTrip(Place pickup, Place drop) async {
    final res = await _post('/v1/trips/estimate', {'pickup': placeToJson(pickup), 'drop': placeToJson(drop)});
    return tripFromJson(res as Map<String, Object?>);
  }

  @override
  Future<BookingOptions> bookingOptions({
    required BookingFlow flow,
    required Place pickup,
    required Place drop,
    required int pickupTime,
  }) async {
    final res = await _post('/v1/booking-options', {
      'flow': flow.configKey,
      'pickup': placeToJson(pickup),
      'drop': placeToJson(drop),
      'pickupTime': pickupTime,
    });
    return bookingOptionsFromJson(res as Map<String, Object?>);
  }

  @override
  Future<List<DriverMatch>> findDrivers(CommuteRequest request) async {
    final res = (await _post('/v1/commutes/proposals', {'request': commuteRequestToJson(request)})) as Map<String, Object?>;
    return [for (final p in res['proposals'] as List) proposalFromJson(p as Map<String, Object?>)];
  }

  @override
  Future<Commute> confirmCommute({
    required CommuteRequest request,
    required DriverMatch match,
    required PaymentMethod usualPayment,
  }) async {
    final id = match.proposalId;
    if (id == null) throw const ApiException('proposal_not_found', 'That match is no longer available. Search again.');
    final res = await _post('/v1/commutes', {'proposalId': id, 'usualPayment': paymentKey(usualPayment)});
    return commuteFromJson(res as Map<String, Object?>);
  }

  @override
  Future<void> joinWaitlist(CommuteRequest request) async {
    await _post('/v1/waitlist', {'request': commuteRequestToJson(request)});
  }

  @override
  Future<HomeData> home() async {
    final res = await _get('/v1/home');
    final commute = res['commute'];
    return HomeData(
      profile: riderFromJson(res['rider'] as Map<String, Object?>),
      commute: commute == null ? null : commuteFromJson(commute as Map<String, Object?>),
      recentRides: const [],
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
    await _send('PUT', '/v1/devices', body: {'pushToken': pushToken, 'platform': platform});
  }
}
