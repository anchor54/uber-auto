import { describe, expect, it } from 'vitest';
import { alongPath, decodePolyline } from '../../src/domain/polyline.js';

describe('polylines', () => {
  it("decodes Google's example", () => {
    // developers.google.com/maps/documentation/utilities/polylinealgorithm
    expect(decodePolyline('_p~iF~ps|U_ulLnnqC_mqNvxq`@')).toEqual([
      { lat: 38.5, lng: -120.2 },
      { lat: 40.7, lng: -120.95 },
      { lat: 43.252, lng: -126.453 },
    ]);
  });

  it('a cut-off polyline is an error', () => {
    expect(() => decodePolyline('_p~iF~ps|U_')).toThrow();
  });

  it('moves along the path by distance, round corners included', () => {
    // An L: 1 km north, then 1 km east (about).
    const at = alongPath([
      { lat: 12.9, lng: 77.6 },
      { lat: 12.909, lng: 77.6 },
      { lat: 12.909, lng: 77.6092 },
    ]);
    expect(at(0)).toEqual({ lat: 12.9, lng: 77.6 });
    expect(at(1)).toEqual({ lat: 12.909, lng: 77.6092 });
    const half = at(0.5); // at the corner, not cutting across
    expect(half.lat).toBeCloseTo(12.909, 3);
    expect(half.lng).toBeCloseTo(77.6, 3);
    expect(at(0.25).lng).toBe(77.6); // still on the first leg
  });
});
