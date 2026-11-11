import { afterEach, describe, expect, it, vi } from "vitest";
import { readRememberedWorkCenter, rememberWorkCenter, SHOP_FLOOR_WORK_CENTER_STORAGE_KEY } from "./shop-floor-storage";

describe("the remembered work center", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    try {
      window.localStorage.removeItem(SHOP_FLOOR_WORK_CENTER_STORAGE_KEY);
    } catch {
      // Nothing to clean when storage is unavailable.
    }
  });

  it("reads back what was remembered", () => {
    rememberWorkCenter(3);
    expect(readRememberedWorkCenter()).toBe(3);
  });

  it("does not remember what is not a positive id", () => {
    rememberWorkCenter(Number.NaN);
    rememberWorkCenter(0);
    expect(window.localStorage.getItem(SHOP_FLOOR_WORK_CENTER_STORAGE_KEY)).toBeNull();
  });

  it("ignores what is not a positive id", () => {
    window.localStorage.setItem(SHOP_FLOOR_WORK_CENTER_STORAGE_KEY, "SAW");
    expect(readRememberedWorkCenter()).toBeUndefined();
  });

  it("is a convenience only: blocked storage neither throws nor remembers", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });
    expect(() => rememberWorkCenter(3)).not.toThrow();
    expect(readRememberedWorkCenter()).toBeUndefined();
  });
});
