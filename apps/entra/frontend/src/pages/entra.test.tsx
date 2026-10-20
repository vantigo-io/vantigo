import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { requiredPermissions } from "../api/connection";
import { sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import {
  CLIENT_ID,
  connected,
  credential,
  type EntraServer,
  entraGroup,
  stubEntraApi,
  TENANT_ID,
  unconfigured,
} from "../test/server";
import { EntraPage, type SignInDefaults } from "./entra";

const steps = [
  "Register the application",
  "Add a client secret",
  "Grant the permissions and the role",
  "Set the account rules",
  "Sign-in",
  "Provisioning",
];

/** The page as the host mounts it, with stand-ins for identity's two forms. */
const openPage = async (server: EntraServer = {}) => {
  const fetchMock = stubEntraApi(server);
  const signIn = vi.fn((defaults: SignInDefaults) => <p>sign-in form for {defaults.clientId}</p>);
  const provisioning = vi.fn(() => <p>provisioning form</p>);
  renderWithProviders(<EntraPage signIn={signIn} provisioning={provisioning} />);
  await screen.findByRole("region", { name: steps[0] });
  return { fetchMock, signIn, provisioning };
};

const region = (name: string) => screen.getByRole("region", { name });

const retype = async (field: HTMLElement, text: string) => {
  await userEvent.clear(field);
  if (text) await userEvent.type(field, text);
};

describe("EntraPage — the wizard's Done marks", () => {
  it("names the module", async () => {
    await openPage();
    expect(screen.getByRole("heading", { name: "Microsoft Entra ID" })).toBeInTheDocument();
  });

  it("marks nothing done before anything is set, and shows no status card", async () => {
    await openPage({ connection: unconfigured() });
    for (const name of steps.slice(0, 4)) expect(within(region(name)).getByText("Not done")).toBeInTheDocument();
    for (const name of steps.slice(4)) expect(within(region(name)).getByText("Optional")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Status" })).not.toBeInTheDocument();
    expect(within(region("Sign-in")).getByText("Register the application in step 1 first.")).toBeInTheDocument();
  });

  it("marks the four steps done from a connected connection and hands step 5 the tenant's authority", async () => {
    const { signIn, provisioning } = await openPage({ connection: connected() });
    for (const name of steps.slice(0, 4)) expect(within(region(name)).getByText("Done")).toBeInTheDocument();
    expect(region("Status")).toBeInTheDocument();
    expect(signIn).toHaveBeenCalledWith({
      provider: "entra",
      authority: `https://login.microsoftonline.com/${TENANT_ID}/v2.0`,
      clientId: CLIENT_ID,
    });
    expect(within(region("Sign-in")).getByText(`sign-in form for ${CLIENT_ID}`)).toBeInTheDocument();
    expect(provisioning).toHaveBeenCalled();
    expect(within(region("Provisioning")).getByRole("link", { name: "Open Staff's settings" })).toHaveAttribute(
      "href",
      "/staff/settings",
    );
  });

  it("marks only what the connection says: registered, the secret revoked, tested with a permission missing", async () => {
    await openPage({
      connection: connected({
        status: "failing",
        statusReason: "credential_invalid",
        credentials: [credential({ inUse: false, revokedAt: "2026-10-18T08:00:00Z" })],
        permissionsMissing: ["Group.ReadWrite.All"],
        settings: { ...connected().settings, upnDomain: undefined },
      }),
    });
    expect(within(region(steps[0])).getByText("Done")).toBeInTheDocument();
    for (const name of steps.slice(1, 4)) expect(within(region(name)).getByText("Not done")).toBeInTheDocument();
  });
});

describe("EntraPage — the Status card", () => {
  it("names each missing permission, the reason in words, the counts and the events", async () => {
    await openPage({
      connection: connected({
        statusReason: "role_missing",
        permissionsPresent: requiredPermissions.filter((name) => name !== "UserAuthMethod-TAP.ReadWrite.All"),
        permissionsMissing: ["UserAuthMethod-TAP.ReadWrite.All"],
      }),
      events: [
        {
          id: 7,
          at: "2026-10-19T11:00:00Z",
          actor: "system:staff-accounts",
          action: "entra.user.activated",
          upn: "ola.nordmann@contoso.no",
          codes: ["licence_unavailable:ENTERPRISEPACK"],
        },
        {
          id: 8,
          at: "2026-10-19T10:00:00Z",
          actor: "system:staff-accounts",
          action: "entra.user.deactivated",
          upn: "kari@contoso.no",
          codes: ["refused:adoption_unconfirmed"],
        },
      ],
    });
    const status = region("Status");
    expect(within(status).getByText("Connected")).toBeInTheDocument();
    expect(within(status).getByText(/most likely lacks the User Administrator role/)).toBeInTheDocument();
    expect(within(status).getByText("Contoso AS")).toBeInTheDocument();
    const missing = within(status).getByRole("list", { name: "Missing permissions" });
    expect(
      within(missing)
        .getAllByRole("listitem")
        .map((item) => item.textContent),
    ).toEqual(["UserAuthMethod-TAP.ReadWrite.All"]);
    expect(within(status).getByText("3 in all: 2 enabled, 1 disabled, 0 deleted")).toBeInTheDocument();
    const events = await within(status).findByRole("list", { name: "Recent events" });
    expect(
      within(events).getByText("An account was enabled · ola.nordmann@contoso.no · by Staff's accounts run"),
    ).toBeInTheDocument();
    expect(within(events).getByText("licence ENTERPRISEPACK has no seat free")).toBeInTheDocument();
    expect(
      within(events).getByText(
        "refused: the account was found in the tenant, not created, and its adoption is not yet confirmed",
      ),
    ).toBeInTheDocument();
  });
});

describe("EntraPage — an adopted account", () => {
  const adopted = {
    objectId: "33333333-3333-3333-3333-333333333333",
    employeeNumber: "E-4",
    userPrincipalName: "kari@contoso.no",
    state: "enabled" as const,
    createdByUs: false,
    adoptionConfirmed: false,
    hasPass: false,
    reported: ["adoption_unconfirmed"],
    createdAt: "2026-10-19T08:00:00Z",
  };
  const created = {
    ...adopted,
    objectId: "44444444-4444-4444-4444-444444444444",
    employeeNumber: "E-5",
    userPrincipalName: "ola@contoso.no",
    createdByUs: true,
    reported: [],
  };

  it("asks to confirm a found account, naming it and what confirming allows, then confirms it", async () => {
    const { fetchMock } = await openPage({ connection: connected(), accounts: [adopted, created] });
    const table = await within(region("Status")).findByRole("table", { name: "The module's accounts" });
    expect(within(table).getByText("Adopted — confirm")).toBeInTheDocument();
    expect(
      within(table).getByText(
        "found in the tenant, not created: nothing is written to it until its adoption is confirmed",
      ),
    ).toBeInTheDocument();
    expect(within(table).queryByRole("button", { name: "Confirm ola@contoso.no" })).not.toBeInTheDocument();

    await userEvent.click(within(table).getByRole("button", { name: "Confirm kari@contoso.no" }));
    const dialog = await screen.findByRole("dialog", { name: "Confirm the adoption of kari@contoso.no?" });
    expect(
      within(dialog).getByText(/enable it and issue a Temporary Access Pass, and disable and delete it/),
    ).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Confirm" }));

    await waitFor(() =>
      expect(sentAll(fetchMock, "POST").map((call) => call.url)).toEqual([
        "/api/v1/entra/accounts/33333333-3333-3333-3333-333333333333/confirm",
      ]),
    );
    expect(await screen.findByText("Adoption confirmed")).toBeInTheDocument();
    await waitFor(() => expect(within(table).queryByText("Adopted — confirm")).not.toBeInTheDocument());
  });

  it("says a refused confirmation in words", async () => {
    await openPage({
      connection: connected(),
      accounts: [adopted],
      handle: (method, path) =>
        method === "POST" && path.endsWith("/confirm")
          ? new Response(JSON.stringify({ title: "Adoption confirmed", status: 409, code: "adoption_confirmed" }), {
              status: 409,
              headers: { "Content-Type": "application/problem+json" },
            })
          : undefined,
    });
    const table = await within(region("Status")).findByRole("table", { name: "The module's accounts" });
    await userEvent.click(within(table).getByRole("button", { name: "Confirm kari@contoso.no" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: "Confirm the adoption of kari@contoso.no?" })).getByRole(
        "button",
        { name: "Confirm" },
      ),
    );
    expect(
      await screen.findByText("The account is Vantigo's already: its adoption was confirmed, or Vantigo created it."),
    ).toBeInTheDocument();
  });
});

describe("EntraPage — the secrets", () => {
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

describe("EntraPage — the connection test", () => {
  it("shows what the test found: the organisation, the domains, each missing permission and the role's sentence", async () => {
    const { fetchMock } = await openPage({
      connection: connected({ status: "unconfigured", lastCheckedAt: undefined }),
      test: {
        status: "connected",
        organisationName: "Contoso AS",
        verifiedDomains: ["contoso.no", "contoso.onmicrosoft.com"],
        permissionsPresent: requiredPermissions.filter((name) => !name.startsWith("Group")),
        permissionsMissing: ["Group.ReadWrite.All"],
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

describe("EntraPage — the account rules", () => {
  it("says what is wrong in words before sending, then saves under If-Match", async () => {
    const { fetchMock } = await openPage({ connection: connected({ verifiedDomains: [] }) });
    const form = within(region("Set the account rules")).getByRole("form", { name: "Account rules" });

    await retype(within(form).getByRole("textbox", { name: /^Domain for user principal names/ }), "Not a domain");
    await retype(within(form).getByRole("textbox", { name: /^Default usage location/ }), "N1");
    await retype(within(form).getByRole("textbox", { name: /^Temporary Access Pass lifetime/ }), "5");
    await userEvent.click(within(form).getByRole("button", { name: "Save the account rules" }));

    expect(await within(form).findByText("Enter a domain name, such as example.com.")).toBeInTheDocument();
    expect(within(form).getByText("Enter a two-letter country code, such as NO.")).toBeInTheDocument();
    expect(within(form).getByText("Enter a whole number of minutes from 10 to 43200.")).toBeInTheDocument();
    expect(sentAll(fetchMock, "PUT")).toHaveLength(0);

    await retype(within(form).getByRole("textbox", { name: /^Domain for user principal names/ }), "contoso.no");
    await retype(within(form).getByRole("textbox", { name: /^Default usage location/ }), "se");
    await retype(within(form).getByRole("textbox", { name: /^Temporary Access Pass lifetime/ }), "60");
    await userEvent.click(within(form).getByRole("switch", { name: "The pass can be used once only" }));
    await userEvent.click(within(form).getByRole("button", { name: "Save the account rules" }));

    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    const put = sent(fetchMock, "PUT");
    expect(put.url).toBe("/api/v1/entra/connection/settings");
    expect(put.headers["if-match"]).toBe("4");
    expect(put.body).toMatchObject({
      upnDomain: "contoso.no",
      defaultUsageLocation: "SE",
      passLifetimeMinutes: 60,
      passUsableOnce: true,
    });
    expect(await screen.findByText("Account rules saved")).toBeInTheDocument();
  });

  it("offers the verified domains as the choices and places the server's field error", async () => {
    const { fetchMock } = await openPage({
      connection: connected(),
      handle: (method, path) =>
        method === "PUT" && path === "/api/v1/entra/connection/settings"
          ? new Response(
              JSON.stringify({ title: "Validation failed", status: 400, errors: { upnDomain: ["Not verified."] } }),
              { status: 400, headers: { "Content-Type": "application/problem+json" } },
            )
          : undefined,
    });
    const form = within(region("Set the account rules")).getByRole("form", { name: "Account rules" });
    const domain = within(form).getByRole("combobox", { name: /^Domain for user principal names/ });
    expect(domain).toHaveValue("contoso.no");
    await userEvent.click(domain);
    expect(await screen.findByRole("option", { name: "contoso.onmicrosoft.com" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("option", { name: "contoso.onmicrosoft.com" }));
    expect(domain).toHaveValue("contoso.onmicrosoft.com");
    await userEvent.click(within(form).getByRole("button", { name: "Save the account rules" }));
    expect(await within(form).findByText("Not verified.")).toBeInTheDocument();
    expect(sentAll(fetchMock, "PUT")).toHaveLength(1);
    expect(sent(fetchMock, "PUT").body).toMatchObject({ upnDomain: "contoso.onmicrosoft.com" });
  });

  it("reloads when the connection moved on (412)", async () => {
    await openPage({
      connection: connected(),
      handle: (method, path) =>
        method === "PUT" && path === "/api/v1/entra/connection/settings"
          ? new Response(JSON.stringify({ title: "Stale", status: 412, code: "version_mismatch" }), {
              status: 412,
              headers: { "Content-Type": "application/problem+json" },
            })
          : undefined,
    });
    const form = within(region("Set the account rules")).getByRole("form", { name: "Account rules" });
    await userEvent.click(within(form).getByRole("button", { name: "Save the account rules" }));
    expect(await screen.findByText("Somebody else saved the connection; it has been reloaded")).toBeInTheDocument();
  });
});

describe("EntraPage — step 1", () => {
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

describe("EntraPage — for an entra:manage holder who is not an Owner", () => {
  it("shows steps 1 to 3 without their forms, and leaves identity's forms out", async () => {
    const { signIn, provisioning } = await openPage({ connection: connected(), owner: false });

    expect(screen.queryByRole("textbox", { name: /^Directory \(tenant\) ID/ })).not.toBeInTheDocument();
    expect(
      within(region("Register the application")).getByText(`Directory (tenant) ID: ${TENANT_ID}`),
    ).toBeInTheDocument();
    expect(screen.queryByRole("form", { name: "Add a secret" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Revoke/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Test connection" })).not.toBeInTheDocument();
    for (const name of ["Register the application", "Add a client secret", "Grant the permissions and the role"])
      expect(within(region(name)).getByText(/An Owner does this step/)).toBeInTheDocument();

    // The account rules are entra:manage's.
    expect(
      within(region("Set the account rules")).getByRole("button", { name: "Save the account rules" }),
    ).toBeInTheDocument();
    expect(signIn).not.toHaveBeenCalled();
    expect(provisioning).not.toHaveBeenCalled();
  });
});

describe("EntraPage — the Groups table", () => {
  it("lists the module's groups under the Status card: name, type, team, state and the last members sync", async () => {
    await openPage({
      connection: connected({ groups: { count: 1 } }),
      groups: [
        entraGroup(),
        entraGroup({
          objectId: "44444444-4444-4444-8444-444444444444",
          groupId: "1003",
          key: "old-project",
          displayName: "Old project (archived)",
          mailNickname: "old-project",
          type: "microsoft365",
          team: true,
          state: "deleted",
          createdByUs: false,
          lastMembersAt: undefined,
        }),
      ],
    });
    expect(within(region("Status")).getByText("1 kept")).toBeInTheDocument();
    const table = await within(region("Groups")).findByRole("table", { name: "Groups" });
    const made = within(table).getByRole("row", { name: /^Engineering/ });
    expect(within(made).getByText("Security")).toBeInTheDocument();
    expect(within(made).getByText("No")).toBeInTheDocument();
    expect(within(made).getByText("Present")).toBeInTheDocument();
    const old = within(table).getByRole("row", { name: /Old project \(archived\)/ });
    expect(within(old).getByText("Microsoft 365")).toBeInTheDocument();
    expect(within(old).getByText("Yes")).toBeInTheDocument();
    expect(within(old).getByText("Deleted")).toBeInTheDocument();
    expect(within(old).getByText("Found in the tenant and adopted")).toBeInTheDocument();
    expect(within(old).getByText("Never")).toBeInTheDocument();
    // The Groups card follows the Status card.
    const sections = screen.getAllByRole("region").map((section) => section.getAttribute("aria-labelledby"));
    const status = sections.indexOf(region("Status").getAttribute("aria-labelledby"));
    expect(sections[status + 1]).toBe(region("Groups").getAttribute("aria-labelledby"));
  });

  it("says there are none yet", async () => {
    await openPage({ connection: connected() });
    expect(await within(region("Groups")).findByText(/No groups yet/)).toBeInTheDocument();
  });

  it("is not drawn for an unconfigured connection", async () => {
    await openPage({ connection: unconfigured() });
    expect(screen.queryByRole("region", { name: "Groups" })).not.toBeInTheDocument();
  });
});
