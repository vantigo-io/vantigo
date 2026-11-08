import { describe, expect, it } from "vitest";
import type { CalibrationPoint, RoleCalibration } from "../api/types";
import { linePath, markerX, percent, pointAt } from "./calibration";

const points: CalibrationPoint[] = Array.from({ length: 51 }, (_, index) => ({
  x: 50 + index,
  autoCount: 100 - index,
  autoShare: (100 - index) / 100,
  badCount: 0,
  badShare: index / 100,
}));
const calibration: RoleCalibration = {
  roleId: "r",
  labelled: 100,
  minimum: 50,
  notEnoughHistory: false,
  threshold: 85,
  heldCount: 4,
  points,
};
const size = { width: 400, height: 160, padding: 10 };

describe("the calibration curve", () => {
  it("reads the point at a threshold, clamped to 50 to 100", () => {
    expect(pointAt(calibration, 85)?.x).toBe(85);
    expect(pointAt(calibration, 20)?.x).toBe(50);
    expect(pointAt(calibration, 130)?.x).toBe(100);
    expect(pointAt({ ...calibration, points: [] }, 85)).toBeUndefined();
  });

  it("states shares as whole percentages", () => {
    expect(percent(0.4)).toBe(40);
    expect(percent(0.2333)).toBe(23);
  });

  it("draws a line from the left edge to the right and puts the marker on the threshold", () => {
    const path = linePath(points, (point) => point.autoShare, size);
    expect(path.startsWith("M10.0 10.0")).toBe(true); // X = 50, share 1: top left
    expect(path.endsWith("L390.0 80.0")).toBe(true); // X = 100, share 0.5: half way down the plot
    expect(path.match(/L/g)).toHaveLength(50);
    expect(markerX(50, size)).toBe(10);
    expect(markerX(100, size)).toBe(390);
    expect(markerX(75, size)).toBe(200);
    expect(markerX(10, size)).toBe(10);
  });
});
