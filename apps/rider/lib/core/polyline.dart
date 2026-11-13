import 'models.dart';

/// Decodes an encoded polyline (Google's format, precision 5), as the maps
/// provider returns routes.
List<GeoPoint> decodePolyline(String encoded, {int precision = 5}) {
  final points = <GeoPoint>[];
  var index = 0, lat = 0, lng = 0;
  final factor = _pow10(precision);
  int next() {
    var shift = 0, result = 0, b = 0;
    do {
      if (index >= encoded.length) throw const FormatException('Polyline ends mid-value');
      b = encoded.codeUnitAt(index++) - 63;
      result |= (b & 0x1f) << shift;
      shift += 5;
    } while (b >= 0x20);
    return (result & 1) != 0 ? ~(result >> 1) : result >> 1;
  }

  while (index < encoded.length) {
    lat += next();
    lng += next();
    points.add(GeoPoint(lat / factor, lng / factor));
  }
  return points;
}

double _pow10(int n) {
  var v = 1.0;
  for (var i = 0; i < n; i++) {
    v *= 10;
  }
  return v;
}
