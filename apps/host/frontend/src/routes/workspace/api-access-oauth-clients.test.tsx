import { QueryClient } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { RegisteredClient } from "../../api/oauth";
import { apiAccessCatalog } from "../../catalogs/api-access";
import { renderRoute } from "../../test/render-route";
import { ClientHosts, RegisteredClients } from "./-api-access-oauth-clients";
import "../../i18n";

const api = vi.hoisted(() => ({
  listRegisteredClients: vi.fn(),
  registerClient: vi.fn(),
  updateRegisteredClient: vi.fn(),
  archiveRegisteredClient: vi.fn(),
  getOAuthClientHosts: vi.fn(),
  setOAuthClientHosts: vi.fn(),
}));

vi.mock("../../api/oauth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/oauth")>()),
  ...api,
}));

const switchedOff = () => Object.assign(new Error("Not Found"), { status: 404 });

const client = (overrides: Partial<RegisteredClient> = {}): RegisteredClient => ({
  id: "rc-1",
  clientId: "vtc_0123456789abcdefghijABCD",
  name: "Cursor",
  redirectUris: ["http://127.0.0.1/callback"],
  createdBy: "u-1",
  createdByName: "Kari Owner",
  createdAt: "2026-11-21T10:00:00Z",
  updatedAt: "2026-11-21T10:00:00Z",
  version: "v-1",
  liveConnections: 2,
  ...overrides,
});

const render = async (section: "clients" | "hosts") => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await renderRoute(
    "/workspace/api-access",
    () => (section === "clients" ? <RegisteredClients /> : <ClientHosts />),
    queryClient,
  );
};

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
});

