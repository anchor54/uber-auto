import 'package:flutter_test/flutter_test.dart';
import 'package:rider/core/polyline.dart';

void main() {
  test("decodes Google's example polyline", () {
    // developers.google.com/maps/documentation/utilities/polylinealgorithm
    final p = decodePolyline('_p~iF~ps|U_ulLnnqC_mqNvxq`@');
    expect([for (final x in p) (x.lat, x.lng)], [(38.5, -120.2), (40.7, -120.95), (43.252, -126.453)]);
  });

  test('an empty route is no points; a cut-off one is an error', () {
    expect(decodePolyline(''), isEmpty);
    expect(() => decodePolyline('_p~iF~ps|U_'), throwsFormatException);
  });
}
