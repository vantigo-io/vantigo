import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { OAuthAuthorizationPreview } from "../api/oauth";
import { renderRoute } from "../test/render-route";
import { OAuthAuthorizePage, oauthPageNavigation } from "./oauth.authorize";
import "../i18n";

const api = vi.hoisted(() => ({
  fetchSession: vi.fn(),
  previewAuthorization: vi.fn(),
  decideAuthorization: vi.fn(),
}));

vi.mock("../api/auth", () => ({ fetchSession: api.fetchSession, sessionQueryKey: ["auth", "session"] }));
vi.mock("../api/oauth", () => ({
  previewAuthorization: api.previewAuthorization,
  decideAuthorization: api.decideAuthorization,
}));

const query =
  "response_type=code&client_id=https%3A%2F%2Fclaude.ai%2Foauth%2Fmeta&redirect_uri=https%3A%2F%2Fclaude.ai%2Fcb&state=s1";

const session = (roles: string[], mfaEnrollmentRequired = false) => ({
  user: { id: "u-1", displayName: "Kari", email: "kari@example.com", roles },
  isSystemAdmin: false,
  mfaEnrollmentRequired,
});

const consent = (overrides: Partial<OAuthAuthorizationPreview> = {}): OAuthAuthorizationPreview => ({
  outcome: "consent",
  client: {
    name: "Claude",
    clientId: "https://claude.ai/oauth/meta",
    clientIdHost: "claude.ai",
    kind: "metadata",
    redirectHost: "claude.ai",
    loopback: false,
  },
  scopes: ["read", "write"],
  integrations: [
    { id: "i-agent", name: "Agent", eligible: true, maxAccess: "read_write", reason: null },
    { id: "i-reader", name: "Reader", eligible: true, maxAccess: "read", reason: null },
    { id: "i-off", name: "Old", eligible: false, maxAccess: null, reason: "mcp_off" },
  ],
  firstUse: false,
  nameWarning: false,
  ...overrides,
});

const nav = {
  assign: vi.fn(),
  closeWindow: vi.fn(),
};

const renderPage = async (q = query) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute("/oauth/authorize", () => <OAuthAuthorizePage query={q} />, queryClient);
};

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
  sessionStorage.clear();
  nav.assign = vi.spyOn(oauthPageNavigation, "assign").mockImplementation(() => undefined);
  nav.closeWindow = vi.spyOn(oauthPageNavigation, "closeWindow").mockImplementation(() => undefined);
  api.fetchSession.mockResolvedValue(session(["Owner"]));
});

