/**
 * Where the shop floor keeps the work center this device last worked at (M31)
 * — a convenience per browser, not a setting. The picker offers it first; the
 * screens work the same without it.
 */
export const SHOP_FLOOR_WORK_CENTER_STORAGE_KEY = "vantigo.manufacturing.shopFloor.workCenter";

export const readRememberedWorkCenter = (): number | undefined => {
  try {
    const raw = window.localStorage.getItem(SHOP_FLOOR_WORK_CENTER_STORAGE_KEY);
    const id = raw ? Number(raw) : Number.NaN;
    return Number.isInteger(id) && id > 0 ? id : undefined;
  } catch {
    return undefined;
  }
};

export const rememberWorkCenter = (workCenterId: number): void => {
  try {
    window.localStorage.setItem(SHOP_FLOOR_WORK_CENTER_STORAGE_KEY, String(workCenterId));
  } catch {
    // A private window or blocked storage: the choice holds for this visit only.
  }
};
