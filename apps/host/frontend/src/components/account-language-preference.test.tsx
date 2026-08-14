import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, waitFor } from "@testing-library/react";
import { getLanguagePreference, setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AccountLanguagePreference } from "./account-language-preference";

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
const profile = { preferredLanguage: "nb" as const };

const renderPreference = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const rendered = render(
    <QueryClientProvider client={queryClient}>
      <AccountLanguagePreference />
    </QueryClientProvider>,
  );
  return { ...rendered, queryClient };
};

beforeEach(() => {
  setLanguagePreference("auto");
  fetchSessionMock.mockReset();
  getProfileMock.mockReset();
});

afterEach(() => {
  setLanguagePreference("auto");
});

describe("AccountLanguagePreference", () => {
  it("applies the canonical profile preference and resets when the session disappears", async () => {
    fetchSessionMock.mockResolvedValue(session);
    getProfileMock.mockResolvedValue(profile);
    const { queryClient } = renderPreference();

    await waitFor(() => expect(getLanguagePreference()).toBe("nb"));

    queryClient.setQueryData(["auth", "session"], null);
    await waitFor(() => expect(getLanguagePreference()).toBe("auto"));
  });

  it("does not clear the preference during a profile refetch", async () => {
    fetchSessionMock.mockResolvedValue(session);
    getProfileMock.mockResolvedValue(profile);
    const { queryClient } = renderPreference();

    await waitFor(() => expect(getLanguagePreference()).toBe("nb"));
    await queryClient.invalidateQueries({ queryKey: ["account", "profile", "user-1"] });

    expect(getLanguagePreference()).toBe("nb");
  });
});