describe("the registered clients", () => {
  it("lists each client with its client ID, redirect URIs, who registered it and its connections", async () => {
    api.listRegisteredClients.mockResolvedValue([
      client(),
      client({ id: "rc-2", name: "VS Code", createdByName: null, liveConnections: 0 }),
    ]);
    await render("clients");
    const table = within(await screen.findByTestId("registered-clients"));
    expect(table.getAllByText("vtc_0123456789abcdefghijABCD", { selector: "code" })).toHaveLength(2);
    expect(table.getAllByText("http://127.0.0.1/callback")).toHaveLength(2);
    expect(table.getByText("Kari Owner")).toBeInTheDocument();
    expect(table.getByText("Deleted person")).toBeInTheDocument();
  });

  it("registers a client from its name and its redirect URIs, one a line, and shows its client ID", async () => {
    api.listRegisteredClients.mockResolvedValue([]);
    api.registerClient.mockResolvedValue(client());
    await render("clients");
    expect(await screen.findByText("No client is registered.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Register a client" }));
    fireEvent.change(await screen.findByLabelText(/^Name/), { target: { value: " Cursor " } });
    fireEvent.change(screen.getByLabelText(/^Redirect URIs/), {
      target: { value: "http://127.0.0.1/callback\n\n https://vscode.dev/redirect \n" },
    });
    fireEvent.click(screen.getAllByRole("button", { name: "Register a client" }).at(-1) as HTMLElement);
    await waitFor(() =>
      expect(api.registerClient).toHaveBeenCalledWith({
        name: "Cursor",
        redirectUris: ["http://127.0.0.1/callback", "https://vscode.dev/redirect"],
      }),
    );
    expect(await screen.findByText(/leave the client secret empty/)).toBeInTheDocument();
    expect(screen.getAllByText("vtc_0123456789abcdefghijABCD").length).toBeGreaterThan(0);
  });

  it("shows the field a refusal names", async () => {
    api.listRegisteredClients.mockResolvedValue([]);
    api.registerClient.mockRejectedValue(
      Object.assign(new Error("The request is invalid."), {
        code: "invalid_request",
        fields: { redirectUris: ["1 to 20 redirect URIs, each https with a host"] },
      }),
    );
    await render("clients");
    fireEvent.click(await screen.findByRole("button", { name: "Register a client" }));
    fireEvent.change(await screen.findByLabelText(/^Name/), { target: { value: "App" } });
    fireEvent.change(screen.getByLabelText(/^Redirect URIs/), { target: { value: "myapp://cb" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Register a client" }).at(-1) as HTMLElement);
    expect(await screen.findByText(/1 to 20 redirect URIs, each https with a host/)).toBeInTheDocument();
  });

  it("edits a client under If-Match on the version read", async () => {
    api.listRegisteredClients.mockResolvedValue([client()]);
    api.updateRegisteredClient.mockResolvedValue(client({ name: "Cursor (laptop)", version: "v-2" }));
    await render("clients");
    fireEvent.click(await screen.findByRole("button", { name: "Edit Cursor" }));
    fireEvent.change(await screen.findByLabelText(/^Name/), { target: { value: "Cursor (laptop)" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(api.updateRegisteredClient).toHaveBeenCalledWith("rc-1", "v-1", {
        name: "Cursor (laptop)",
        redirectUris: ["http://127.0.0.1/callback"],
      }),
    );
  });

  it("archives a client behind a confirmation that says its connections end", async () => {
    api.listRegisteredClients.mockResolvedValue([client()]);
    api.archiveRegisteredClient.mockResolvedValue(undefined);
    await render("clients");
    fireEvent.click(await screen.findByRole("button", { name: "Archive Cursor" }));
    expect(await screen.findByText(/connected now: 2/)).toBeInTheDocument();
    expect(api.archiveRegisteredClient).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Archive" }));
    await waitFor(() => expect(api.archiveRegisteredClient).toHaveBeenCalledWith("rc-1", "v-1"));
  });

  it("is not shown while OAuth is switched off", async () => {
    api.listRegisteredClients.mockRejectedValue(switchedOff());
    await render("clients");
    await waitFor(() => expect(api.listRegisteredClients).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByText("Registered clients")).not.toBeInTheDocument());
  });
});

describe("the client hosts", () => {
  it("says an empty list admits any host, and saves the hosts entered", async () => {
    api.getOAuthClientHosts.mockResolvedValue([]);
    api.setOAuthClientHosts.mockResolvedValue(["claude.ai"]);
    await render("hosts");
    expect(await screen.findByText("Empty: an app from any host may connect.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/^Allowed hosts/), { target: { value: " claude.ai \n\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.setOAuthClientHosts).toHaveBeenCalledWith(["claude.ai"]));
    expect(await screen.findByText(/Only apps from these hosts/)).toBeInTheDocument();
  });

  it("shows the refusal's words for a host it does not take", async () => {
    api.getOAuthClientHosts.mockResolvedValue([]);
    api.setOAuthClientHosts.mockRejectedValue(
      Object.assign(new Error("The request is invalid."), { fields: { clientHosts: ["At most 50 host names"] } }),
    );
    await render("hosts");
    fireEvent.change(await screen.findByLabelText(/^Allowed hosts/), { target: { value: "*.example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("At most 50 host names")).toBeInTheDocument();
  });

  it("is not shown while OAuth is switched off", async () => {
    api.getOAuthClientHosts.mockRejectedValue(switchedOff());
    await render("hosts");
    await waitFor(() => expect(api.getOAuthClientHosts).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByText("Client hosts")).not.toBeInTheDocument());
  });
});

describe("the words of delivery C", () => {
  it("exist in English and Norwegian alike", () => {
    const keys = Object.keys(apiAccessCatalog.en).filter((key) =>
      /^apiAccess\.(client|registered|register|editClient|archiveClient|redirectUris|noClients|couldNotLoadClient|couldNotArchiveClient|mcpSignIn|connectionKind|inactive_client)/.test(
        key,
      ),
    );
    expect(keys.length).toBeGreaterThan(25);
    for (const key of keys) expect(apiAccessCatalog.nb[key as keyof typeof apiAccessCatalog.nb]).toBeTruthy();
  });
});
