import 'dart:async';
import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:http/http.dart' as http;
import 'package:maplibre_gl/maplibre_gl.dart';

import '../app_scope.dart';
import '../core/models.dart';
import '../core/polyline.dart';
import 'tokens.dart';

/// Whether the backend serves a map style: asked once per URL per app run.
final _styleAvailable = <String, Future<bool>>{};

Future<bool> _mapStyleAvailable(String url) => _styleAvailable[url] ??= http
    .get(Uri.parse(url))
    .timeout(const Duration(seconds: 8))
    .then((r) => r.statusCode == 200)
    .catchError((Object _) => false);

/// A real street map (Ola Maps, through the backend's style) with the road
/// route, the pickup and drop-off, and the driver when known.
///
/// Shows [fallback] (the simple drawn map) when there is no map to load: demo
/// mode, tests, the web build, or a backend without a maps key. The map
/// doesn't pan or zoom, so it never fights the screen's scrolling; it fits
/// itself to the route and the driver.
class RideMap extends StatefulWidget {
  const RideMap({
    super.key,
    required this.pickup,
    required this.drop,
    required this.fallback,
    this.route,
    this.driver,
    this.towardsDrop = true,
    this.height = 220,
    this.label,
  });

  final Place pickup;
  final Place drop;

  /// Encoded road route from pickup to drop-off; a straight line when null.
  final String? route;

  /// The driver's latest position; no marker when null.
  final GeoPoint? driver;

  /// Riding: the route is the focus. On the way: the driver's approach is.
  final bool towardsDrop;
  final double height;
  final String? label;
  final Widget fallback;

  @override
  State<RideMap> createState() => _RideMapState();
}

class _RideMapState extends State<RideMap> {
  Future<bool>? _available;
  MapLibreMapController? _map;
  bool _styleLoaded = false;
  Circle? _driverDot;
  Line? _approach;

  static const _green = '#0E6B50';
  static const _ink = '#12161A';
  static const _yellow = '#F5C451';

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final url = AppScope.of(context).mapStyleUrl;
    if (_available == null && url != null && !kIsWeb) _available = _mapStyleAvailable(url);
  }

  LatLng _ll(double lat, double lng) => LatLng(lat, lng);

  List<LatLng> get _routePoints {
    final r = widget.route;
    if (r != null && r.isNotEmpty) {
      try {
        return [for (final p in decodePolyline(r)) _ll(p.lat, p.lng)];
      } on FormatException {
        // Fall through to the straight line.
      }
    }
    return [_ll(widget.pickup.lat, widget.pickup.lng), _ll(widget.drop.lat, widget.drop.lng)];
  }

  Future<void> _draw() async {
    final map = _map;
    if (map == null) return;
    _styleLoaded = true;
    await map.addLine(LineOptions(
      geometry: _routePoints,
      lineColor: _green,
      lineWidth: 5,
      lineOpacity: widget.towardsDrop ? 1 : 0.5,
      lineJoin: 'round',
    ));
    await map.addCircle(CircleOptions(
      geometry: _ll(widget.pickup.lat, widget.pickup.lng),
      circleRadius: 7,
      circleColor: '#FFFFFF',
      circleStrokeColor: _green,
      circleStrokeWidth: 4,
    ));
    await map.addCircle(CircleOptions(
      geometry: _ll(widget.drop.lat, widget.drop.lng),
      circleRadius: 7,
      circleColor: _ink,
      circleStrokeColor: '#FFFFFF',
      circleStrokeWidth: 2,
    ));
    await _drawDriver();
  }

  Future<void> _drawDriver() async {
    final map = _map;
    final d = widget.driver;
    if (map == null || !_styleLoaded) return;
    if (d != null) {
      final at = _ll(d.lat, d.lng);
      if (!widget.towardsDrop) {
        final approach = [at, _ll(widget.pickup.lat, widget.pickup.lng)];
        final line = _approach;
        if (line == null) {
          _approach = await map.addLine(LineOptions(geometry: approach, lineColor: _ink, lineWidth: 3, lineOpacity: 0.8));
        } else {
          await map.updateLine(line, LineOptions(geometry: approach));
        }
      } else if (_approach != null) {
        await map.removeLine(_approach!);
        _approach = null;
      }
      final dot = _driverDot;
      if (dot == null) {
        _driverDot = await map.addCircle(CircleOptions(
          geometry: at,
          circleRadius: 10,
          circleColor: _yellow,
          circleStrokeColor: _ink,
          circleStrokeWidth: 2,
        ));
      } else {
        await map.updateCircle(dot, CircleOptions(geometry: at));
      }
    }
    await _fit();
  }

  /// Fits the route, the stops and the driver into view.
  Future<void> _fit() async {
    final map = _map;
    if (map == null) return;
    final pts = [..._routePoints, if (widget.driver != null) _ll(widget.driver!.lat, widget.driver!.lng)];
    var s = pts.first.latitude, n = s, w = pts.first.longitude, e = w;
    for (final p in pts) {
      s = min(s, p.latitude);
      n = max(n, p.latitude);
      w = min(w, p.longitude);
      e = max(e, p.longitude);
    }
    await map.moveCamera(CameraUpdate.newLatLngBounds(
      LatLngBounds(southwest: _ll(s, w), northeast: _ll(n, e)),
      left: 36,
      top: widget.label == null ? 36 : 56,
      right: 36,
      bottom: 36,
    ));
  }

  @override
  void didUpdateWidget(RideMap old) {
    super.didUpdateWidget(old);
    if (old.driver?.lat != widget.driver?.lat || old.driver?.lng != widget.driver?.lng || old.towardsDrop != widget.towardsDrop) {
      unawaited(_drawDriver());
    }
  }

  @override
  Widget build(BuildContext context) {
    final available = _available;
    if (available == null) return widget.fallback;
    return FutureBuilder<bool>(
      future: available,
      builder: (context, snap) {
        if (snap.data != true) return widget.fallback;
        final url = AppScope.of(context).mapStyleUrl!;
        final center = _ll((widget.pickup.lat + widget.drop.lat) / 2, (widget.pickup.lng + widget.drop.lng) / 2);
        return ClipRRect(
          key: const Key('ride.realMap'),
          borderRadius: BorderRadius.circular(16),
          child: SizedBox(
            height: widget.height,
            width: double.infinity,
            child: Stack(
              children: [
                MapLibreMap(
                  styleString: url,
                  initialCameraPosition: CameraPosition(target: center, zoom: 12),
                  onMapCreated: (c) => _map = c,
                  onStyleLoadedCallback: () => unawaited(_draw()),
                  compassEnabled: false,
                  rotateGesturesEnabled: false,
                  scrollGesturesEnabled: false,
                  zoomGesturesEnabled: false,
                  tiltGesturesEnabled: false,
                  doubleClickZoomEnabled: false,
                  // The backend serves tiles up to zoom 15 (smaller, fewer).
                  minMaxZoomPreference: const MinMaxZoomPreference(null, 15),
                  attributionButtonPosition: AttributionButtonPosition.bottomRight,
                ),
                if (widget.label != null)
                  Positioned(
                    left: 12,
                    top: 12,
                    child: Container(
                      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                      decoration: BoxDecoration(color: Colors.white, borderRadius: BorderRadius.circular(8)),
                      child: Text(widget.label!, style: AppText.body(12, weight: FontWeight.w600)),
                    ),
                  ),
              ],
            ),
          ),
        );
      },
    );
  }
}
