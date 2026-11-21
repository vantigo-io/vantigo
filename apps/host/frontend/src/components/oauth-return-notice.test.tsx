import { QueryClient } from "@tanstack/react-query";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { oauthReturnNavigation } from "../lib/oauth-return";
import { renderRoute } from "../test/render-route";
import { OAuthReturnNotice } from "./oauth-return-notice";
import "../i18n";

const api = vi.hoisted(() => ({ fetchSession: vi.fn() }));
vi.mock("../api/auth", () => ({ fetchSession: api.fetchSession, sessionQueryKey: ["auth", "session"] }));

const session = (mfaEnrollmentRequired: boolean) => ({
  user: { id: "u-1", displayName: "Kari", email: "kari@example.com", roles: ["Owner"] },
  isSystemAdmin: false,
  mfaEnrollmentRequired,
});
const entry = (at = Date.now()) =>
  sessionStorage.setItem("oauth.return", JSON.stringify({ path: "/oauth/authorize?client_id=c&state=1e3", at }));

let queryClient: QueryClient;
const renderNotice = async () => {
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute("/settings/security", () => <OAuthReturnNotice />, queryClient);
};

beforeEach(() => {
  sessionStorage.clear();
  api.fetchSession.mockReset();
  vi.spyOn(oauthReturnNavigation, "assign").mockImplementation(() => undefined);
});

describe("the way back to an app's request after MFA enrolment (OAuth connections design A27)", () => {
  it("waits while enrolment is required, keeping the entry fresh, and offers the button once it is done", async () => {
    const started = Date.now() - 9 * 60_000;
    entry(started);
    api.fetchSession.mockResolvedValue(session(true));
    await renderNotice();
    await waitFor(() => expect(Number(sessionStorage.getItem("oauth.return.touched"))).toBeGreaterThan(started));
    expect(screen.queryByRole("button", { name: "Continue to the app's request" })).not.toBeInTheDocument();

    // Enabling the authenticator refetches the session, which no longer needs enrolment.
    api.fetchSession.mockResolvedValue(session(false));
    await act(() => queryClient.invalidateQueries({ queryKey: ["auth", "session"] }));
    const button = await screen.findByRole("button", { name: "Continue to the app's request" });
    // Shown, not followed: the recovery codes stay on screen until the person chooses.
    expect(oauthReturnNavigation.assign).not.toHaveBeenCalled();
    expect(sessionStorage.getItem("oauth.return")).not.toBeNull();

    fireEvent.click(button);
    expect(oauthReturnNavigation.assign).toHaveBeenCalledWith("/oauth/authorize?client_id=c&state=1e3");
    expect(sessionStorage.getItem("oauth.return")).toBeNull();
    expect(sessionStorage.getItem("oauth.return.touched")).toBeNull();
  });

  it("shows nothing when no valid entry waits", async () => {
    sessionStorage.setItem("oauth.return", JSON.stringify({ path: "/workspace", at: Date.now() }));
    api.fetchSession.mockResolvedValue(session(false));
    await renderNotice();
    await waitFor(() => expect(api.fetchSession).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: "Continue to the app's request" })).not.toBeInTheDocument();
  });
});
