import 'dart:async';
import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:http/http.dart' as http;
import 'package:maplibre_gl/maplibre_gl.dart';

import '../app_scope.dart';
import '../core/models.dart';
import '../core/polyline.dart';
import '../core/route_geometry.dart';
import 'tokens.dart';

/// Whether the backend serves a map style: asked once per URL per app run.
final _styleAvailable = <String, Future<bool>>{};

Future<bool> _mapStyleAvailable(String url) => _styleAvailable[url] ??= http
    .get(Uri.parse(url))
    .timeout(const Duration(seconds: 8))
    .then((r) => r.statusCode == 200)
    .catchError((Object _) => false);

/// What the map is showing. Only one route is ever drawn.
enum MapPhase {
  /// Before booking: the commute route, pickup and drop-off.
  preview,

  /// The driver coming to the rider: their road route to the pickup, the
  /// pickup and the driver. No commute route, no drop-off.
  approach,

  /// Riding: the part of the commute route still ahead, the drop-off and the
  /// driver. No approach, no pickup.
  ride,
}

/// The points to draw for a [phase]: one line, the pins, and the driver.
class MapGeometry {
  MapGeometry({required this.line, required this.pins, required this.driver, required this.lineIsFaint});

  final List<GeoPoint> line;

  /// (point, isPickup). The pickup is a ringed dot; the drop-off a dark dot.
  final List<(GeoPoint, bool)> pins;
  final GeoPoint? driver;

  /// The approach line is thinner and darker than the commute route.
  final bool lineIsFaint;

  static List<GeoPoint>? _decode(String? encoded) {
    if (encoded == null || encoded.isEmpty) return null;
    try {
      final p = decodePolyline(encoded);
      return p.length >= 2 ? p : null;
    } on FormatException {
      return null;
    }
  }

  factory MapGeometry.of({
    required MapPhase phase,
    required Place pickup,
    required Place drop,
    String? route,
    String? approachRoute,
    GeoPoint? driver,
  }) {
    final p = GeoPoint(pickup.lat, pickup.lng), d = GeoPoint(drop.lat, drop.lng);
    switch (phase) {
      case MapPhase.preview:
        return MapGeometry(line: _decode(route) ?? [p, d], pins: [(p, true), (d, false)], driver: null, lineIsFaint: false);
      case MapPhase.approach:
        final road = _decode(approachRoute);
        final line = road != null
            ? (driver == null ? road : remainingRoute(road, driver))
            : (driver == null ? <GeoPoint>[] : [driver, p]);
        return MapGeometry(line: line, pins: [(p, true)], driver: driver, lineIsFaint: true);
      case MapPhase.ride:
        final full = _decode(route) ?? [p, d];
        return MapGeometry(line: driver == null ? full : remainingRoute(full, driver), pins: [(d, false)], driver: driver, lineIsFaint: false);
    }
  }

  /// Everything the camera should keep in view.
  List<GeoPoint> get all => [...line, for (final (pt, _) in pins) pt, if (driver != null) driver!];
}

/// A real street map (Ola Maps, through the backend's style) for one
/// [MapPhase]. Shows [fallback] (the simple drawn map) when there is no map
/// to load: demo mode, tests, the web build, or a backend without a maps
/// key. The map doesn't pan or zoom, so it never fights the screen's
/// scrolling; it fits itself to what it shows.
class RideMap extends StatefulWidget {
  const RideMap({
    super.key,
    required this.pickup,
    required this.drop,
    required this.fallback,
    this.phase = MapPhase.preview,
    this.route,
    this.approachRoute,
    this.driver,
    this.height = 220,
    this.label,
    this.rounded = true,
    this.overlap = EdgeInsets.zero,
  });

  final Place pickup;
  final Place drop;
  final MapPhase phase;

  /// Rounded corners for a map in a card; false for a full-bleed map.
  final bool rounded;

  /// What covers the map's edges (the status bar, a sheet over its bottom):
  /// the camera keeps the route clear of it.
  final EdgeInsets overlap;

  /// Encoded road route from pickup to drop-off; a straight line when null.
  final String? route;

  /// Encoded road route from the driver to the pickup (approach phase).
  final String? approachRoute;

  /// The driver's latest position; no marker when null.
  final GeoPoint? driver;
  final double height;
  final String? label;
  final Widget fallback;

  MapGeometry get geometry =>
      MapGeometry.of(phase: phase, pickup: pickup, drop: drop, route: route, approachRoute: approachRoute, driver: driver);

  @override
  State<RideMap> createState() => _RideMapState();
}

class _RideMapState extends State<RideMap> {
  Future<bool>? _available;
  MapLibreMapController? _map;
  bool _styleLoaded = false;
  Line? _line;
  Circle? _driverDot;
  bool _drawing = false;
  bool _redrawAgain = false;

