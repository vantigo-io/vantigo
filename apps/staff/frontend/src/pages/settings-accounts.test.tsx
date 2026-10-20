import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { entraProvider, meta, settings } from "../test/fixtures";
import { choose, region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

const slow = { timeout: 5000 };

describe("Settings — accounts", () => {
  it("saves the two account settings with every other setting sent back, under the revision read", async () => {
    const fetchMock = stubStaffApi({
      settings: settings({ createBeforeDays: 14, erasePregnancyHealthTypes: true, injuryInsurer: "If" }),
    });
    renderRoute("/staff/settings");

    const card = await region("Accounts");
    expect(
      within(card).getByText(/deletes the person's account in an identity provider's directory/),
    ).toBeInTheDocument();
    await retype(within(card).getByRole("textbox", { name: /Prepare the account/ }), "21");
    await retype(within(card).getByRole("textbox", { name: /Delete an external account/ }), "365");
    await userEvent.click(within(card).getByRole("button", { name: "Save the account settings" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/staff\/settings$/)).toHaveLength(1), slow);
    const [put] = sent(fetchMock, "PUT", /\/staff\/settings$/);
    expect(put.ifMatch).toBe("3");
    expect(put.body).toEqual({
      employeeNumberPrefix: "E-",
      nextEmployeeNumber: 8,
      pensionProvider: "Storebrand",
      injuryInsurer: "If",
      createBeforeDays: 21,
      deleteExternalAfterDays: 365,
      erasePregnancyHealthTypes: true,
      eraseSickCaseDates: false,
    });
  });

  it("sends an emptied deletion as null, for never", async () => {
    const fetchMock = stubStaffApi({ settings: settings({ deleteExternalAfterDays: 90 }) });
    renderRoute("/staff/settings");
    const card = await region("Accounts");
    await retype(within(card).getByRole("textbox", { name: /Delete an external account/ }), "");
    await userEvent.click(within(card).getByRole("button", { name: "Save the account settings" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/staff\/settings$/)).toHaveLength(1), slow);
    expect(sent(fetchMock, "PUT", /\/staff\/settings$/)[0].body).toMatchObject({
      createBeforeDays: 14,
      deleteExternalAfterDays: null,
    });
  });

  it("refuses days out of bounds before sending", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");
    const card = await region("Accounts");
    await retype(within(card).getByRole("textbox", { name: /Prepare the account/ }), "91");
    await userEvent.click(within(card).getByRole("button", { name: "Save the account settings" }));
    expect(await within(card).findByText("0 to 90 days")).toBeInTheDocument();
    expect(sent(fetchMock, "PUT", /\/staff\/settings$/)).toHaveLength(0);
  });

  it("says no provider is offered and that an Owner has named the employee roles", async () => {
    stubStaffApi();
    renderRoute("/staff/settings");
    const card = await region("Accounts");
    expect(within(card).getByText(/No identity provider is offered/)).toBeInTheDocument();
    expect(within(card).getByText(/An Owner has named the roles Staff may give/)).toBeInTheDocument();
  });

  it("warns when no Owner has named the employee roles, with the way to the roles page", async () => {
    stubStaffApi({ meta: meta("manage", { provisioningRolesSet: false }) });
    renderRoute("/staff/settings");
    const card = await region("Accounts");
    expect(within(card).getByText(/No Owner has named the roles Staff may give/)).toBeInTheDocument();
    expect(within(card).getByRole("link", { name: "Open Roles & access" })).toHaveAttribute("href", "/workspace/roles");
  });

  it("runs the accounts now and says the counts, warning that it may take minutes", async () => {
    const fetchMock = stubStaffApi({ accountsRun: { attempted: 5, done: 3, failed: 2, remaining: 4 } });
    renderRoute("/staff/settings");
    const card = await region("Accounts");
    expect(within(card).getByText(/may take minutes/)).toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Run accounts now" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/staff\/accounts\/run$/)).toHaveLength(1), slow);
    expect(
      await within(card).findByText(
        "Accounts worked: 5. Done: 3. Failed, retried by the worker: 2. Left for the worker's next cycle: 4.",
        {},
        slow,
      ),
    ).toBeInTheDocument();
  });

  it("says accounts_unavailable in words on Run accounts now", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "POST" && path.endsWith("/accounts/run")
          ? staffProblem(503, { code: "accounts_unavailable", title: "Accounts are unavailable" })
          : undefined,
    });
    renderRoute("/staff/settings");
    const card = await region("Accounts");
    await userEvent.click(within(card).getByRole("button", { name: "Run accounts now" }));
    expect(await screen.findByText(/Identity could not be reached/, {}, slow)).toBeInTheDocument();
  });

  it("says each provider's status, with its reason in words", async () => {
    stubStaffApi({
      meta: meta("manage", {
        providers: [
          entraProvider({ status: "failing", reason: "secret_expired" }),
          entraProvider({ key: "other", name: "Other directory", status: "unconfigured" }),
        ],
      }),
    });
    renderRoute("/staff/settings");
    const card = await region("Accounts");
    expect(
      within(card).getByText(/^Microsoft Entra ID: failing — .* Its client secret has expired\.$/),
    ).toBeInTheDocument();
    expect(within(card).getByText("Other directory: not connected yet.")).toBeInTheDocument();
  });

  it("offers entra as the default kind only when /meta lists its provider", async () => {
    stubStaffApi();
    renderRoute("/staff/settings");
    const card = await region("Accounts");
    await userEvent.click(within(card).getByRole("combobox", { name: /Default account kind/ }));
    expect(await screen.findByRole("option", { name: "External identity provider" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "Microsoft Entra ID" })).not.toBeInTheDocument();
  });

  it("saves a changed default kind with the account settings", async () => {
    const fetchMock = stubStaffApi({ meta: meta("manage", { providers: [entraProvider()] }) });
    renderRoute("/staff/settings");
    const card = await region("Accounts");
    expect(within(card).getByText("Microsoft Entra ID: connected.")).toBeInTheDocument();
    await choose(card, /Default account kind/, "Microsoft Entra ID");
    await userEvent.click(within(card).getByRole("button", { name: "Save the account settings" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/staff\/settings$/)).toHaveLength(1), slow);
    expect(sent(fetchMock, "PUT", /\/staff\/settings$/)[0].body).toMatchObject({ defaultAccountKind: "entra" });
    expect(fetchMock.state.settings.defaultAccountKind).toBe("entra");
  });

  it("says provider_missing on the default kind in words", async () => {
    stubStaffApi({
      meta: meta("manage", { providers: [entraProvider()] }),
      write: (method, path) =>
        method === "PUT" && path.endsWith("/staff/settings")
          ? staffProblem(400, {
              code: "provider_missing",
              title: "Invalid Staff settings",
              errors: { defaultAccountKind: ["The server's English"] },
            })
          : undefined,
    });
    renderRoute("/staff/settings");
    const card = await region("Accounts");
    await choose(card, /Default account kind/, "Microsoft Entra ID");
    await userEvent.click(within(card).getByRole("button", { name: "Save the account settings" }));
    expect(
      await within(card).findByText(/No enabled module offers this identity provider/, {}, slow),
    ).toBeInTheDocument();
    expect(screen.queryByText("The server's English")).not.toBeInTheDocument();
  });
});

