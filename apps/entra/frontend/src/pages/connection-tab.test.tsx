import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { requiredPermissions } from "../api/connection";
import { sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import {
  CLIENT_ID,
  connected,
  credential,
  type EntraServer,
  stubEntraApi,
  TENANT_ID,
  unconfigured,
} from "../test/server";
import { EntraConnectionTab } from "./connection-tab";

/** The Connection tab as the host mounts it, ready once its first card is drawn. */
const openPage = async (server: EntraServer = {}) => {
  const fetchMock = stubEntraApi(server);
  renderWithProviders(<EntraConnectionTab />);
  await screen.findByRole("region", { name: "Register the application" });
  return { fetchMock };
};

const region = (name: string) => screen.getByRole("region", { name });

const retype = async (field: HTMLElement, text: string) => {
  await userEvent.clear(field);
  if (text) await userEvent.type(field, text);
};

describe("EntraConnectionTab — the secrets", () => {
  it("lists the secrets with their state and says the credential_in_use refusal in words", async () => {
    const { fetchMock } = await openPage({ connection: connected() });
    const step = region("Add a client secret");
    const table = within(step).getByRole("table", { name: "Client secrets" });
    expect(within(table).getByText("Vantigo 2026")).toBeInTheDocument();
    expect(within(table).getByText("In use")).toBeInTheDocument();

    await userEvent.click(within(table).getByRole("button", { name: "Revoke Vantigo 2026" }));
    const dialog = await screen.findByRole("dialog", { name: "Revoke Vantigo 2026?" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Revoke" }));

    expect(
      await screen.findByText(
        "This is the only valid secret while the module keeps accounts. Add a new secret first, then revoke this one.",
      ),
    ).toBeInTheDocument();
    expect(sentAll(fetchMock, "DELETE").map((call) => call.url)).toEqual(["/api/v1/entra/connection/credentials/1"]);
  });

  it("revokes an older secret and shows it revoked", async () => {
    const { fetchMock } = await openPage({
      connection: connected({
        credentials: [credential({ id: 2, label: "Vantigo 2027" }), credential({ id: 1, inUse: false })],
      }),
    });
    const table = within(region("Add a client secret")).getByRole("table", { name: "Client secrets" });
    await userEvent.click(within(table).getByRole("button", { name: "Revoke Vantigo 2026" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: "Revoke Vantigo 2026?" })).getByRole("button", {
        name: "Revoke",
      }),
    );
    await waitFor(() => expect(sent(fetchMock, "DELETE").url).toBe("/api/v1/entra/connection/credentials/1"));
    expect(await within(region("Add a client secret")).findByText(/^Revoked /)).toBeInTheDocument();
  });

  it("adds a secret with its label, value and expiry, then clears the value", async () => {
    const { fetchMock } = await openPage({ connection: connected() });
    const form = within(region("Add a client secret")).getByRole("form", { name: "Add a secret" });

    await userEvent.click(within(form).getByRole("button", { name: "Add the secret" }));
    expect(await within(form).findByText("Paste the secret's value.")).toBeInTheDocument();
    expect(within(form).getByText("Enter the expiry the portal shows.")).toBeInTheDocument();
    expect(sentAll(fetchMock, "POST")).toHaveLength(0);

    await userEvent.type(within(form).getByRole("textbox", { name: "Description" }), "Vantigo 2027");
    await userEvent.type(within(form).getByLabelText(/^Value/), "s3cr3t~value");
    await retype(within(form).getByRole("textbox", { name: "Expires" }), "Oct 1, 2028");
    await userEvent.tab();
    await userEvent.click(within(form).getByRole("button", { name: "Add the secret" }));

    await waitFor(() => expect(sentAll(fetchMock, "POST")).toHaveLength(1));
    expect(sent(fetchMock, "POST").body).toEqual({
      kind: "client_secret",
      label: "Vantigo 2027",
      secret: "s3cr3t~value",
      expiresAt: new Date("2028-10-01T00:00:00").toISOString(),
    });
    await waitFor(() => expect(within(form).getByLabelText(/^Value/)).toHaveValue(""));
  });
});

describe("EntraConnectionTab — the connection test", () => {
  it("shows what the test found: the organisation, the domains, each missing permission and the role's sentence", async () => {
    const { fetchMock } = await openPage({
      connection: connected({ status: "unconfigured", lastCheckedAt: undefined }),
      test: {
        status: "connected",
        organisationName: "Contoso AS",
        verifiedDomains: ["contoso.no", "contoso.onmicrosoft.com"],
        permissionsPresent: requiredPermissions.filter((name) => !name.startsWith("Group")),
        permissionsMissing: ["Group.ReadWrite.All"],
        permissionsUnexpected: [],
        roleKnown: false,
      },
    });
    const step = region("Grant the permissions and the role");
    expect(within(step).getByText("User.RevokeSessions.All")).toBeInTheDocument();
    expect(within(step).getByRole("button", { name: "Copy Organization.Read.All" })).toBeInTheDocument();

    await userEvent.click(within(step).getByRole("button", { name: "Test connection" }));

    const result = await within(step).findByRole("status");
    expect(within(result).getByText("Connected to Contoso AS")).toBeInTheDocument();
    expect(within(result).getByText("Verified domains: contoso.no, contoso.onmicrosoft.com")).toBeInTheDocument();
    expect(within(within(result).getByRole("list")).getByText("Group.ReadWrite.All")).toBeInTheDocument();
    expect(within(result).getByText(/cannot be checked from here/)).toBeInTheDocument();
    expect(sent(fetchMock, "POST").url).toBe("/api/v1/entra/connection/test");
  });

  it("says why the test failed in words", async () => {
    await openPage({
      connection: connected({ status: "unconfigured", lastCheckedAt: undefined }),
      test: {
        status: "failing",
        reason: "credential_invalid",
        verifiedDomains: [],
        permissionsPresent: [],
        permissionsMissing: [...requiredPermissions],
        permissionsUnexpected: [],
        roleKnown: false,
      },
    });
    const step = region("Grant the permissions and the role");
    await userEvent.click(within(step).getByRole("button", { name: "Test connection" }));
    const result = await within(step).findByRole("status");
    expect(within(result).getByText("The connection failed")).toBeInTheDocument();
    expect(within(result).getByText(/The tenant refused the client secret/)).toBeInTheDocument();
  });
});

describe("EntraConnectionTab — step 1", () => {
  it("asks before changing the tenant while the module keeps accounts, then sends confirmChange", async () => {
    const other = "33333333-3333-3333-3333-333333333333";
    const { fetchMock } = await openPage({ connection: connected() });
    const form = within(region("Register the application")).getByRole("form", { name: "Register the application" });

    await retype(within(form).getByRole("textbox", { name: /^Directory \(tenant\) ID/ }), "not-a-guid");
    await userEvent.click(within(form).getByRole("button", { name: "Save" }));
    expect(await within(form).findByText(/Enter the ID as the portal shows it/)).toBeInTheDocument();
    expect(sentAll(fetchMock, "PUT")).toHaveLength(0);

    await retype(within(form).getByRole("textbox", { name: /^Directory \(tenant\) ID/ }), other);
    await userEvent.click(within(form).getByRole("button", { name: "Save" }));

    const dialog = await screen.findByRole("dialog", { name: "Change the tenant or the application?" });
    expect(sentAll(fetchMock, "PUT")).toHaveLength(1);
    expect(sent(fetchMock, "PUT").body).toEqual({ tenantId: other, clientId: CLIENT_ID });
    expect(sent(fetchMock, "PUT").headers["if-match"]).toBe("4");

    await userEvent.click(within(dialog).getByRole("button", { name: "Change it" }));
    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(2));
    expect(sentAll(fetchMock, "PUT")[1].body).toEqual({ tenantId: other, clientId: CLIENT_ID, confirmChange: true });
    expect(await screen.findByText("Application saved")).toBeInTheDocument();
  });
});

const steps = ["Register the application", "Add a client secret", "Grant the permissions and the role"];

describe("EntraConnectionTab — the Done marks", () => {
  it("holds steps 1 to 3 and nothing of the other tabs", async () => {
    await openPage();
    for (const name of steps) expect(region(name)).toBeInTheDocument();
    for (const name of ["Status", "Set the account rules", "Sign-in", "Provisioning", "Groups", "Outlook and mail"])
      expect(screen.queryByRole("region", { name })).not.toBeInTheDocument();
  });

  it("marks nothing done before anything is set, and shows no status card", async () => {
    await openPage({ connection: unconfigured() });
    for (const name of steps) expect(within(region(name)).getByText("Not done")).toBeInTheDocument();
  });

  it("marks the three steps done from a connected connection", async () => {
    await openPage({ connection: connected() });
    for (const name of steps) expect(within(region(name)).getByText("Done")).toBeInTheDocument();
  });

  it("marks only what the connection says: registered, the secret revoked, tested with a permission missing", async () => {
    await openPage({
      connection: connected({
        status: "failing",
        statusReason: "credential_invalid",
        credentials: [credential({ inUse: false, revokedAt: "2026-10-18T08:00:00Z" })],
        permissionsMissing: ["Group.ReadWrite.All"],
      }),
    });
    expect(within(region(steps[0])).getByText("Done")).toBeInTheDocument();
    for (const name of steps.slice(1)) expect(within(region(name)).getByText("Not done")).toBeInTheDocument();
  });
});

describe("EntraConnectionTab — for an entra:manage holder who is not an Owner", () => {
  it("shows steps 1 to 3 without their forms", async () => {
    await openPage({ connection: connected(), owner: false });

    expect(screen.queryByRole("textbox", { name: /^Directory \(tenant\) ID/ })).not.toBeInTheDocument();
    expect(
      within(region("Register the application")).getByText(`Directory (tenant) ID: ${TENANT_ID}`),
    ).toBeInTheDocument();
    expect(screen.queryByRole("form", { name: "Add a secret" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Revoke/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Test connection" })).not.toBeInTheDocument();
    for (const name of steps) expect(within(region(name)).getByText(/An Owner does this step/)).toBeInTheDocument();
  });
});

describe("EntraConnectionTab — the permissions", () => {
  // The calendar writes on each person's own connection (Outlook connection design M2): the eight alone,
  // whether or not the Outlook connection is on.
  it("asks for the eight application permissions and never an application Calendars.ReadWrite", async () => {
    await openPage({ connection: connected() });
    const step = region("Grant the permissions and the role");
    expect(within(step).getByText("Add these eight application permissions, one by one.")).toBeInTheDocument();
    const list = within(step).getByRole("list", { name: "The permissions to add" });
    expect(within(list).queryByText("Calendars.ReadWrite")).not.toBeInTheDocument();
    expect(within(list).getAllByRole("listitem")).toHaveLength(requiredPermissions.length);
    expect(screen.queryByText("(for calendar sync)")).not.toBeInTheDocument();
  });
});
