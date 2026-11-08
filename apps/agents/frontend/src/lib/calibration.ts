import type { CalibrationPoint, RoleCalibration } from "../api/types";

/** The point of the curve at a threshold; the nearest one when the threshold lies outside it. */
export const pointAt = (calibration: RoleCalibration, x: number): CalibrationPoint | undefined =>
  calibration.points.find((point) => point.x === Math.min(100, Math.max(50, Math.round(x))));

/** A share, 0 to 1, as a whole percentage. */
export const percent = (share: number): number => Math.round(share * 100);

export interface ChartSize {
  width: number;
  height: number;
  padding: number;
}

const xOf = (x: number, size: ChartSize) => size.padding + ((x - 50) / 50) * (size.width - 2 * size.padding);
const yOf = (share: number, size: ChartSize) => size.height - size.padding - share * (size.height - 2 * size.padding);

/** The SVG path of a line over the points, from X = 50 to X = 100. */
export const linePath = (
  points: readonly CalibrationPoint[],
  value: (point: CalibrationPoint) => number,
  size: ChartSize,
) =>
  points
    .map(
      (point, index) =>
        `${index === 0 ? "M" : "L"}${xOf(point.x, size).toFixed(1)} ${yOf(value(point), size).toFixed(1)}`,
    )
    .join(" ");

/** The horizontal position of the threshold's marker. */
export const markerX = (x: number, size: ChartSize) => xOf(Math.min(100, Math.max(50, x)), size);