describe("Settings — the directory profile", () => {
  it("switches a field and saves the switches with every other setting sent back, under the revision read", async () => {
    const fetchMock = stubStaffApi({
      settings: settings({ createBeforeDays: 21, deleteExternalAfterDays: 365, eraseSickCaseDates: true }),
    });
    renderRoute("/staff/settings");
    const card = await region("Directory profile");
    expect(within(card).getByRole("switch", { name: "Job title" })).toBeChecked();
    expect(within(card).getByRole("switch", { name: "Work address" })).not.toBeChecked();
    // The fields always written are not switches here.
    expect(within(card).queryByRole("switch", { name: "Given name" })).not.toBeInTheDocument();
    await userEvent.click(within(card).getByRole("switch", { name: "Work address" }));
    await userEvent.click(within(card).getByRole("switch", { name: "Manager" }));
    await userEvent.click(within(card).getByRole("button", { name: "Save the directory profile" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/staff\/settings$/)).toHaveLength(1), slow);
    const [put] = sent(fetchMock, "PUT", /\/staff\/settings$/);
    expect(put.ifMatch).toBe("3");
    expect(put.body).toEqual({
      employeeNumberPrefix: "E-",
      nextEmployeeNumber: 8,
      pensionProvider: "Storebrand",
      createBeforeDays: 21,
      deleteExternalAfterDays: 365,
      erasePregnancyHealthTypes: false,
      eraseSickCaseDates: true,
      directoryFields: {
        employeeHireDate: true,
        employeeLeaveDateTime: true,
        employeeType: true,
        jobTitle: true,
        department: true,
        companyName: true,
        employeeOrgData: true,
        officeLocation: true,
        workAddress: true,
        phones: false,
        preferredLanguage: true,
        manager: false,
      },
    });
    expect(fetchMock.state.settings.directoryFields.workAddress).toBe(true);
    expect(fetchMock.state.settings.directoryFields.manager).toBe(false);
  });

  it("says a revision conflict in words and reloads", async () => {
    const fetchMock = stubStaffApi({
      write: (method, path) =>
        method === "PUT" && path.endsWith("/staff/settings")
          ? staffProblem(409, { code: "revision_conflict", title: "Revision conflict", detail: "revision 4" })
          : undefined,
    });
    renderRoute("/staff/settings");
    const card = await region("Directory profile");
    await userEvent.click(within(card).getByRole("switch", { name: "Phones" }));
    await userEvent.click(within(card).getByRole("button", { name: "Save the directory profile" }));
    expect(await screen.findByText("Somebody else saved this; it has been reloaded", {}, slow)).toBeInTheDocument();
    await waitFor(() => expect(sent(fetchMock, "GET", /\/staff\/settings$/).length).toBeGreaterThan(1));
  });
});
