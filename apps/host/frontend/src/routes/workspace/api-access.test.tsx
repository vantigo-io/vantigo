import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { PermissionDescriptor } from "../../api/authorization";
import type { ApiKey, IntegrationDetail } from "../../api/integrations";
import { renderRoute } from "../../test/render-route";
import { IntegrationPage } from "./-api-access-integration";
import "../../i18n";

const api = vi.hoisted(() => ({
  getIntegration: vi.fn(),
  createApiKey: vi.fn(),
  rollApiKey: vi.fn(),
  updateIntegration: vi.fn(),
}));

vi.mock("../../api/integrations", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/integrations")>()),
  getIntegration: api.getIntegration,
  createApiKey: api.createApiKey,
  rollApiKey: api.rollApiKey,
  revokeApiKey: vi.fn(),
  updateIntegration: api.updateIntegration,
  setIntegrationRoles: vi.fn(),
  disableIntegration: vi.fn(),
  enableIntegration: vi.fn(),
  archiveIntegration: vi.fn(),
}));

const permission = (key: string, displayName: string, sensitive = false): PermissionDescriptor => ({
  key,
  module: key.split(":")[0],
  category: "General",
  displayName,
  description: displayName,
  sensitive,
  delegable: true,
});

vi.mock("../../api/authorization", async (importOriginal) => ({
  isCustomRole: (await importOriginal<typeof import("../../api/authorization")>()).isCustomRole,
  listPermissionCatalog: () =>
    Promise.resolve([
      permission("alpha:view", "Alpha view"),
      permission("beta:access", "Beta access"),
      permission("alpha:secret", "Alpha secret", true),
    ]),
  listAuthorizationRoles: () =>
    Promise.resolve([
      {
        id: "r-webshop",
        name: "webshop",
        normalizedName: "WEBSHOP",
        displayName: "Webshop",
        description: "Webshop",
        isSystem: false,
        isBuiltIn: false,
        stewardUserId: null,
        version: "1",
        permissions: ["alpha:view", "alpha:secret"],
      },
    ]),
  listAuthorizationUsers: () => Promise.resolve([]),
}));

const key = (overrides: Partial<ApiKey> = {}): ApiKey => ({
  id: "k-1",
  name: "Production",
  display: "vtg_4f2a…9XyZ",
  status: "active",
  readOnly: false,
  allowedNetworks: [],
  permissions: null,
  expiresAt: "2027-02-15T10:00:00Z",
  createdAt: "2026-11-17T10:00:00Z",
  createdBy: "me",
  revokedAt: null,
  rolledFrom: null,
  lastUsedAt: null,
  lastUsedIp: null,
  ...overrides,
});

const detail = (keys: ApiKey[] = []): IntegrationDetail => ({
  integration: {
    id: "i-1",
    name: "Webshop sync",
    description: null,
    contactUserId: null,
    contactDisplayName: null,
    rateLimitPerMinute: 600,
    status: "active",
    roles: [{ id: "r-webshop", name: "webshop", displayName: "Webshop" }],
    keyCount: keys.length,
    nearestExpiry: null,
    lastUsedAt: null,
    createdAt: "2026-11-17T10:00:00Z",
    createdBy: "me",
    archivedAt: null,
    version: "3",
  },
  keys,
  effectivePermissions: [
    { key: "alpha:view", apiNever: false },
    { key: "alpha:secret", apiNever: true },
  ],
  mcpAccess: "off",
  mcpAvailable: true,
});

const renderPage = async () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute("/workspace/api-access/i-1", () => <IntegrationPage integrationId="i-1" />, queryClient);
  await screen.findByRole("heading", { name: /Webshop sync/ });
  return queryClient;
};

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
});

