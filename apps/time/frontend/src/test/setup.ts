import "@testing-library/jest-dom/vitest";

import { notifications } from "@mantine/notifications";
import { cleanup, configure } from "@testing-library/react";
import { afterEach, vi } from "vitest";

import { setAuthStateClearer, setUnauthorizedHandler } from "../api/request";

// testing-library gives findBy/waitFor one second by default, which a first
// render of the memory router plus Mantine can exceed on a single slow core —
// a CI runner or a pinned CPU. The wait still ends as soon as the assertion
// passes, so the only thing a longer ceiling costs is how long a genuinely
// failing test takes to say so.
configure({ asyncUtilTimeout: 5000 });

afterEach(() => {
  // The notification store is a module-level singleton: without this, one
  // test's success notification is still on screen during the next one.
  notifications.clean();
  notifications.cleanQueue();
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  setUnauthorizedHandler(undefined);
  setAuthStateClearer(undefined);
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

// jsdom does not implement the async clipboard API used by useClipboard.
Object.defineProperty(navigator, "clipboard", {
  writable: true,
  value: {
    writeText: async () => {},
  },
});
