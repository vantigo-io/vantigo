import "@testing-library/jest-dom/vitest";

import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";

import { clearCsrfToken, setAuthStateClearer, setUnauthorizedHandler } from "../api/request";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  clearCsrfToken();
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
// A plain async function (not vi.fn) so restoreAllMocks cannot strip the
// implementation; tests spy on it with vi.spyOn when asserting copies.
Object.defineProperty(navigator, "clipboard", {
  writable: true,
  value: {
    writeText: async () => {},
  },
});
