import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import {
  CLIENT_ID,
  connected,
  type EntraServer,
  entraGroup,
  stubEntraApi,
  TENANT_ID,
  unconfigured,
} from "../test/server";
import type { SignInDefaults } from "./accounts-tab";
import { EntraAccountsTab } from "./accounts-tab";

/** The Accounts tab as the host mounts it, ready once its first card is drawn. */
const openPage = async (server: EntraServer = {}) => {
  const fetchMock = stubEntraApi(server);
  const signIn = vi.fn((defaults: SignInDefaults) => <p>sign-in form for {defaults.clientId}</p>);
  const provisioning = vi.fn(() => <p>provisioning form</p>);
  renderWithProviders(<EntraAccountsTab signIn={signIn} provisioning={provisioning} />);
  await screen.findByRole("region", { name: "Set the account rules" });
  return { fetchMock, signIn, provisioning };
};

const region = (name: string) => screen.getByRole("region", { name });

const retype = async (field: HTMLElement, text: string) => {
  await userEvent.clear(field);
  if (text) await userEvent.type(field, text);
};

describe("EntraAccountsTab — the account rules", () => {
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
    // The switches that used to end this form are the Outlook and mail card's: left out, so kept.
    expect(put.body).not.toHaveProperty("outlookConnect");
    expect(put.body).not.toHaveProperty("mailChannels");
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

describe("EntraAccountsTab — the Groups table", () => {
  it("lists the module's groups: name, type, team, state and the last members sync", async () => {
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
    // The Groups card follows the account rules, sign-in and provisioning.
    const sections = screen.getAllByRole("region").map((section) => section.getAttribute("aria-labelledby"));
    expect(sections.at(-1)).toBe(region("Groups").getAttribute("aria-labelledby"));
  });

  it("asks to confirm a found group, naming it and what confirming allows, then confirms it; a retired one is worded", async () => {
    const { fetchMock } = await openPage({
      connection: connected({ groups: { count: 1 } }),
      groups: [
        entraGroup({
          objectId: "55555555-5555-4555-8555-555555555555",
          groupId: "1004",
          key: "sales",
          displayName: "Sales (found)",
          mailNickname: "sales",
          createdByUs: false,
          lastMembersAt: undefined,
        }),
        entraGroup({ displayName: "Engineering (archived)", state: "retired" }),
      ],
    });
    const table = await within(region("Groups")).findByRole("table", { name: "Groups" });
    const found = within(table).getByRole("row", { name: /Sales \(found\)/ });
    expect(within(found).getByText("Adopted — confirm")).toBeInTheDocument();
    const retired = within(table).getByRole("row", { name: /Engineering \(archived\)/ });
    expect(within(retired).getByText("Emptied and kept")).toBeInTheDocument();
    expect(within(retired).queryByRole("button")).not.toBeInTheDocument();

    await userEvent.click(within(found).getByRole("button", { name: "Confirm Sales (found)" }));
    const dialog = await screen.findByRole("dialog", { name: "Confirm the adoption of Sales (found)?" });
    expect(within(dialog).getByText(/never deletes a group it did not create/)).toBeInTheDocument();
    expect(
      within(dialog).getByText(
        /Its first write makes the group's owners exactly Staff's owners and the application — the owners it has now are removed — and writes Staff's name, description and visibility to it/,
      ),
    ).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Confirm" }));

    await waitFor(() =>
      expect(sentAll(fetchMock, "POST").map((call) => call.url)).toEqual([
        "/api/v1/entra/groups/55555555-5555-4555-8555-555555555555/confirm",
      ]),
    );
    expect(await screen.findByText("Adoption confirmed")).toBeInTheDocument();
    await waitFor(() => expect(within(table).queryByText("Adopted — confirm")).not.toBeInTheDocument());
    expect(within(table).getByText("Found in the tenant and adopted")).toBeInTheDocument();
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

describe("EntraAccountsTab — steps 4 to 6", () => {
  it("holds steps 4 to 6 and the Groups table, and nothing of the other tabs", async () => {
    await openPage({ connection: connected() });
    for (const name of ["Set the account rules", "Sign-in", "Provisioning"]) expect(region(name)).toBeInTheDocument();
    expect(await screen.findByRole("region", { name: "Groups" })).toBeInTheDocument();
    for (const name of ["Status", "Register the application", "Add a client secret", "Outlook and mail"])
      expect(screen.queryByRole("region", { name })).not.toBeInTheDocument();
  });

  it("marks step 4 from the UPN domain and leaves 5 and 6 optional", async () => {
    await openPage({ connection: connected() });
    expect(within(region("Set the account rules")).getByText("Done")).toBeInTheDocument();
    for (const name of ["Sign-in", "Provisioning"])
      expect(within(region(name)).getByText("Optional")).toBeInTheDocument();
  });

  it("marks step 4 not done without a domain", async () => {
    await openPage({ connection: connected({ settings: { ...connected().settings, upnDomain: undefined } }) });
    expect(within(region("Set the account rules")).getByText("Not done")).toBeInTheDocument();
  });

  it("asks for step 1 first when nothing is registered, and draws no Groups table", async () => {
    await openPage({ connection: unconfigured() });
    expect(within(region("Set the account rules")).getByText("Not done")).toBeInTheDocument();
    expect(within(region("Sign-in")).getByText("Register the application in step 1 first.")).toBeInTheDocument();
  });

  it("hands step 5 the tenant's authority and links step 6 to Staff's settings", async () => {
    const { signIn, provisioning } = await openPage({ connection: connected() });
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

  it("leaves the account rules to an entra:manage holder who is not an Owner, and identity's forms out", async () => {
    const { signIn, provisioning } = await openPage({ connection: connected(), owner: false });
    expect(
      within(region("Set the account rules")).getByRole("button", { name: "Save the account rules" }),
    ).toBeInTheDocument();
    for (const name of ["Sign-in", "Provisioning"])
      expect(within(region(name)).getByText(/An Owner does this step/)).toBeInTheDocument();
    expect(signIn).not.toHaveBeenCalled();
    expect(provisioning).not.toHaveBeenCalled();
  });
});
