import "@testing-library/jest-dom/vitest";

import { notifications } from "@mantine/notifications";
import { cleanup, configure } from "@testing-library/react";
import { afterEach, vi } from "vitest";

// testing-library gives findBy/waitFor one second by default, which a first
// render of Mantine plus FullCalendar can exceed on a single slow core — a CI
// runner or a pinned CPU. The wait still ends as soon as the assertion passes,
// so the only thing a longer ceiling costs is how long a genuinely failing
// test takes to say so.
configure({ asyncUtilTimeout: 5000 });

afterEach(() => {
  // The notification store is a module-level singleton: without this, one
  // test's notification is still on screen during the next one.
  notifications.clean();
  notifications.cleanQueue();
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

// Mantine components rely on browser APIs that jsdom does not implement.
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
// shadows jsdom's and is unusable without `--localstorage-file`. The legend's
// filter keeps its state there, so the suite gets an in-memory one whatever
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
