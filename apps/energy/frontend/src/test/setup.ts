import "@testing-library/jest-dom/vitest";

import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { setUnauthorizedHandler } from "../api/request";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  setUnauthorizedHandler(undefined);
  window.localStorage.clear();
});

Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
});

class ResizeObserverMock {
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
}

window.ResizeObserver = window.ResizeObserver ?? ResizeObserverMock;
window.HTMLElement.prototype.scrollIntoView = window.HTMLElement.prototype.scrollIntoView ?? vi.fn();
window.scrollTo = vi.fn();

// Node 25 and later define a global `localStorage` of their own, which
// shadows jsdom's and is unusable without `--localstorage-file`. The Prices
// page remembers its areas there, so the suite gets an in-memory one whatever
// Node it runs on.
const storageWorks = (() => {
  try {
    return typeof window.localStorage?.getItem === "function";
  } catch {
    return false;
  }
})();
if (!storageWorks) {
  const values = new Map<string, string>();
  const memoryStorage: Storage = {
    get length() {
      return values.size;
    },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => [...values.keys()][index] ?? null,
    removeItem: (key) => void values.delete(key),
    setItem: (key, value) => void values.set(key, String(value)),
  };
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: memoryStorage });
  Object.defineProperty(window, "localStorage", { configurable: true, value: memoryStorage });
}
