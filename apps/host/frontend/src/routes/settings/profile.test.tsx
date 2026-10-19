import { QueryClient } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { PersonalProfile } from "../../api/account";
import { renderRoute } from "../../test/render-route";
import { Route as ProfileRoute } from "./profile";
import "../../i18n";

const { getProfile, updateProfile, fetchSession } = vi.hoisted(() => ({
  getProfile: vi.fn(),
  updateProfile: vi.fn(),
  fetchSession: vi.fn(),
}));

vi.mock("../../api/account", () => ({
  getProfile,
  updateProfile,
  uploadProfilePhoto: vi.fn(),
  removeProfilePhoto: vi.fn(),
  profileQueryKey: (userId: string) => ["account", "profile", userId],
}));
vi.mock("../../api/auth", () => ({ fetchSession, sessionQueryKey: ["auth", "session"] }));

const profile = (overrides: Partial<PersonalProfile> = {}): PersonalProfile => ({
  displayName: "Kari Nordmann",
  email: "kari@example.com",
  preferredLanguage: "en",
  avatarUrl: null,
  timeZone: null,
  ...overrides,
});

const renderProfile = async () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute("/settings/profile", ProfileRoute.options.component as never, queryClient);
  return screen.findByLabelText("Time zone", { selector: "input:not([type=hidden])" });
};

beforeEach(() => {
  getProfile.mockReset();
  updateProfile.mockReset();
  fetchSession.mockResolvedValue({
    user: { id: "user-1", displayName: "Kari Nordmann", email: "kari@example.com", roles: [] },
    isSystemAdmin: false,
    timeZone: "Europe/Oslo",
    timeZoneSource: "business",
    businessTimeZone: "Europe/Oslo",
  });
});

describe("the profile's time zone", () => {
  it("follows the workspace by default, naming its zone, and saves that as null", async () => {
    getProfile.mockResolvedValue(profile());
    updateProfile.mockImplementation(async (body) => profile(body));
    const field = await renderProfile();
    await waitFor(() => expect(field).toHaveValue("Same as the workspace (Europe/Oslo)"));

    await userEvent.click(screen.getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(updateProfile).toHaveBeenCalled());
    expect(updateProfile.mock.calls[0][0]).toMatchObject({ displayName: "Kari Nordmann", timeZone: null });
  });

  it("saves a chosen zone, and shows a zone the server refuses on the field", async () => {
    getProfile.mockResolvedValue(profile({ timeZone: "America/New_York" }));
    updateProfile.mockRejectedValue(
      Object.assign(new Error("The profile request is invalid."), {
        fields: { timeZone: ["'CET' means one thing to this installation and another to its database"] },
      }),
    );
    const field = await renderProfile();
    await waitFor(() => expect(field).toHaveValue("America/New_York"));

    await userEvent.click(screen.getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(updateProfile).toHaveBeenCalled());
    expect(updateProfile.mock.calls[0][0]).toMatchObject({ timeZone: "America/New_York" });
    expect(await screen.findByText(/means one thing to this installation/)).toBeInTheDocument();
  });
});
