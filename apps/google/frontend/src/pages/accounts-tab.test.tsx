import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it, vi } from "vitest";
import { jsonResponse, problemResponse, sent, sentAll } from "../test/api";
import { renderWithProviders } from "../test/render";
import {
  connected,
  type GoogleServer,
  googleAccount,
  googleGroup,
  sku,
  stubGoogleApi,
  unconfigured,
} from "../test/server";
import { GoogleAccountsTab, type SignInDefaults } from "./accounts-tab";

afterEach(() => setLanguagePreference("auto"));

/** The Accounts tab as the host mounts it, ready once its first card is drawn. */
const openPage = async (server: GoogleServer = {}, firstCard = "Account rules") => {
  const fetchMock = stubGoogleApi(server);
  const user = userEvent.setup();
  const signIn = vi.fn((defaults: SignInDefaults) => <p>sign-in form for {defaults.clientId}</p>);
  const provisioning = vi.fn(() => <p>provisioning form</p>);
  renderWithProviders(<GoogleAccountsTab signIn={signIn} provisioning={provisioning} />);
  await screen.findByRole("region", { name: firstCard });
  return { fetchMock, user, signIn, provisioning };
};

const region = (name: string) => screen.getByRole("region", { name });
const rulesForm = () => within(region("Account rules")).getByRole("form", { name: "Account rules" });