  static const _green = '#0E6B50';
  static const _ink = '#12161A';
  static const _yellow = '#F5C451';

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final url = AppScope.of(context).mapStyleUrl;
    if (_available == null && url != null && !kIsWeb) _available = _mapStyleAvailable(url);
  }

  LatLng _ll(GeoPoint p) => LatLng(p.lat, p.lng);

  Future<void> _onStyleLoaded() async {
    final map = _map;
    if (map == null) return;
    _styleLoaded = true;
    for (final (pt, isPickup) in widget.geometry.pins) {
      await map.addCircle(CircleOptions(
        geometry: _ll(pt),
        circleRadius: 7,
        circleColor: isPickup ? '#FFFFFF' : _ink,
        circleStrokeColor: isPickup ? _green : '#FFFFFF',
        circleStrokeWidth: isPickup ? 4 : 2,
      ));
    }
    await _update();
  }

  /// Brings the line and the driver up to date, then refits the camera.
  /// Calls that arrive while one is running are folded into one more pass.
  Future<void> _update() async {
    final map = _map;
    if (map == null || !_styleLoaded) return;
    if (_drawing) {
      _redrawAgain = true;
      return;
    }
    _drawing = true;
    try {
      do {
        _redrawAgain = false;
        final g = widget.geometry;
        final line = [for (final p in g.line) _ll(p)];
        if (_line == null && line.length >= 2) {
          _line = await map.addLine(LineOptions(
            geometry: line,
            lineColor: g.lineIsFaint ? _ink : _green,
            lineWidth: g.lineIsFaint ? 4 : 5,
            lineOpacity: g.lineIsFaint ? 0.75 : 1,
            lineJoin: 'round',
          ));
        } else if (_line != null && line.length >= 2) {
          await map.updateLine(_line!, LineOptions(geometry: line));
        }
        final d = g.driver;
        if (d != null && _driverDot == null) {
          _driverDot = await map.addCircle(CircleOptions(
            geometry: _ll(d),
            circleRadius: 10,
            circleColor: _yellow,
            circleStrokeColor: _ink,
            circleStrokeWidth: 2,
          ));
        } else if (d != null) {
          await map.updateCircle(_driverDot!, CircleOptions(geometry: _ll(d)));
        }
        await _fit(g.all);
      } while (_redrawAgain && mounted);
    } finally {
      _drawing = false;
    }
  }

  Future<void> _fit(List<GeoPoint> pts) async {
    final map = _map;
    if (map == null || pts.isEmpty) return;
    var s = pts.first.lat, n = s, w = pts.first.lng, e = w;
    for (final p in pts) {
      s = min(s, p.lat);
      n = max(n, p.lat);
      w = min(w, p.lng);
      e = max(e, p.lng);
    }
    // Never zoom in tighter than ~400 m across, e.g. when the driver is at the pickup.
    const pad = 0.002;
    if (n - s < pad * 2) {
      s -= pad;
      n += pad;
    }
    if (e - w < pad * 2) {
      w -= pad;
      e += pad;
    }
    await map.moveCamera(CameraUpdate.newLatLngBounds(
      LatLngBounds(southwest: LatLng(s, w), northeast: LatLng(n, e)),
      left: 36 + widget.overlap.left,
      top: (widget.label == null ? 36 : 56) + widget.overlap.top,
      right: 36 + widget.overlap.right,
      bottom: 36 + widget.overlap.bottom,
    ));
  }

  @override
  void didUpdateWidget(RideMap old) {
    super.didUpdateWidget(old);
    if (old.driver?.lat != widget.driver?.lat ||
        old.driver?.lng != widget.driver?.lng ||
        old.approachRoute != widget.approachRoute ||
        old.route != widget.route) {
      unawaited(_update());
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
        final all = widget.geometry.all;
        final center = all.isEmpty
            ? LatLng(widget.pickup.lat, widget.pickup.lng)
            : LatLng(all.map((p) => p.lat).reduce((a, b) => a + b) / all.length, all.map((p) => p.lng).reduce((a, b) => a + b) / all.length);
        return ClipRRect(
          key: Key('ride.realMap.${widget.phase.name}'),
          borderRadius: BorderRadius.circular(widget.rounded ? 16 : 0),
          child: SizedBox(
            height: widget.height,
            width: double.infinity,
            child: Stack(
              children: [
                MapLibreMap(
                  styleString: url,
                  initialCameraPosition: CameraPosition(target: center, zoom: 12),
                  onMapCreated: (c) => _map = c,
                  onStyleLoadedCallback: () => unawaited(_onStyleLoaded()),
                  compassEnabled: false,
                  rotateGesturesEnabled: false,
                  scrollGesturesEnabled: false,
                  zoomGesturesEnabled: false,
                  tiltGesturesEnabled: false,
                  doubleClickZoomEnabled: false,
                  // The backend serves tiles up to zoom 15 (smaller, fewer).
                  minMaxZoomPreference: const MinMaxZoomPreference(null, 15),
                  // Kept out from under whatever covers the bottom of the map.
                  attributionButtonPosition:
                      widget.overlap.bottom > 0 ? AttributionButtonPosition.topRight : AttributionButtonPosition.bottomRight,
                  attributionButtonMargins: widget.overlap.bottom > 0 ? Point(8, 8 + widget.overlap.top) : null,
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
