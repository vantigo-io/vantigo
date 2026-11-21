import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { IntegrationConnection } from "../../api/oauth";
import { renderRoute } from "../../test/render-route";
import { IntegrationConnections } from "./-api-access-connections";
import "../../i18n";

const api = vi.hoisted(() => ({ listIntegrationConnections: vi.fn(), revokeIntegrationConnection: vi.fn() }));

vi.mock("../../api/oauth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/oauth")>()),
  listIntegrationConnections: api.listIntegrationConnections,
  revokeIntegrationConnection: api.revokeIntegrationConnection,
}));

const connection = (overrides: Partial<IntegrationConnection> = {}): IntegrationConnection => ({
  id: "c-1",
  clientId: "https://claude.ai/oauth/claude-code-client-metadata",
  clientKind: "metadata",
  clientName: "Claude Code",
  clientHost: "claude.ai",
  scopes: ["read", "write"],
  authorizedBy: "u-1",
  authorizedByName: "Kari Owner",
  connectedAt: "2026-11-20T10:00:00Z",
  expiresAt: "2027-05-19T10:00:00Z",
  lastUsedAt: "2026-11-21T08:00:00Z",
  lastUsedIp: "203.0.113.7",
  status: "active",
  inactiveReason: null,
  ...overrides,
});

const renderSection = async (archived = false) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute(
    "/workspace/api-access/i-1",
    () => <IntegrationConnections integrationId="i-1" archived={archived} />,
    queryClient,
  );
};

beforeEach(() => {
  api.listIntegrationConnections.mockReset();
  api.revokeIntegrationConnection.mockReset();
});

describe("an integration's connected apps", () => {
  it("shows each connection's kind, and why one whose host is no longer allowed is held back", async () => {
    api.listIntegrationConnections.mockResolvedValue([
      connection(),
      connection({
        id: "c-3",
        clientId: "vtc_0123456789abcdefghijABCD",
        clientKind: "registered",
        clientName: "Cursor",
        clientHost: "127.0.0.1",
      }),
      connection({
        id: "c-4",
        clientName: "ChatGPT",
        clientHost: "chatgpt.com",
        status: "inactive",
        inactiveReason: "client_not_allowed",
      }),
    ]);
    await renderSection();
    const table = within(await screen.findByTestId("connected-apps"));
    const kinds = table.getAllByTestId("connection-kind").map((badge) => badge.textContent);
    expect(kinds).toEqual(["Client description", "Registered client", "Client description"]);
    expect(table.getByText(/not on this workspace's list of client hosts/)).toBeInTheDocument();
  });

  it("lists each connection with its host, who connected it, its access, dates and state", async () => {
    api.listIntegrationConnections.mockResolvedValue([
      connection(),
      connection({
        id: "c-2",
        clientName: "ChatGPT",
        clientHost: "chatgpt.com",
        scopes: ["read"],
        authorizedBy: null,
        authorizedByName: null,
        lastUsedAt: null,
        lastUsedIp: null,
        status: "inactive",
        inactiveReason: "authorizer_removed",
      }),
    ]);
    await renderSection();
    expect(await screen.findByRole("heading", { name: "Connected apps" })).toBeInTheDocument();
    const first = within(await screen.findByText("Claude Code").then((el) => el.closest("tr") as HTMLElement));
    expect(first.getByText("claude.ai")).toBeInTheDocument();
    expect(first.getByText("Kari Owner")).toBeInTheDocument();
    expect(first.getByText("Read and write")).toBeInTheDocument();
    expect(first.getByText("Active")).toBeInTheDocument();
    expect(first.getByText(/203\.0\.113\.7/)).toBeInTheDocument();
    const second = within(screen.getByText("ChatGPT").closest("tr") as HTMLElement);
    expect(second.getByText("chatgpt.com")).toBeInTheDocument();
    expect(second.getByText("Read only")).toBeInTheDocument();
    expect(second.getByText("Inactive")).toBeInTheDocument();
    expect(second.getByText(/no longer an active Owner/)).toBeInTheDocument();
    expect(second.getByText("Deleted person")).toBeInTheDocument();
    expect(second.getByText("Never")).toBeInTheDocument();
    expect(api.listIntegrationConnections).toHaveBeenCalledWith("i-1");
  });

  it("says when no app is connected", async () => {
    api.listIntegrationConnections.mockResolvedValue([]);
    await renderSection();
    expect(await screen.findByText("No app is connected.")).toBeInTheDocument();
  });

  it("is not shown while the installation has OAuth switched off", async () => {
    api.listIntegrationConnections.mockRejectedValue(Object.assign(new Error("Not found"), { status: 404 }));
    await renderSection();
    await waitFor(() => expect(api.listIntegrationConnections).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByRole("heading", { name: "Connected apps" })).not.toBeInTheDocument());
  });

  it("revokes a connection after a confirmation, and reads the list again", async () => {
    api.listIntegrationConnections.mockResolvedValueOnce([connection()]).mockResolvedValue([]);
    api.revokeIntegrationConnection.mockResolvedValue(undefined);
    await renderSection();
    fireEvent.click(await screen.findByRole("button", { name: "Revoke Claude Code" }));
    const dialog = await screen.findByRole("dialog", { name: "Revoke Claude Code?" });
    expect(within(dialog).getByText(/The app from claude\.ai is refused from its next request/)).toBeInTheDocument();
    expect(api.revokeIntegrationConnection).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(api.revokeIntegrationConnection).toHaveBeenCalledWith("i-1", "c-1"));
    expect(await screen.findByText("No app is connected.")).toBeInTheDocument();
  });

  it("offers no Revoke on an archived integration", async () => {
    api.listIntegrationConnections.mockResolvedValue([connection()]);
    await renderSection(true);
    expect(await screen.findByText("Claude Code")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Revoke Claude Code" })).not.toBeInTheDocument();
  });
});
