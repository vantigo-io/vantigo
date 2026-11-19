import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, waitFor } from "@testing-library/react";
import { vantigoColorSchemeManager } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AccountColorSchemePreference } from "./account-color-scheme-preference";

const { fetchSessionMock, getProfileMock } = vi.hoisted(() => ({
  fetchSessionMock: vi.fn(),
  getProfileMock: vi.fn(),
}));

vi.mock("../api/auth", () => ({
  fetchSession: fetchSessionMock,
  sessionQueryKey: ["auth", "session"],
}));
vi.mock("../api/account", () => ({
  getProfile: getProfileMock,
  profileQueryKey: (userId: string) => ["account", "profile", userId],
}));

const session = { user: { id: "user-1" } };
const attribute = () => document.documentElement.getAttribute("data-mantine-color-scheme");

/** The setup file's matchMedia says the computer is light; this says it is dark. */
const computerIsDark = () => {
  const original = window.matchMedia;
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: query.includes("prefers-color-scheme: dark"),
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }));
  return () => {
    window.matchMedia = original;
  };
};

const renderPreference = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MantineProvider env="test" defaultColorScheme="auto" colorSchemeManager={vantigoColorSchemeManager}>
      <QueryClientProvider client={queryClient}>
        <AccountColorSchemePreference />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

beforeEach(() => {
  window.localStorage.clear();
  document.documentElement.removeAttribute("data-mantine-color-scheme");
  fetchSessionMock.mockReset();
  getProfileMock.mockReset();
});

afterEach(() => {
  window.localStorage.clear();
});

describe("AccountColorSchemePreference", () => {
  it("applies the profile's scheme, and keeps it in the local mirror the next first paint reads", async () => {
    fetchSessionMock.mockResolvedValue(session);
    getProfileMock.mockResolvedValue({ colorScheme: "dark" });
    renderPreference();

    await waitFor(() => expect(attribute()).toBe("dark"));
    expect(window.localStorage.getItem("vantigo-color-scheme")).toBe("dark");
  });

  it("takes the server's light over a dark mirror: the account is the truth", async () => {
    window.localStorage.setItem("vantigo-color-scheme", "dark");
    fetchSessionMock.mockResolvedValue(session);
    getProfileMock.mockResolvedValue({ colorScheme: "light" });
    renderPreference();

    await waitFor(() => expect(attribute()).toBe("light"));
    expect(window.localStorage.getItem("vantigo-color-scheme")).toBe("light");
  });

  it("follows the computer for automatic, replacing a mirror left by an earlier choice", async () => {
    window.localStorage.setItem("vantigo-color-scheme", "light");
    const restore = computerIsDark();
    try {
      fetchSessionMock.mockResolvedValue(session);
      getProfileMock.mockResolvedValue({ colorScheme: "auto" });
      renderPreference();

      await waitFor(() => expect(attribute()).toBe("dark"));
      expect(window.localStorage.getItem("vantigo-color-scheme")).toBe("auto");
    } finally {
      restore();
    }
  });

  it("leaves the scheme alone without a session", async () => {
    window.localStorage.setItem("vantigo-color-scheme", "dark");
    fetchSessionMock.mockResolvedValue(null);
    renderPreference();

    await waitFor(() => expect(fetchSessionMock).toHaveBeenCalled());
    expect(getProfileMock).not.toHaveBeenCalled();
    expect(window.localStorage.getItem("vantigo-color-scheme")).toBe("dark");
  });
});
