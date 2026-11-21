import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { OAuthAuthorizationPreview } from "../api/oauth";
import { renderRoute } from "../test/render-route";
import { isAllowedRedirect, OAuthAuthorizePage, oauthPageNavigation } from "./oauth.authorize";
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
    expect(screen.getByText(/description could not be fetched or accepted/)).toBeInTheDocument();
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

  it("says who registered a registered client, instead of where its description is", async () => {
    api.previewAuthorization.mockResolvedValue(
      consent({
        client: {
          name: "Cursor",
          clientId: "vtc_0123456789abcdefghijABCD",
          clientIdHost: "127.0.0.1",
          kind: "registered",
          redirectHost: "127.0.0.1",
          loopback: true,
          registeredBy: "Kari Owner",
        },
      }),
    );
    await renderPage();
    expect(await screen.findByTestId("oauth-registered-by")).toHaveTextContent(
      "Registered in this workspace by Kari Owner",
    );
    const verified = within(screen.getByTestId("oauth-verified"));
    expect(verified.queryByText(/read its description from/)).not.toBeInTheDocument();
    expect(verified.getByText("127.0.0.1")).toBeInTheDocument();
    expect(screen.getByText("An app on this computer")).toBeInTheDocument();
  });

  it("names no one once the person who registered a client no longer vouches for it", async () => {
    api.previewAuthorization.mockResolvedValue(
      consent({
        client: {
          name: "In-house app",
          clientId: "vtc_0123456789abcdefghijABCD",
          clientIdHost: "app.example.com",
          kind: "registered",
          redirectHost: "app.example.com",
          loopback: false,
          registeredBy: null,
        },
      }),
    );
    await renderPage();
    expect(await screen.findByTestId("oauth-registered-by")).toHaveTextContent(/^Registered in this workspace$/);
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

  it("tells a non-Owner held at enrolment to ask an Owner, and sends them nowhere", async () => {
    api.fetchSession.mockResolvedValue({ ...session(["Member"], true), isSystemAdmin: true });
    await renderPage();
    expect(await screen.findByRole("heading", { name: "Ask an Owner" })).toBeInTheDocument();
    expect(nav.assign).not.toHaveBeenCalled();
    expect(sessionStorage.getItem("oauth.return")).toBeNull();
  });

  it("says the request could not be read when the session cannot be", async () => {
    api.fetchSession.mockRejectedValue(Object.assign(new Error("boom"), { status: 500 }));
    await renderPage();
    expect(await screen.findByRole("heading", { name: "The request could not be read" })).toBeInTheDocument();
    expect(api.previewAuthorization).not.toHaveBeenCalled();
  });

  it.each([
    [403, "Your session may not approve connections."],
    [429, "Too many connection requests in a short time."],
    [400, "Something went wrong."],
  ])("explains a preview answered %i, and redirects nowhere", async (status, text) => {
    api.previewAuthorization.mockRejectedValue(Object.assign(new Error("refused"), { status }));
    await renderPage();
    expect(await screen.findByText(new RegExp(text))).toBeInTheDocument();
    expect(nav.assign).not.toHaveBeenCalled();
  });

  it("goes back to sign in when the session expired before the preview", async () => {
    api.previewAuthorization.mockRejectedValue(Object.assign(new Error("expired"), { status: 401 }));
    await renderPage();
    await waitFor(() => expect(nav.assign).toHaveBeenCalledWith("/sign-in"));
    expect(JSON.parse(sessionStorage.getItem("oauth.return") ?? "{}").path).toBe(`/oauth/authorize?${query}`);
  });

  it("goes back to sign in when the session expired before the approval", async () => {
    api.previewAuthorization.mockResolvedValue(consent());
    api.decideAuthorization.mockRejectedValue(Object.assign(new Error("expired"), { status: 401 }));
    await renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Deny" }));
    await waitFor(() => expect(nav.assign).toHaveBeenCalledWith("/sign-in"));
    expect(sessionStorage.getItem("oauth.return")).toContain("/oauth/authorize?");
  });

  it("reads the integrations again when the approval finds the one chosen no longer eligible", async () => {
    api.previewAuthorization.mockResolvedValue(consent());
    api.decideAuthorization.mockRejectedValue(
      Object.assign(new Error("conflict"), { status: 409, code: "integration_not_eligible" }),
    );
    await renderPage();
    fireEvent.click(await screen.findByLabelText("Integration", { selector: "input" }));
    fireEvent.click(await screen.findByRole("option", { name: "Agent" }));
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    expect(await screen.findByText(/cannot be connected with that access any more/)).toBeInTheDocument();
    await waitFor(() => expect(api.previewAuthorization).toHaveBeenCalledTimes(2));
    expect(nav.assign).not.toHaveBeenCalled();
  });

  it.each([
    [429, "Too many connection requests in a short time."],
    [500, "Something went wrong."],
  ])("explains an approval answered %i, and lets the person try again", async (status, text) => {
    api.previewAuthorization.mockResolvedValue(consent());
    api.decideAuthorization.mockRejectedValue(Object.assign(new Error("failed"), { status }));
    await renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Deny" }));
    expect(await screen.findByText(new RegExp(text))).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Deny" })).toBeEnabled();
    expect(nav.assign).not.toHaveBeenCalled();
  });

  it("posts one decision however often Approve is pressed", async () => {
    api.previewAuthorization.mockResolvedValue(consent());
    api.decideAuthorization.mockReturnValue(new Promise(() => {}));
    await renderPage();
    fireEvent.click(await screen.findByLabelText("Integration", { selector: "input" }));
    fireEvent.click(await screen.findByRole("option", { name: "Agent" }));
    const approve = screen.getByRole("button", { name: "Approve" });
    fireEvent.click(approve);
    fireEvent.click(approve);
    fireEvent.click(screen.getByRole("button", { name: "Deny" }));
    await waitFor(() => expect(api.decideAuthorization).toHaveBeenCalledTimes(1));
  });

  it.each([["javascript:alert(1)"], ["http://evil.example/cb?code=c1"], ["data:text/html,hi"]])(
    "refuses to send the browser to %s, whatever the server answered",
    async (to) => {
      api.previewAuthorization.mockResolvedValue(consent());
      api.decideAuthorization.mockResolvedValue({ redirectTo: to });
      await renderPage();
      fireEvent.click(await screen.findByRole("button", { name: "Deny" }));
      expect(await screen.findByText(/not one this page will send you to/)).toBeInTheDocument();
      expect(nav.assign).not.toHaveBeenCalled();
    },
  );

  it("refuses an automatic redirect to an address it does not allow", async () => {
    api.previewAuthorization.mockResolvedValue({
      outcome: "redirect",
      error: "invalid_scope",
      message: null,
      redirectTo: "javascript:alert(1)",
      returnHost: "x",
      automatic: true,
    });
    await renderPage();
    expect(await screen.findByText(/not one this page will send you to/)).toBeInTheDocument();
    expect(nav.assign).not.toHaveBeenCalled();
  });

  it("allows https anywhere and http only to this computer", () => {
    expect(isAllowedRedirect("https://claude.ai/cb?code=1")).toBe(true);
    expect(isAllowedRedirect("http://127.0.0.1:33418/callback")).toBe(true);
    expect(isAllowedRedirect("http://[::1]:5000/cb")).toBe(true);
    expect(isAllowedRedirect("http://localhost:5000/cb")).toBe(true);
    expect(isAllowedRedirect("http://localhost.evil.example/cb")).toBe(false);
    expect(isAllowedRedirect("https://user:pw@claude.ai/cb")).toBe(false);
    expect(isAllowedRedirect("vbscript:x")).toBe(false);
    expect(isAllowedRedirect("/relative")).toBe(false);
  });

  it("renders nothing and asks nothing inside a frame", async () => {
    const top = Object.getOwnPropertyDescriptor(window, "top");
    Object.defineProperty(window, "top", { configurable: true, get: () => ({}) });
    try {
      await renderPage();
      await new Promise((resolve) => setTimeout(resolve, 20));
      expect(screen.queryByRole("heading")).not.toBeInTheDocument();
      expect(screen.queryByRole("status")).not.toBeInTheDocument();
      expect(api.fetchSession).not.toHaveBeenCalled();
    } finally {
      if (top) Object.defineProperty(window, "top", top);
    }
  });

  it("keeps a long or stacked name to its box", async () => {
    api.previewAuthorization.mockResolvedValue(
      consent({
        client: { ...(consent().client as NonNullable<OAuthAuthorizationPreview["client"]>), name: "W".repeat(80) },
      }),
    );
    await renderPage();
    const name = await screen.findByTestId("oauth-client-name");
    expect(name.getAttribute("data-line-clamp") ?? name.className).toBeTruthy();
    expect(name.style.overflowWrap).toBe("anywhere");
  });
});
