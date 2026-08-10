import "@testing-library/jest-dom/vitest";

import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { clearCsrfToken, setUnauthorizedHandler } from "../api/request";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  clearCsrfToken();
  setUnauthorizedHandler(undefined);
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
