import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { IntegrationDetail, IntegrationMcpAccess } from "../../api/integrations";
import { apiAccessCatalog } from "../../catalogs/api-access";
import { renderRoute } from "../../test/render-route";
import { IntegrationMcp, mcpSnippets } from "./-api-access-mcp";
import "../../i18n";

const api = vi.hoisted(() => ({ setIntegrationMcpAccess: vi.fn() }));

vi.mock("../../api/integrations", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/integrations")>()),
  setIntegrationMcpAccess: api.setIntegrationMcpAccess,
}));

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

beforeEach(() => api.setIntegrationMcpAccess.mockReset());

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

  it("shows how to connect once it is on, with a placeholder for the key", async () => {
    await renderSection(detail("read"));
    expect(screen.getByRole("radio", { name: /^Read only/ })).toBeChecked();
    const command = screen.getByText(/claude mcp add --transport http/);
    expect(command.textContent).toBe(
      `claude mcp add --transport http claude-agent ${window.location.origin}/api/mcp --header "Authorization: Bearer vtg_…"`,
    );
    expect(screen.getByText(/"mcpServers"/).textContent).toContain(`"url": "${window.location.origin}/api/mcp"`);
    expect(screen.getByText(/A key with write access lets it change data/)).toBeInTheDocument();
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
  it("name the server after the integration and carry url and headers", () => {
    const { claude, json } = mcpSnippets("https://erp.example.com/base/api/mcp", "Øst Webshop / 2");
    expect(claude).toBe(
      'claude mcp add --transport http st-webshop-2 https://erp.example.com/base/api/mcp --header "Authorization: Bearer vtg_…"',
    );
    expect(JSON.parse(json)).toEqual({
      mcpServers: {
        "st-webshop-2": { url: "https://erp.example.com/base/api/mcp", headers: { Authorization: "Bearer vtg_…" } },
      },
    });
    expect(mcpSnippets("https://x/api/mcp", "???").claude).toContain(" vantigo ");
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
