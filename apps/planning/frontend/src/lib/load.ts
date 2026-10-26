import type { PlanningWeekFigures } from "../api/board";

/** The colour scale of a week's load (design §7): free, fine, full, over — or no capacity at all. */
export type LoadLevel = "none" | "free" | "fine" | "full" | "over";

/** Where a week falls on the scale: under half is free, under 90 % fine, up to 100 % full, beyond it over. */
export const loadLevel = (figures: Pick<PlanningWeekFigures, "loadPercent" | "booked">): LoadLevel => {
  if (figures.loadPercent === undefined) return "none";
  if (figures.loadPercent > 100) return "over";
  if (figures.loadPercent >= 90) return "full";
  if (figures.loadPercent >= 50) return "fine";
  return "free";
};

export const loadBackgrounds: Record<LoadLevel, string> = {
  none: "var(--mantine-color-default-hover)",
  free: "var(--mantine-color-gray-light)",
  fine: "var(--mantine-color-teal-light)",
  full: "var(--mantine-color-blue-light)",
  over: "var(--mantine-color-orange-light)",
};

/** The diagonal hatching a tentative share is drawn with (S3: shown apart). */
export const HATCH = "repeating-linear-gradient(45deg, var(--mantine-color-violet-light) 0 4px, transparent 4px 8px)";
