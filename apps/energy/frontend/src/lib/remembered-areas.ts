/**
 * Where the Prices page keeps the areas the person last compared — a
 * convenience per browser, not a setting. A link's `areas` wins over it; it
 * only decides what the page opens on when the link names none.
 */
export const REMEMBERED_AREAS_STORAGE_KEY = "vantigo.energy.prices.areas";

export const readRememberedAreas = (): string[] => {
  try {
    const raw = window.localStorage.getItem(REMEMBERED_AREAS_STORAGE_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed.filter((area): area is string => typeof area === "string") : [];
  } catch {
    return [];
  }
};

export const rememberAreas = (areas: readonly string[]): void => {
  try {
    window.localStorage.setItem(REMEMBERED_AREAS_STORAGE_KEY, JSON.stringify(areas));
  } catch {
    // A private window or blocked storage: the choice holds for this visit only.
  }
};