describe("an integration's page", () => {
  it("marks a permission no API key ever holds, and only that one", async () => {
    api.getIntegration.mockResolvedValue(detail());
    await renderPage();
    const table = await screen.findByTestId("effective-permissions");
    const never = await within(table).findByText("Alpha secret");
    const neverRow = never.closest("tr") as HTMLElement;
    expect(within(neverRow).getByText("Never available to API keys")).toBeInTheDocument();
    const viewRow = within(table).getByText("Alpha view").closest("tr") as HTMLElement;
    expect(within(viewRow).queryByText("Never available to API keys")).not.toBeInTheDocument();
  });

  it("marks a narrowing's permission the roles do not grant, and strikes through one Vantigo no longer has", async () => {
    api.getIntegration.mockResolvedValue(
      detail([
        key({
          permissions: [
            { key: "alpha:view", grantedByRoles: true, inCatalog: true, apiNever: false },
            { key: "beta:access", grantedByRoles: false, inCatalog: true, apiNever: false },
            { key: "legacy:gone", grantedByRoles: false, inCatalog: false, apiNever: false },
            { key: "alpha:secret", grantedByRoles: false, inCatalog: true, apiNever: true },
          ],
        }),
      ]),
    );
    const container = document.body;
    await renderPage();
    const granted = await waitFor(() => {
      const one = container.querySelector('[data-narrowing="alpha:view"]') as HTMLElement;
      expect(one).not.toBeNull();
      return one;
    });
    expect(within(granted).queryByText("Not granted by the roles")).not.toBeInTheDocument();
    const ungranted = container.querySelector('[data-narrowing="beta:access"]') as HTMLElement;
    expect(within(ungranted).getByText("Not granted by the roles")).toBeInTheDocument();
    const removed = container.querySelector('[data-narrowing="legacy:gone"]') as HTMLElement;
    expect(within(removed).getByText("legacy:gone")).toHaveAttribute("title", "No longer in Vantigo; kept and ignored");
    expect(within(removed).queryByText("Not granted by the roles")).not.toBeInTheDocument();
    const never = container.querySelector('[data-narrowing="alpha:secret"]') as HTMLElement;
    expect(within(never).getByText("Never available to API keys")).toBeInTheDocument();
    expect(within(never).queryByText("Not granted by the roles")).not.toBeInTheDocument();
  });

  it("shows a new key's secret once, with a curl example, and forgets it when the dialog closes", async () => {
    api.getIntegration.mockResolvedValue(detail());
    const secret = "vtg_0123456789abcdefghijABCDEFGHIJ0123456789abc000000";
    api.createApiKey.mockResolvedValue({ key: key({ name: "Production" }), secret });
    await renderPage();

    fireEvent.click(await screen.findByRole("button", { name: "New API key" }));
    fireEvent.change(await screen.findByRole("textbox", { name: /^Name/ }), { target: { value: "Production" } });
    fireEvent.click(screen.getByRole("button", { name: "Create key" }));

    expect(await screen.findByText(secret)).toBeInTheDocument();
    expect(api.createApiKey).toHaveBeenCalledWith("i-1", {
      name: "Production",
      expiresInDays: 90,
      readOnly: false,
      allowedNetworks: [],
    });
    expect(screen.getByText("This is the only time the key is shown.", { exact: false })).toBeInTheDocument();
    expect(
      screen.getByText(`curl -H "Authorization: Bearer ${secret}" ${window.location.origin}/api/v1/customers`),
    ).toBeInTheDocument();

    // Only "I have copied the key" closes it: no close button, and not Escape.
    const dialog = screen.getByRole("dialog");
    expect(dialog.querySelector(".mantine-Modal-close")).toBeNull();
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(screen.getByText(secret)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "I have copied the key" }));
    await waitFor(() => expect(screen.queryByText(secret)).not.toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "New API key" }));
    expect(await screen.findByRole("button", { name: "Create key" })).toBeInTheDocument();
    expect(screen.queryByText(secret)).not.toBeInTheDocument();
    expect(screen.queryByText(secret, { exact: false })).not.toBeInTheDocument();
  });

  it("narrows a key to what is ticked, never offering what no key may hold", async () => {
    api.getIntegration.mockResolvedValue(detail());
    api.createApiKey.mockResolvedValue({ key: key(), secret: "vtg_x" });
    await renderPage();

    fireEvent.click(await screen.findByRole("button", { name: "New API key" }));
    fireEvent.change(await screen.findByRole("textbox", { name: /^Name/ }), { target: { value: "Narrow" } });
    fireEvent.click(screen.getByRole("switch", { name: /Narrow the key/ }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByRole("checkbox", { name: /Alpha secret/ })).toBeDisabled();
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /Alpha view/ }));
    fireEvent.click(within(dialog).getByRole("switch", { name: /Show the permissions the roles do not grant/ }));
    const showUngranted = within(dialog).getByRole("switch", { name: /Show the permissions the roles do not grant/ });
    // A tick the dialog stops showing is dropped.
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /Beta access/ }));
    fireEvent.click(showUngranted);
    fireEvent.click(showUngranted);
    const ungranted = within(dialog).getByRole("checkbox", { name: /Beta access/ });
    expect(ungranted).not.toBeChecked();
    expect(ungranted.closest(".mantine-Checkbox-root")).toHaveTextContent("Not granted by the roles");
    fireEvent.click(ungranted);
    fireEvent.click(within(dialog).getByRole("switch", { name: /Read only/ }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Create key" }));

    await waitFor(() =>
      expect(api.createApiKey).toHaveBeenCalledWith("i-1", {
        name: "Narrow",
        expiresInDays: 90,
        permissions: ["alpha:view", "beta:access"],
        readOnly: true,
        allowedNetworks: [],
      }),
    );
  });

  it("rolls a key, showing the new secret once, and marks the rolled key without Roll", async () => {
    const old = key({ id: "k-1", name: "Production" });
    api.getIntegration.mockResolvedValue(detail([old]));
    const secret = "vtg_rolledsecret0123456789abcdefghijABCDEFGHIJ0123456";
    const made = key({ id: "k-2", name: "Production", rolledFrom: "k-1" });
    api.rollApiKey.mockResolvedValue({ key: made, secret });
    await renderPage();

    fireEvent.click(await screen.findByRole("button", { name: "Roll" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/If the key has leaked, revoke it instead/)).toBeInTheDocument();
    api.getIntegration.mockResolvedValue(detail([old, made]));
    fireEvent.click(within(dialog).getByRole("button", { name: "Roll" }));

    expect(await screen.findByText(secret)).toBeInTheDocument();
    expect(api.rollApiKey).toHaveBeenCalledWith("i-1", "k-1", { overlapHours: 24, expiresInDays: 90 });
    fireEvent.click(screen.getByRole("button", { name: "I have copied the key" }));
    await waitFor(() => expect(screen.queryByText(secret)).not.toBeInTheDocument());

    const rolledRow = (await waitFor(() => {
      const row = document.querySelector('[data-key="k-1"]');
      expect(row).not.toBeNull();
      expect(within(row as HTMLElement).getByText("Rolled")).toBeInTheDocument();
      return row;
    })) as HTMLElement;
    expect(within(rolledRow).queryByRole("button", { name: "Roll" })).not.toBeInTheDocument();
    expect(within(rolledRow).getByRole("button", { name: "Revoke" })).toBeInTheDocument();
    const newRow = document.querySelector('[data-key="k-2"]') as HTMLElement;
    expect(within(newRow).getByRole("button", { name: "Roll" })).toBeInTheDocument();
  });

  it("answers a stale version by closing the form and reloading the integration", async () => {
    api.getIntegration.mockResolvedValue(detail());
    api.updateIntegration.mockRejectedValue(
      Object.assign(new Error("Precondition failed"), { code: "version_mismatch" }),
    );
    await renderPage();

    fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Edit details" }));
    const form = await screen.findByRole("dialog");
    fireEvent.click(within(form).getByRole("button", { name: "Save" }));

    expect(await screen.findByText(/Someone changed the integration since you opened it/)).toBeInTheDocument();
    expect(api.updateIntegration).toHaveBeenCalledWith("i-1", "3", { name: "Webshop sync", rateLimitPerMinute: 600 });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => expect(api.getIntegration.mock.calls.length).toBeGreaterThan(1));
  });
});
