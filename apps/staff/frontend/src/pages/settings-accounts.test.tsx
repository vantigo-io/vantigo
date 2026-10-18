import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { meta, settings } from "../test/fixtures";
import { region, retype, sent } from "../test/helpers";
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
    expect(within(card).getByText(/used by the Entra module/i)).toBeInTheDocument();
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

  it("says the mode and that an Owner has named the employee roles", async () => {
    stubStaffApi();
    renderRoute("/staff/settings");
    const card = await region("Accounts");
    expect(within(card).getByText(/Local accounts — Staff creates the user/)).toBeInTheDocument();
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
});