describe("GoogleAccountsTab — the account rules", () => {
  it("shows the connection's rules and saves them under If-Match", async () => {
    const { fetchMock, user } = await openPage({
      connection: connected({ addressPattern: "g.family", orgUnitPath: "/Staff", passLifetimeDays: 10 }),
    });
    const form = rulesForm();
    expect(within(form).getByRole("combobox", { name: /^Address domain/ })).toHaveValue(
      "The primary domain (contoso.no)",
    );
    expect(within(form).getByRole("combobox", { name: /^Address pattern/ })).toHaveValue(
      "First initial and last name: k.nordmann",
    );
    expect(within(form).getByRole("textbox", { name: /^Organisational unit/ })).toHaveValue("/Staff");
    expect(within(form).getByRole("textbox", { name: /^The first password's lifetime/ })).toHaveValue("10");
    expect(within(form).getByRole("combobox", { name: /^Who hands over the first password/ })).toHaveValue("HR");
    expect(within(form).getByText(/HR shows the password once, on the person's Account tab in Staff/)).toBeVisible();
    expect(within(form).getByRole("switch", { name: /^Remove the licences/ })).toBeChecked();
    expect(within(form).getByRole("combobox", { name: /^Take them out of groups/ })).toHaveValue(
      "The groups Vantigo made",
    );

    await user.click(within(form).getByRole("combobox", { name: /^Address domain/ }));
    await user.click(await screen.findByRole("option", { name: "contoso.com" }));
    const unit = within(form).getByRole("textbox", { name: /^Organisational unit/ });
    await user.clear(unit);
    await user.type(unit, "/Staff/Oslo");
    await user.click(within(form).getByRole("switch", { name: /^Sign them out of every session/ }));
    await user.click(within(form).getByRole("button", { name: "Save the account rules" }));

    expect(await screen.findByText("Account rules saved")).toBeInTheDocument();
    expect(sent(fetchMock, "PUT")).toMatchObject({
      url: "/api/v1/google/connection/settings",
      headers: { "if-match": "5" },
      body: {
        addressDomain: "contoso.com",
        addressPattern: "g.family",
        orgUnitPath: "/Staff/Oslo",
        passLifetimeDays: 10,
        passDelivery: "hr",
        leaverRemoveLicences: true,
        leaverRemoveGroups: "managed",
        leaverSignOut: false,
      },
    });
    expect(fetchMock.state.connection.version).toBe(6);
  });

  it("offers a free-text domain before the verified domains are read", async () => {
    const { fetchMock, user } = await openPage({ connection: unconfigured() });
    const form = rulesForm();
    const domain = within(form).getByRole("textbox", { name: /^Address domain/ });
    expect(domain).toHaveValue("");
    await user.type(domain, "Contoso.NO");
    await user.click(within(form).getByRole("button", { name: "Save the account rules" }));
    await waitFor(() => expect(sentAll(fetchMock, "PUT")).toHaveLength(1));
    expect(sent(fetchMock, "PUT").body).toMatchObject({ addressDomain: "contoso.no" });
  });

  it("says what is wrong before sending", async () => {
    const { fetchMock, user } = await openPage({ connection: connected() });
    const form = rulesForm();
    const unit = within(form).getByRole("textbox", { name: /^Organisational unit/ });
    await user.clear(unit);
    await user.type(unit, "Staff");
    const days = within(form).getByRole("textbox", { name: /^The first password's lifetime/ });
    await user.clear(days);
    await user.click(within(form).getByRole("button", { name: "Save the account rules" }));
    expect(await within(form).findByText("Write a path that starts with /, such as /Staff.")).toBeInTheDocument();
    expect(within(form).getByText("Enter a whole number of days from 1 to 30.")).toBeInTheDocument();
    expect(sentAll(fetchMock, "PUT")).toHaveLength(0);
  });

  it("places the server's field error beside its field", async () => {
    const { user } = await openPage({
      connection: connected(),
      handle: (method, path) =>
        method === "PUT" && path === "/api/v1/google/connection/settings"
          ? problemResponse(400, "Validation failed", {
              errors: { orgUnitPath: ["There is no such organisational unit."] },
            })
          : undefined,
    });
    const form = rulesForm();
    await user.click(within(form).getByRole("button", { name: "Save the account rules" }));
    expect(await within(form).findByText("There is no such organisational unit.")).toBeInTheDocument();
    expect(within(form).getByRole("textbox", { name: /^Organisational unit/ })).toHaveAttribute("aria-invalid", "true");
  });

  it("reloads when the connection moved on (412)", async () => {
    const { fetchMock, user } = await openPage({ connection: connected() });
    fetchMock.state.connection = { ...fetchMock.state.connection, version: 9 };
    await user.click(within(rulesForm()).getByRole("button", { name: "Save the account rules" }));
    expect(await screen.findByText("Somebody else saved the connection; it has been reloaded")).toBeInTheDocument();
  });
});

describe("GoogleAccountsTab — the licences", () => {
  it("lists the licences kept on the connection and reads them anew", async () => {
    const { fetchMock, user } = await openPage({
      connection: connected({ skus: [sku()] }),
      skus: [
        sku({ assigned: 13 }),
        sku({ skuId: "1010020028", value: "Google-Apps/1010020028", name: "Business Standard", assigned: 2 }),
      ],
    });
    const card = region("Licences");
    expect(within(card).getByText(/These are the choices when a Staff group names the licences/)).toBeVisible();
    const table = within(card).getByRole("table", { name: "Licences" });
    const row = within(table).getByRole("row", { name: /Google Workspace Business Starter/ });
    expect(within(row).getByText("Google-Apps/1010020027")).toBeInTheDocument();
    expect(within(row).getByText("12")).toBeInTheDocument();

    await user.click(within(card).getByRole("button", { name: "Read the licences" }));
    expect(await screen.findByText("Licences read")).toBeInTheDocument();
    expect(await within(card).findByText("Business Standard")).toBeInTheDocument();
    expect(sentAll(fetchMock, "GET").map((call) => call.url)).toContain("/api/v1/google/connection/skus");
    expect(within(card).getByText("13")).toBeInTheDocument();
  });

  it("says in words why Google could not be asked (503) or was asked too often (429)", async () => {
    let answer = problemResponse(503, "Service Unavailable", { code: "credential_invalid" });
    const { user } = await openPage({
      connection: connected(),
      handle: (method, path) => (method === "GET" && path === "/api/v1/google/connection/skus" ? answer : undefined),
    });
    const card = region("Licences");
    expect(within(card).getByText("No licences have been read yet.")).toBeInTheDocument();
    await user.click(within(card).getByRole("button", { name: "Read the licences" }));
    const alert = await within(card).findByRole("alert");
    expect(alert).toHaveTextContent("The licences could not be read");
    expect(alert).toHaveTextContent(
      "Google refused the key, or the scope needed is not delegated. Test the connection and check the delegation.",
    );

    answer = jsonResponse(429, { error: { code: "rate_limited", message: "Too many requests" } });
    await user.click(within(card).getByRole("button", { name: "Read the licences" }));
    expect(await within(card).findByText("That was asked too often. Wait a minute and try again.")).toBeInTheDocument();
  });

  it("asks for a tested connection before reading", async () => {
    await openPage({ connection: unconfigured() });
    const card = region("Licences");
    expect(within(card).getByRole("button", { name: "Read the licences" })).toBeDisabled();
    expect(within(card).getByText("Test the connection first: the licences are read through it.")).toBeVisible();
  });
});

describe("GoogleAccountsTab — the accounts", () => {
  it("lists the accounts with their state, licences, reported codes in words and the waiting password", async () => {
    await openPage({
      connection: connected({ skus: [sku()] }),
      accounts: [
        googleAccount({ passWaiting: true }),
        googleAccount({
          userId: "100000000000000000002",
          employeeNumber: "1002",
          primaryEmail: "ola.hansen@contoso.no",
          state: "disabled",
          licences: [],
          lastError: "privileged",
          reported: [
            "privileged",
            "licence_unavailable:1010020028",
            "leaver_step_failed:licences",
            "manager_not_found",
            "something_new",
          ],
        }),
      ],
    });
    const table = await within(region("Accounts")).findByRole("table", { name: "Accounts" });
    const kari = within(table).getByRole("row", { name: /kari\.nordmann@contoso\.no/ });
    expect(within(kari).getByText("1001")).toBeInTheDocument();
    expect(within(kari).getByText("Active")).toBeInTheDocument();
    expect(within(kari).getByText("Google Workspace Business Starter")).toBeInTheDocument();
    expect(within(kari).getByText("Waiting to be shown")).toBeInTheDocument();
    expect(within(kari).queryByRole("button")).not.toBeInTheDocument();

    const ola = within(table).getByRole("row", { name: /ola\.hansen@contoso\.no/ });
    expect(within(ola).getByText("Suspended")).toBeInTheDocument();
    expect(within(ola).getByText("None")).toBeInTheDocument();
    expect(
      within(ola).getByText("Last write refused: the user is an admin, and Vantigo does not write to admins"),
    ).toBeInTheDocument();
    expect(within(ola).getByText("The user is an admin: Vantigo leaves it alone")).toBeInTheDocument();
    expect(within(ola).getByText("Licence 1010020028 could not be assigned: none is free")).toBeInTheDocument();
    expect(within(ola).getByText("A leaver's step failed: licences")).toBeInTheDocument();
    expect(
      within(ola).getByText("The manager has no Google account Vantigo knows, so none is set"),
    ).toBeInTheDocument();
    expect(within(ola).getByText("something_new")).toBeInTheDocument();
  });

  it("asks before confirming a found account, then confirms it and refreshes the table", async () => {
    const { fetchMock, user } = await openPage({
      connection: connected(),
      accounts: [
        googleAccount({
          userId: "100000000000000000003",
          primaryEmail: "per.berg@contoso.no",
          createdByUs: false,
          state: "disabled",
          reported: ["adoption_unconfirmed"],
        }),
      ],
    });
    const table = await within(region("Accounts")).findByRole("table", { name: "Accounts" });
    expect(within(table).getByText("Found — confirm")).toBeInTheDocument();
    expect(within(table).getByText(/nothing is written to it until its adoption is confirmed/)).toBeInTheDocument();

    await user.click(within(table).getByRole("button", { name: "Confirm per.berg@contoso.no" }));
    const dialog = await screen.findByRole("dialog", { name: "Confirm the adoption of per.berg@contoso.no?" });
    expect(within(dialog).getByText(/Confirming lets Vantigo manage it as its own/)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Confirm" }));

    await waitFor(() =>
      expect(sentAll(fetchMock, "POST").map((call) => call.url)).toEqual([
        "/api/v1/google/accounts/100000000000000000003/confirm",
      ]),
    );
    expect(await screen.findByText("Adoption confirmed")).toBeInTheDocument();
    expect(await within(table).findByText("Found and adopted")).toBeInTheDocument();
    expect(within(table).queryByText("Found — confirm")).not.toBeInTheDocument();
    expect(within(table).queryByText(/nothing is written to it until/)).not.toBeInTheDocument();
  });

  it("says there are none yet", async () => {
    await openPage({ connection: connected() });
    expect(await within(region("Accounts")).findByText(/^No accounts yet\./)).toBeInTheDocument();
  });
});

describe("GoogleAccountsTab — the groups", () => {
  it("lists the groups, made or adopted, and confirms a found one", async () => {
    const { fetchMock, user } = await openPage({
      connection: connected(),
      groups: [
        googleGroup(),
        googleGroup({ groupId: "03found", staffGroupId: "43", email: "sales@contoso.no", createdByUs: false }),
      ],
    });
    const table = await within(region("Groups")).findByRole("table", { name: "Groups" });
    const made = within(table).getByRole("row", { name: /engineering@contoso\.no/ });
    expect(within(made).getByText("42")).toBeInTheDocument();
    expect(within(made).getByText("Created by Vantigo")).toBeInTheDocument();
    const found = within(table).getByRole("row", { name: /sales@contoso\.no/ });
    expect(within(found).getByText("Found — confirm")).toBeInTheDocument();

    await user.click(within(found).getByRole("button", { name: "Confirm sales@contoso.no" }));
    const dialog = await screen.findByRole("dialog", { name: "Confirm the adoption of sales@contoso.no?" });
    expect(within(dialog).getByText(/Vantigo never deletes a group it did not create/)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Confirm" }));

    await waitFor(() =>
      expect(sentAll(fetchMock, "POST").map((call) => call.url)).toEqual(["/api/v1/google/groups/03found/confirm"]),
    );
    expect(await within(table).findByText("Found and adopted")).toBeInTheDocument();
    expect(within(table).queryByRole("button", { name: /^Confirm/ })).not.toBeInTheDocument();
  });

  it("says there are none yet", async () => {
    await openPage({ connection: connected() });
    expect(await within(region("Groups")).findByText(/^No groups yet\./)).toBeInTheDocument();
  });
});

describe("GoogleAccountsTab — in Norwegian", () => {
  it("says the cards, the form and the tables in Norwegian", async () => {
    setLanguagePreference("nb");
    await openPage(
      {
        connection: connected({ skus: [sku()] }),
        accounts: [googleAccount({ createdByUs: false, reported: ["adoption_unconfirmed"] })],
        groups: [googleGroup()],
      },
      "Kontoregler",
    );
    expect(within(region("Kontoregler")).getByRole("button", { name: "Lagre kontoreglene" })).toBeInTheDocument();
    expect(within(region("Kontoregler")).getByRole("combobox", { name: /^Adressemønster/ })).toHaveValue(
      "Fornavn og etternavn: kari.nordmann",
    );
    expect(region("Lisenser")).toBeInTheDocument();
    const accounts = await within(region("Kontoer")).findByRole("table", { name: "Kontoer" });
    expect(within(accounts).getByText("Aktiv")).toBeInTheDocument();
    expect(within(accounts).getByRole("button", { name: "Bekreft kari.nordmann@contoso.no" })).toBeInTheDocument();
    const groups = await within(region("Grupper")).findByRole("table", { name: "Grupper" });
    expect(within(groups).getByText("Opprettet av Vantigo")).toBeInTheDocument();
  });
});

describe("GoogleAccountsTab — sign-in and provisioning", () => {
  it("hands the host's sign-in form Google, the web client and the verified domains", async () => {
    const { signIn, provisioning } = await openPage({ connection: connected() });
    expect(signIn).toHaveBeenCalledWith({
      provider: "google",
      authority: "https://accounts.google.com",
      clientId: connected().oauthClientId,
      allowedDomains: ["contoso.no", "contoso.com"],
    });
    expect(within(region("Sign-in")).getByText(`sign-in form for ${connected().oauthClientId}`)).toBeVisible();
    expect(within(region("Sign-in")).getByText(/Find matches in the directory…/)).toBeVisible();
    expect(provisioning).toHaveBeenCalled();
    expect(within(region("Provisioning")).getByRole("link", { name: "Open Staff's settings" })).toHaveAttribute(
      "href",
      "/staff/settings",
    );
  });

  it("asks for the web client before the form, when there is none", async () => {
    const { signIn } = await openPage({ connection: connected({ oauthClientId: undefined }) });
    expect(
      within(region("Sign-in")).getByText("Enter the Internal web client on the Connection tab first."),
    ).toBeVisible();
    expect(signIn).not.toHaveBeenCalled();
  });

  it("is the Owner's", async () => {
    const { signIn, provisioning } = await openPage({ connection: connected(), owner: false });
    for (const name of ["Sign-in", "Provisioning"])
      expect(
        within(region(name)).getByText("An Owner does this step. You can see what is set, but not change it."),
      ).toBeVisible();
    expect(signIn).not.toHaveBeenCalled();
    expect(provisioning).not.toHaveBeenCalled();
  });

  it("is hidden until a test has read the organisation", async () => {
    const { signIn } = await openPage({ connection: connected({ customerId: undefined }) });
    expect(screen.queryByRole("region", { name: "Sign-in" })).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Provisioning" })).not.toBeInTheDocument();
    expect(signIn).not.toHaveBeenCalled();
  });

  it("speaks Norwegian", async () => {
    setLanguagePreference("nb");
    await openPage({ connection: connected() }, "Kontoregler");
    expect(screen.getByRole("region", { name: "Innlogging" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Klargjøring" })).toBeInTheDocument();
  });
});
