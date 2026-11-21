import { notifications } from "@mantine/notifications";
import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { IntegrationDetail, IntegrationMcpAccess } from "../../api/integrations";
import { apiAccessCatalog } from "../../catalogs/api-access";
import { renderRoute } from "../../test/render-route";
import { IntegrationMcp } from "./-api-access-mcp";
import { mcpSnippets } from "./-api-access-shared";
import "../../i18n";

const api = vi.hoisted(() => ({ setIntegrationMcpAccess: vi.fn(), listIntegrationConnections: vi.fn() }));

vi.mock("../../api/integrations", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/integrations")>()),
  setIntegrationMcpAccess: api.setIntegrationMcpAccess,
}));

vi.mock("../../api/oauth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/oauth")>()),
  listIntegrationConnections: api.listIntegrationConnections,
}));

const switchedOff = () => Object.assign(new Error("Not Found"), { status: 404 });

const detail = (mcpAccess: IntegrationMcpAccess = "off", mcpAvailable = true): IntegrationDetail => ({
  integration: {
    id: "i-1",
    name: "Claude agent",
    description: null,
    contactUserId: null,
    contactDisplayName: null,
    rateLimitPerMinute: 600,
    status: "active",
    roles: [],
    keyCount: 1,
    nearestExpiry: null,
    lastUsedAt: null,
    createdAt: "2026-11-19T10:00:00Z",
    createdBy: "me",
    archivedAt: null,
    version: "v-3",
  },
  keys: [],
  effectivePermissions: [],
  mcpAccess,
  mcpAvailable,
});

const renderSection = async (d: IntegrationDetail, archived = false) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute("/workspace/api-access/i-1", () => <IntegrationMcp detail={d} archived={archived} />, queryClient);
  await screen.findByRole("heading", { name: "MCP" });
};

// A spy of each test's own: the setup restores every mock after a test.
const notify = { show: vi.spyOn(notifications, "show") };

beforeEach(() => {
  api.setIntegrationMcpAccess.mockReset();
  api.listIntegrationConnections.mockReset();
  api.listIntegrationConnections.mockRejectedValue(switchedOff());
  notify.show = vi.spyOn(notifications, "show");
});

