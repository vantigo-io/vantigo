/**
 * Where the board keeps the site last looked at (M36) — a convenience per
 * browser, not a setting. The board starts at the first site without it.
 */
export const SCHEDULE_SITE_STORAGE_KEY = "vantigo.manufacturing.schedule.site";

export const readRememberedScheduleSite = (): number | undefined => {
  try {
    const raw = window.localStorage.getItem(SCHEDULE_SITE_STORAGE_KEY);
    const id = raw ? Number(raw) : Number.NaN;
    return Number.isInteger(id) && id > 0 ? id : undefined;
  } catch {
    return undefined;
  }
};

export const rememberScheduleSite = (siteId: number): void => {
  try {
    window.localStorage.setItem(SCHEDULE_SITE_STORAGE_KEY, String(siteId));
  } catch {
    // A private window or blocked storage: the choice holds for this visit only.
  }
};