describe("the OAuth consent page", () => {
  it("waits for the session and the server's judgement before showing anything", async () => {
    api.fetchSession.mockReturnValue(new Promise(() => {}));
    await renderPage();
    expect(screen.getByLabelText("Loading")).toBeInTheDocument();
    expect(api.previewAuthorization).not.toHaveBeenCalled();
  });

  it("sends a visitor without a session to sign in, keeping its way back", async () => {
    api.fetchSession.mockResolvedValue(null);
    await renderPage();
    await waitFor(() => expect(nav.assign).toHaveBeenCalledWith("/sign-in"));
    const entry = JSON.parse(sessionStorage.getItem("oauth.return") ?? "{}");
    expect(entry.path).toBe(`/oauth/authorize?${query}`);
    expect(typeof entry.at).toBe("number");
    expect(api.previewAuthorization).not.toHaveBeenCalled();
  });

  it("sends an Owner who must enrol MFA to enrol, keeping its way back", async () => {
    api.fetchSession.mockResolvedValue(session(["Owner"], true));
    await renderPage();
    await waitFor(() => expect(nav.assign).toHaveBeenCalledWith("/settings/security"));
    expect(sessionStorage.getItem("oauth.return")).toContain("/oauth/authorize?");
    expect(api.previewAuthorization).not.toHaveBeenCalled();
  });

  it("tells a person who is not an Owner to ask one, fetches nothing and offers only Close", async () => {
    api.fetchSession.mockResolvedValue(session(["Member"]));
    await renderPage();
    expect(await screen.findByRole("heading", { name: "Ask an Owner" })).toBeInTheDocument();
    expect(api.previewAuthorization).not.toHaveBeenCalled();
    expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual(["Close"]);
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(nav.closeWindow).toHaveBeenCalled();
    expect(nav.assign).toHaveBeenCalledWith("/");
  });

  it("never redirects for a refused client, and offers only Close", async () => {
    api.previewAuthorization.mockResolvedValue({
      outcome: "client_refused",
      error: "invalid_client",
      message: "The redirect_uri is not one the client registered.",
    });
    await renderPage();
    expect(await screen.findByRole("heading", { name: "This app cannot connect" })).toBeInTheDocument();
    expect(api.previewAuthorization).toHaveBeenCalledWith(query);
    expect(screen.getByText("The redirect_uri is not one the client registered.")).toBeInTheDocument();
    expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual(["Close"]);
    expect(nav.assign).not.toHaveBeenCalled();
  });

  it("redirects an error at once when the server says it may", async () => {
    api.previewAuthorization.mockResolvedValue({
      outcome: "redirect",
      error: "invalid_scope",
      message: "The scopes are read, write and offline_access.",
      redirectTo: "https://claude.ai/cb?error=invalid_scope&state=s1&iss=x",
      returnHost: "claude.ai",
      automatic: true,
    });
    await renderPage();
    await waitFor(() =>
      expect(nav.assign).toHaveBeenCalledWith("https://claude.ai/cb?error=invalid_scope&state=s1&iss=x"),
    );
  });

  it("puts an error behind a Return to button when the server does not say it may redirect", async () => {
    api.previewAuthorization.mockResolvedValue({
      outcome: "redirect",
      error: "invalid_request",
      message: "PKCE is required.",
      redirectTo: "https://app.example/cb?error=invalid_request&iss=x",
      returnHost: "app.example",
      automatic: false,
    });
    await renderPage();
    const back = await screen.findByRole("button", { name: "Return to app.example" });
    expect(screen.getByText("PKCE is required.")).toBeInTheDocument();
    expect(nav.assign).not.toHaveBeenCalled();
    fireEvent.click(back);
    expect(nav.assign).toHaveBeenCalledWith("https://app.example/cb?error=invalid_request&iss=x");
  });

  it("shows the claimed name beside the verified hosts, with no integration chosen and Read only", async () => {
    api.previewAuthorization.mockResolvedValue(consent());
    await renderPage();
    expect(await screen.findByText("The app calls itself")).toBeInTheDocument();
    expect(screen.getByTestId("oauth-client-name")).toHaveTextContent("Claude");
    const verified = within(screen.getByTestId("oauth-verified"));
    expect(verified.getByText(/Vantigo read its description from/)).toBeInTheDocument();
    expect(verified.getAllByText("claude.ai")).toHaveLength(2);
    expect(screen.queryByText("Check who this app is")).not.toBeInTheDocument();
    expect(screen.queryByText("An app on this computer")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Integration", { selector: "input" })).toHaveValue("");
    expect(screen.getByRole("radio", { name: "Read only" })).toBeChecked();
    expect(screen.getByRole("radio", { name: /Read and write/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
  });

  it("warns of a name that borrows Vantigo's, a loopback redirect and a host never connected", async () => {
    api.previewAuthorization.mockResolvedValue(
      consent({
        client: {
          name: "Vantigo Helper",
          clientId: "https://tools.example/meta",
          clientIdHost: "tools.example",
          kind: "metadata",
          redirectHost: "127.0.0.1",
          loopback: true,
        },
        nameWarning: true,
        firstUse: true,
      }),
    );
    await renderPage();
    expect(await screen.findByText("Check who this app is")).toBeInTheDocument();
    expect(screen.getByText("An app on this computer")).toBeInTheDocument();
    expect(
      screen.getByText("No app from tools.example has been connected to this workspace before."),
    ).toBeInTheDocument();
    // A loopback redirect is not called verified.
    expect(screen.queryByText(/Vantigo read its description from/)).not.toBeInTheDocument();
    expect(screen.getByText(/Its description is hosted at/)).toBeInTheDocument();
  });

  it("approves with the integration and the access chosen, and follows the server's redirect", async () => {
    api.previewAuthorization.mockResolvedValue(consent());
    api.decideAuthorization.mockResolvedValue({ redirectTo: "https://claude.ai/cb?code=c1&state=s1&iss=x" });
    await renderPage();
    fireEvent.click(await screen.findByLabelText("Integration", { selector: "input" }));
    expect(await screen.findByRole("option", { name: "Old — MCP is off" })).toHaveAttribute(
      "data-combobox-disabled",
      "true",
    );
    fireEvent.click(screen.getByRole("option", { name: "Agent" }));
    const write = screen.getByRole("radio", { name: /Read and write/ });
    await waitFor(() => expect(write).toBeEnabled());
    fireEvent.click(write);
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() =>
      expect(api.decideAuthorization).toHaveBeenCalledWith({
        query,
        decision: "approve",
        integrationId: "i-agent",
        access: "read_write",
      }),
    );
    await waitFor(() => expect(nav.assign).toHaveBeenCalledWith("https://claude.ai/cb?code=c1&state=s1&iss=x"));
  });

  it("keeps Read only for an integration that allows only reading", async () => {
    api.previewAuthorization.mockResolvedValue(consent());
    api.decideAuthorization.mockResolvedValue({ redirectTo: "https://claude.ai/cb?code=c1" });
    await renderPage();
    fireEvent.click(await screen.findByLabelText("Integration", { selector: "input" }));
    fireEvent.click(await screen.findByRole("option", { name: "Reader" }));
    expect(screen.getByRole("radio", { name: /Read and write/ })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() =>
      expect(api.decideAuthorization).toHaveBeenCalledWith({
        query,
        decision: "approve",
        integrationId: "i-reader",
        access: "read",
      }),
    );
  });

  it("denies, and follows the server's redirect", async () => {
    api.previewAuthorization.mockResolvedValue(consent());
    api.decideAuthorization.mockResolvedValue({ redirectTo: "https://claude.ai/cb?error=access_denied&iss=x" });
    await renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Deny" }));
    await waitFor(() => expect(api.decideAuthorization).toHaveBeenCalledWith({ query, decision: "deny" }));
    await waitFor(() => expect(nav.assign).toHaveBeenCalledWith("https://claude.ai/cb?error=access_denied&iss=x"));
  });

  it("does not redirect when the approval refuses the client", async () => {
    api.previewAuthorization.mockResolvedValue(consent());
    api.decideAuthorization.mockRejectedValue(
      Object.assign(new Error("refused"), { status: 400, code: "client_refused" }),
    );
    await renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Deny" }));
    expect(await screen.findByRole("heading", { name: "This app cannot connect" })).toBeInTheDocument();
    expect(nav.assign).not.toHaveBeenCalled();
  });

  it("says when OAuth is switched off here", async () => {
    api.previewAuthorization.mockRejectedValue(Object.assign(new Error("not found"), { status: 404 }));
    await renderPage();
    expect(
      await screen.findByText("Connecting apps by signing in is switched off on this installation."),
    ).toBeInTheDocument();
    expect(nav.assign).not.toHaveBeenCalled();
  });

  it("asks nothing of the server for a page opened without a request", async () => {
    await renderPage("");
    expect(await screen.findByText("This page was opened without a connection request.")).toBeInTheDocument();
    expect(api.previewAuthorization).not.toHaveBeenCalled();
  });
});
