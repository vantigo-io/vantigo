import { QueryClient } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { PersonalProfile } from "../../api/account";
import { renderRoute } from "../../test/render-route";
import { Route as ProfileRoute } from "./profile";
import "../../i18n";

const { getProfile, updateProfile, removeProfilePhoto, fetchSession } = vi.hoisted(() => ({
  getProfile: vi.fn(),
  updateProfile: vi.fn(),
  removeProfilePhoto: vi.fn(),
  fetchSession: vi.fn(),
}));

vi.mock("../../api/account", () => ({
  getProfile,
  updateProfile,
  uploadProfilePhoto: vi.fn(),
  removeProfilePhoto,
  profileQueryKey: (userId: string) => ["account", "profile", userId],
}));
vi.mock("../../api/auth", () => ({ fetchSession, sessionQueryKey: ["auth", "session"] }));

const profile = (overrides: Partial<PersonalProfile> = {}): PersonalProfile => ({
  displayName: "Kari Nordmann",
  email: "kari@example.com",
  preferredLanguage: "en",
  avatarUrl: null,
  avatarManagedBy: null,
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
  removeProfilePhoto.mockReset();
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

describe("the profile's photo", () => {
  it("disables the photo controls when Staff keeps the photo", async () => {
    getProfile.mockResolvedValue(
      profile({ avatarUrl: "/api/v1/identity/account/avatar?v=managed", avatarManagedBy: "staff" }),
    );
    await renderProfile();

    // The picture is still shown: Staff's photo is the user's avatar.
    expect(document.querySelector('img[src="/api/v1/identity/account/avatar?v=managed"]')).toBeInTheDocument();
    const field = screen.getByLabelText("Choose a photo");
    expect(field).toBeDisabled();
    // The hint is the field's description, which a screen reader hears with it.
    expect(field).toHaveAccessibleDescription("Your photo is kept by Staff. Ask HR to change it.");
    expect(screen.getByRole("button", { name: "Upload photo" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Remove" })).toBeDisabled();
  });

  it("leaves the photo controls to the user otherwise", async () => {
    getProfile.mockResolvedValue(profile({ avatarUrl: "/api/v1/identity/account/avatar?v=own" }));
    await renderProfile();

    expect(screen.queryByText("Your photo is kept by Staff. Ask HR to change it.")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Choose a photo")).toBeEnabled();
    expect(screen.getByRole("button", { name: "Remove" })).toBeEnabled();
  });

  it("says in its own words that Staff keeps the photo when it was set meanwhile, and closes the controls", async () => {
    getProfile.mockResolvedValueOnce(profile({ avatarUrl: "/api/v1/identity/account/avatar?v=own" }));
    getProfile.mockResolvedValue(
      profile({ avatarUrl: "/api/v1/identity/account/avatar?v=managed", avatarManagedBy: "staff" }),
    );
    removeProfilePhoto.mockRejectedValue(
      Object.assign(new Error("Your photo is kept by Staff and cannot be changed here."), {
        status: 409,
        code: "avatar_managed",
      }),
    );
    await renderProfile();

    await userEvent.click(screen.getByRole("button", { name: "Remove" }));

    const said = await screen.findAllByText("Your photo is kept by Staff. Ask HR to change it.");
    expect(said.length).toBeGreaterThan(0);
    expect(screen.queryByText("Your photo is kept by Staff and cannot be changed here.")).not.toBeInTheDocument();
    // The profile is read again, so the controls close.
    await waitFor(() => expect(getProfile).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByRole("button", { name: "Remove" })).toBeDisabled());
    expect(screen.getByLabelText("Choose a photo")).toBeDisabled();
  });
});