describe("an integration's MCP section", () => {
  it("is off by default, shows the endpoint and no snippet", async () => {
    await renderSection(detail());
    expect(screen.getByRole("radio", { name: /^Off/ })).toBeChecked();
    expect(screen.getByText(`${window.location.origin}/api/mcp`)).toBeInTheDocument();
    expect(screen.queryByText(/claude mcp add/)).not.toBeInTheDocument();
  });

  it("saves a mode under If-Match on the version read", async () => {
    api.setIntegrationMcpAccess.mockResolvedValue(detail("read"));
    await renderSection(detail());
    fireEvent.click(screen.getByRole("radio", { name: /^Read only/ }));
    await waitFor(() => expect(api.setIntegrationMcpAccess).toHaveBeenCalledWith("i-1", "v-3", "read"));
  });

  it("shows how to connect once it is on, the key read from the client's own store", async () => {
    await renderSection(detail("read"));
    expect(screen.getByRole("radio", { name: /^Read only/ })).toBeChecked();
    const command = screen.getByText(/claude mcp add --transport http/);
    expect(command.textContent).toBe(
      `claude mcp add --transport http claude-agent ${window.location.origin}/api/mcp --header "Authorization: Bearer $VANTIGO_API_KEY"`,
    );
    expect(screen.getByText(/"mcpServers"/).textContent).toContain(`"url": "${window.location.origin}/api/mcp"`);
    expect(screen.getByText(/"servers"/).textContent).toContain('"type": "http"');
    expect(screen.getByText(/Never put a key in a file you commit/).textContent).toContain("export VANTIGO_API_KEY=…");
    expect(screen.getByText(/a key that is not read-only can still write over the REST API/)).toBeInTheDocument();
    expect(screen.getByText(/text written to mislead it/)).toBeInTheDocument();
    expect(screen.queryByText(/write access/)).not.toBeInTheDocument();
  });

  it("says how to connect by signing in beside the key, while OAuth is on", async () => {
    api.listIntegrationConnections.mockResolvedValue([]);
    await renderSection(detail("read"));
    const signIn = within(await screen.findByTestId("mcp-sign-in"));
    expect(screen.getByText("Connect with a key")).toBeInTheDocument();
    expect(signIn.getByText("Connect by signing in")).toBeInTheDocument();
    expect(signIn.getByText(/add a custom connector with the MCP endpoint above/)).toBeInTheDocument();
    expect(signIn.getByText(/needs a public https address/)).toBeInTheDocument();
    // The test page is http: Claude and ChatGPT could not reach it.
    expect(signIn.getByText(/is not https/)).toBeInTheDocument();
    expect(
      signIn.getByText(`claude mcp add --transport http claude-agent ${window.location.origin}/api/mcp`),
    ).toBeInTheDocument();
    expect(signIn.getByText(/type \/mcp in Claude Code and choose Authenticate/)).toBeInTheDocument();
    expect(signIn.getByText(/Registered clients/)).toBeInTheDocument();
    expect(signIn.queryByText(/VANTIGO_API_KEY/)).not.toBeInTheDocument();
  });

  it("says nothing of signing in while OAuth is switched off, or MCP is off", async () => {
    await renderSection(detail("read"));
    await waitFor(() => expect(api.listIntegrationConnections).toHaveBeenCalled());
    expect(screen.queryByTestId("mcp-sign-in")).not.toBeInTheDocument();
    expect(screen.queryByText("Connect with a key")).not.toBeInTheDocument();
  });

  it("says nothing of signing in for an integration whose MCP is off", async () => {
    api.listIntegrationConnections.mockResolvedValue([]);
    await renderSection(detail("off"));
    await waitFor(() => expect(api.listIntegrationConnections).toHaveBeenCalled());
    expect(screen.queryByTestId("mcp-sign-in")).not.toBeInTheDocument();
  });

  it("warns of writes only under read and write, and says what writing allows", async () => {
    await renderSection(detail("read_write"));
    expect(screen.getByText(/A key with write access lets it change data/)).toBeInTheDocument();
    expect(screen.getByText(/also call operations that change data/)).toBeInTheDocument();
    expect(screen.queryByText(/not available through MCP yet/)).not.toBeInTheDocument();
  });

  it("tells of a change made meanwhile and reads the integration again", async () => {
    api.setIntegrationMcpAccess.mockRejectedValue(
      Object.assign(new Error("The integration has been changed since it was read."), { code: "version_mismatch" }),
    );
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    await renderRoute(
      "/workspace/api-access/i-1",
      () => <IntegrationMcp detail={detail()} archived={false} />,
      queryClient,
    );
    fireEvent.click(await screen.findByRole("radio", { name: /^Read only/ }));
    await waitFor(() =>
      expect(notify.show).toHaveBeenCalledWith(
        expect.objectContaining({ color: "orange", message: expect.stringMatching(/Someone changed the integration/) }),
      ),
    );
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["identity", "integrations", "i-1"] }));
  });

  it("shows a plain save error", async () => {
    api.setIntegrationMcpAccess.mockRejectedValue(new Error("The server is unavailable."));
    await renderSection(detail());
    fireEvent.click(screen.getByRole("radio", { name: /^Read only/ }));
    await waitFor(() =>
      expect(notify.show).toHaveBeenCalledWith(
        expect.objectContaining({ color: "red", message: "The server is unavailable." }),
      ),
    );
  });

  it("says when the installation has switched MCP off", async () => {
    await renderSection(detail("read", false));
    expect(screen.getByText(/MCP_ENABLED=0/)).toBeInTheDocument();
    expect(screen.queryByText(/claude mcp add/)).not.toBeInTheDocument();
  });

  it("is not changed on an archived integration", async () => {
    await renderSection(detail("read"), true);
    for (const radio of screen.getAllByRole("radio")) expect(radio).toBeDisabled();
  });
});

describe("the connection snippets", () => {
  it("name the server after the integration and read the key from each client's own store, never holding it", () => {
    const url = "https://erp.example.com/base/api/mcp";
    const { claude, claudeSignIn, json, vscode } = mcpSnippets(url, "Øst Webshop / 2");
    expect(claudeSignIn).toBe(`claude mcp add --transport http st-webshop-2 ${url}`);
    expect(claude).toBe(
      `claude mcp add --transport http st-webshop-2 ${url} --header "Authorization: Bearer $VANTIGO_API_KEY"`,
    );
    expect(JSON.parse(json)).toEqual({
      mcpServers: { "st-webshop-2": { url, headers: { Authorization: "Bearer $" + "{env:VANTIGO_API_KEY}" } } },
    });
    expect(JSON.parse(vscode)).toEqual({
      inputs: [{ type: "promptString", id: "vantigo-key", description: "Vantigo API key", password: true }],
      servers: {
        "st-webshop-2": { type: "http", url, headers: { Authorization: "Bearer $" + "{input:vantigo-key}" } },
      },
    });
    expect(mcpSnippets("https://x/api/mcp", "???").claude).toContain(" vantigo ");
    for (const text of [claude, claudeSignIn, json, vscode]) expect(text).not.toContain("vtg_");
  });
});

describe("the MCP words", () => {
  it("exist in English and Norwegian alike", () => {
    const en = Object.keys(apiAccessCatalog.en).filter((key) => key.startsWith("apiAccess.mcp"));
    const nb = Object.keys(apiAccessCatalog.nb).filter((key) => key.startsWith("apiAccess.mcp"));
    expect(en.length).toBeGreaterThan(10);
    expect(nb.sort()).toEqual(en.sort());
    for (const key of en) expect(apiAccessCatalog.nb[key as keyof typeof apiAccessCatalog.nb]).not.toBe("");
  });
});
