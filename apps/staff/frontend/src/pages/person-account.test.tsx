import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { components } from "../api-schema";
import { erasedPerson, withErased } from "../test/fixtures";
import { region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

type Account = components["schemas"]["StaffPersonAccount"];
type Link = components["schemas"]["StaffAccountLink"];

/** A round trip — a write, the refetch it causes, the render — on a loaded runner. */
const slow = { timeout: 5000 };

const ADA_USER = { id: "8d5c6a3e-0000-4000-8000-000000000101", displayName: "Ada at Fjord", active: true };

/** Ada's account as HR turned it on: the work address on file, the day from her employment. */
const account = (overrides: Partial<Account> = {}): Account => ({
  provisionAccount: true,
  accountEmailKind: "work",
  address: { kind: "work", present: true },
  provisionFromEffective: "2025-01-01",
  mode: "local",
  revision: 4,
  ...overrides,
});

const managed = (overrides: Partial<Link> = {}): Link => ({
  state: "active",
  managed: true,
  attempts: 0,
  user: ADA_USER,
  lastSyncedAt: "2026-10-11T03:00:00Z",
  ...overrides,
});

const openTab = async (url = "/staff/people/101?tab=account") => {
  renderRoute(url);
  return region("Account flag");
};

const typeDate = async (field: HTMLElement, text: string) => {
  await retype(field, text);
  await userEvent.tab();
};

describe("Person page — the Account tab", () => {
  it("opens from ?tab=account, the dashboard's link, for HR", async () => {
    stubStaffApi({ accounts: { 101: account({ link: managed() }) } });
    await openTab();
    expect(screen.getByRole("tab", { name: "Account", selected: true })).toBeInTheDocument();
  });

  it("says the local mode in words", async () => {
    stubStaffApi({ accounts: { 101: account({ link: managed() }) } });
    await openTab();
    expect(await screen.findByText(/Local accounts — Staff creates the user/)).toBeInTheDocument();
  });

  it("says the federated mode in words", async () => {
    stubStaffApi({ accounts: { 101: account({ mode: "federated", link: managed() }) } });
    await openTab();
    expect(await screen.findByText(/Federated — the identity provider owns the accounts/)).toBeInTheDocument();
  });

  it("says accounts are unavailable when the mode is absent", async () => {
    stubStaffApi({ accounts: { 101: account({ mode: undefined }) } });
    await openTab();
    expect(await screen.findByText(/Accounts are unavailable/)).toBeInTheDocument();
  });

  it("explains the derived day when HR set none, from the earliest employment that has not ended", async () => {
    stubStaffApi();
    const card = await openTab();
    expect(within(card).getByRole("switch", { name: /Staff provides an account/ })).not.toBeChecked();
    expect(
      within(card).getByText("Left empty, the first day of the earliest employment that has not ended: Jan 1, 2025."),
    ).toBeInTheDocument();
  });

  it("says there is no day yet when the person has no employment to derive it from", async () => {
    stubStaffApi({ accounts: { 101: account({ provisionFromEffective: undefined }) } });
    const card = await openTab();
    expect(
      within(card).getByText(/there is none yet, so there is no day to enable the account on/),
    ).toBeInTheDocument();
  });

  it("warns when the chosen address is missing, before and after choosing it", async () => {
    stubStaffApi({
      accounts: { 101: account({ accountEmailKind: "private", address: { kind: "private", present: false } }) },
    });
    const card = await openTab();
    expect(within(card).getByText(/Ada Lovelace has no private email on file/)).toBeInTheDocument();
    // Ada has a work email: choosing it takes the warning away.
    await userEvent.click(within(card).getByRole("radio", { name: "Work email" }));
    expect(within(card).queryByText(/has no private email on file/)).not.toBeInTheDocument();
    expect(within(card).queryByText(/has no work email on file/)).not.toBeInTheDocument();
  });

  it("saves all three fields with the account's own revision", async () => {
    const fetchMock = stubStaffApi();
    const card = await openTab();
    await userEvent.click(within(card).getByRole("switch", { name: /Staff provides an account/ }));
    await typeDate(within(card).getByRole("textbox", { name: /Account from/ }), "Nov 2, 2026");
    await userEvent.click(within(card).getByRole("radio", { name: "Private email" }));
    await userEvent.click(within(card).getByRole("button", { name: "Save the account flag" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/people\/101\/account$/)).toHaveLength(1), slow);
    const [put] = sent(fetchMock, "PUT", /\/people\/101\/account$/);
    // The account's revision, never the person's (3).
    expect(put.ifMatch).toBe("1");
    expect(put.body).toEqual({ provisionAccount: true, provisionFrom: "2026-11-02", accountEmailKind: "private" });
    expect(fetchMock.state.accounts[101].revision).toBe(2);
  });

  it("sends a cleared day as null, the other two as they are", async () => {
    const fetchMock = stubStaffApi({ accounts: { 101: account({ provisionFrom: "2026-11-02" }) } });
    const card = await openTab();
    expect(within(card).getByText(/The account is enabled on this day/)).toBeInTheDocument();
    await retype(within(card).getByRole("textbox", { name: /Account from/ }), "");
    await userEvent.tab();
    await userEvent.click(within(card).getByRole("button", { name: "Save the account flag" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/people\/101\/account$/)).toHaveLength(1), slow);
    expect(sent(fetchMock, "PUT", /\/people\/101\/account$/)[0]).toMatchObject({
      ifMatch: "4",
      body: { provisionAccount: true, provisionFrom: null, accountEmailKind: "work" },
    });
  });

  it("says a revision conflict in words and reloads", async () => {
    const fetchMock = stubStaffApi({
      write: (method, path) =>
        method === "PUT" && path.endsWith("/account")
          ? staffProblem(409, { code: "revision_conflict", title: "Revision conflict", detail: "revision 5" })
          : undefined,
    });
    const card = await openTab();
    await userEvent.click(within(card).getByRole("switch", { name: /Staff provides an account/ }));
    await userEvent.click(within(card).getByRole("button", { name: "Save the account flag" }));
    expect(await screen.findByText("Somebody else saved this; it has been reloaded", {}, slow)).toBeInTheDocument();
    await waitFor(() => expect(sent(fetchMock, "GET", /\/people\/101\/account$/).length).toBeGreaterThan(1));
  });
});

describe("Person page — the account's link", () => {
  const states: [string, Link, RegExp][] = [
    ["preparing", { state: "preparing", managed: true, attempts: 0 }, /Being prepared/],
    ["pending", managed({ state: "pending", invitation: { state: "active", issued: 1 } }), /Pending — waiting for/],
    ["active", managed(), /^Active$/],
    ["disabled by Staff", managed({ state: "disabled" }), /^Disabled by Staff$/],
    ["disabled by an Owner", managed({ state: "disabled", reason: "disabled_by_owner" }), /^Disabled by an Owner$/],
    ["failed", managed({ state: "failed", attempts: 2 }), /^Failed$/],
    ["blocked", managed({ state: "blocked", reason: "user_gone" }), /^Blocked$/],
    [
      "linked by hand",
      { state: "linked", managed: false, attempts: 0, user: ADA_USER },
      /Linked, not managed by Staff/,
    ],
  ];
  it.each(states)("says the state %s in words", async (_, link, words) => {
    stubStaffApi({ accounts: { 101: account({ link }) } });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    expect(within(card).getByText(words)).toBeInTheDocument();
  });

  it("says no user has been made or found yet when there is no link", async () => {
    stubStaffApi({ accounts: { 101: account() } });
    renderRoute("/staff/people/101?tab=account");
    expect(within(await region("User account")).getByText(/No user yet/)).toBeInTheDocument();
  });

  const reasons: [string, RegExp][] = [
    ["no_employment", /no employment, so there is no day/],
    ["account_email_missing", /does not have the address the flag names/],
    ["blocked_by_owner", /An Owner disabled the pending user/],
    ["disabled_by_owner", /An Owner disabled the user while the person is employed/],
    ["user_gone", /no longer exists; Staff never creates a second/],
    ["mismatch", /belongs to another employee number/],
    ["mode_changed", /created for local accounts, but the installation is now federated/],
    ["email_differs", /address in identity differs from the person's chosen address/],
    ["invitation_revoked", /An Owner revoked the invitation; Run now sends a fresh one/],
    ["invitation_unanswered", /Three invitations went unanswered; Run now sends a fresh one/],
    ["account_not_managed", /no longer managed by Staff/],
    ["account_privileged", /an Owner or a system administrator/],
    ["not_found", /No user with the person's employee number exists yet/],
    ["account_email_taken", /Another user, not one Staff manages .*, holds the address/],
    ["employee_number_taken", /A user Staff does not manage holds the employee number/],
    ["invalid_actor", /A fault in Staff stopped the call/],
    ["employee_role_missing", /No Owner has named the roles Staff may give/],
    ["employee_role_unsafe", /grants administration/],
    ["mode_mismatch", /authority mode differs/],
  ];
  it.each(reasons)("says the reason %s in words", async (reason, words) => {
    stubStaffApi({ accounts: { 101: account({ link: managed({ state: "blocked", reason }) }) } });
    renderRoute("/staff/people/101?tab=account");
    expect(within(await region("User account")).getByText(words)).toBeInTheDocument();
  });

  it("says a failing account, the invitation's state, the next attempt and the last sync", async () => {
    stubStaffApi({
      accounts: {
        101: account({
          link: managed({
            state: "failed",
            attempts: 3,
            nextAttemptAt: "2026-10-12T00:00:00Z",
            invitation: { state: "expired", issued: 2 },
          }),
        }),
      },
    });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    expect(within(card).getByText(/The last 3 attempts failed/)).toBeInTheDocument();
    expect(within(card).getByText(/The latest invitation lapsed unanswered · 2 sent/)).toBeInTheDocument();
    expect(within(card).getByText(/Oct 12, 2026/)).toBeInTheDocument();
    expect(within(card).getByText(/Oct 11, 2026/)).toBeInTheDocument();
  });

  it("shows HR the Owner's adoption step for an existing user Staff does not manage", async () => {
    stubStaffApi({
      accounts: { 101: account({ link: { state: "linked", managed: false, attempts: 0, user: ADA_USER } }) },
    });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    expect(within(card).getByText(/an Owner adopts the user for Staff on the Users page/)).toBeInTheDocument();
    expect(within(card).getByText(/E-1/)).toBeInTheDocument();
    expect(within(card).getByRole("link", { name: "Open Users" })).toHaveAttribute("href", "/workspace/users");
  });
});

describe("Person page — Run now and Disable now", () => {
  it("runs now and says a fresh invitation was sent", async () => {
    const fetchMock = stubStaffApi({
      accounts: {
        101: account({
          link: managed({
            state: "pending",
            reason: "invitation_unanswered",
            invitation: { state: "exhausted", issued: 3 },
          }),
        }),
      },
    });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    await userEvent.click(within(card).getByRole("button", { name: "Run now" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/people\/101\/account\/run$/)).toHaveLength(1), slow);
    expect(await within(card).findByText(/Done: a new invitation was sent/, {}, slow)).toBeInTheDocument();
    expect(await within(card).findByText(/An invitation is open and can be accepted · 1 sent/)).toBeInTheDocument();
  });

  it("says no invitation was sent as a success, not a failure", async () => {
    stubStaffApi({ accounts: { 101: account({ link: managed() }) } });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    await userEvent.click(within(card).getByRole("button", { name: "Run now" }));
    expect(await within(card).findByText(/Done: no invitation was sent/, {}, slow)).toBeInTheDocument();
  });

  it.each([
    ["accounts_busy", 409, /acting on this account right now/],
    ["accounts_unavailable", 503, /Identity could not be reached/],
    ["person_erased", 409, /This record is erased/],
  ])("says Run now's refusal %s in words", async (code, status, words) => {
    stubStaffApi({
      accounts: { 101: account({ link: managed() }) },
      write: (method, path) =>
        method === "POST" && path.endsWith("/account/run")
          ? staffProblem(status, { code, title: "Refused", detail: "The server's English" })
          : undefined,
    });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    await userEvent.click(within(card).getByRole("button", { name: "Run now" }));
    expect(await screen.findByText(words, {}, slow)).toBeInTheDocument();
    expect(screen.queryByText("The server's English")).not.toBeInTheDocument();
  });

  it("offers no Run now while the flag is off", async () => {
    stubStaffApi({ accounts: { 101: account({ provisionAccount: false, link: managed({ state: "disabled" }) }) } });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    expect(within(card).queryByRole("button", { name: "Run now" })).not.toBeInTheDocument();
  });

  it("disables now after saying it also turns the flag off", async () => {
    const fetchMock = stubStaffApi({ accounts: { 101: account({ link: managed() }) } });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    await userEvent.click(within(card).getByRole("button", { name: "Disable now" }));
    const dialog = await screen.findByRole("dialog", { name: "Disable Ada Lovelace's account now?" });
    expect(within(dialog).getByText(/This also turns the account flag off/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Disable now" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/people\/101\/account\/disable$/)).toHaveLength(1), slow);
    expect(
      await within(card).findByText(/The account is disabled and the account flag is off/, {}, slow),
    ).toBeInTheDocument();
    expect(await within(card).findByText(/^Disabled by Staff$/)).toBeInTheDocument();
    await waitFor(() =>
      expect(within(screen.getByRole("region", { name: "Account flag" })).getByRole("switch")).not.toBeChecked(),
    );
  });

  it.each([
    ["account_not_managed", managed({ managed: false, state: "linked" }), /Staff does not manage them/],
    ["account_privileged", managed({ reason: "account_privileged" }), /an Owner or a system administrator/],
    ["account_mismatch", managed({ reason: "mismatch" }), /another employee number/],
  ])("says Disable now's refusal %s in words, the flag already off", async (_, link, words) => {
    stubStaffApi({ accounts: { 101: account({ link }) } });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    await userEvent.click(within(card).getByRole("button", { name: "Disable now" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: /Disable Ada Lovelace's account now/ })).getByRole("button", {
        name: "Disable now",
      }),
    );
    const said = await within(card).findByText(words, { selector: "[role=alert] *" }, slow);
    expect(said).toHaveTextContent(/The account flag is off, but the user was not disabled/);
    await waitFor(() =>
      expect(within(screen.getByRole("region", { name: "Account flag" })).getByRole("switch")).not.toBeChecked(),
    );
  });

  it("says identity was unreachable on Disable now, the flag already off", async () => {
    stubStaffApi({
      accounts: { 101: account({ link: managed() }) },
      write: (method, path) =>
        method === "POST" && path.endsWith("/account/disable")
          ? staffProblem(503, { code: "accounts_unavailable", title: "Accounts are unavailable" })
          : undefined,
    });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    await userEvent.click(within(card).getByRole("button", { name: "Disable now" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: /Disable Ada Lovelace's account now/ })).getByRole("button", {
        name: "Disable now",
      }),
    );
    expect(
      await within(card).findByText(/The account flag is off, but identity could not be reached/, {}, slow),
    ).toBeInTheDocument();
  });
});

describe("Person page — who sees the Account tab", () => {
  it("is read-only for the person themselves", async () => {
    stubStaffApi({ role: "view", me: 101, accounts: { 101: account({ link: managed({ state: "pending" }) }) } });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("Account flag");
    expect(within(card).getByText("Yes")).toBeInTheDocument();
    expect(within(card).getByText("Work email")).toBeInTheDocument();
    expect(within(card).queryByRole("switch")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save the account flag" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Run now" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Disable now" })).not.toBeInTheDocument();
    expect(within(await region("User account")).getByText(/Pending — waiting for/)).toBeInTheDocument();
  });

  it("is read-only for a holder of staff:personal-data alone, who reads the links group", async () => {
    stubStaffApi({ role: "personalDataOnly", accounts: { 101: account({ link: managed({ state: "pending" }) }) } });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("Account flag");
    expect(screen.getByRole("tab", { name: "Account", selected: true })).toBeInTheDocument();
    expect(within(card).getByText("Yes")).toBeInTheDocument();
    expect(within(card).queryByRole("switch")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save the account flag" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Run now" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Disable now" })).not.toBeInTheDocument();
    expect(within(await region("User account")).getByText(/Pending — waiting for/)).toBeInTheDocument();
  });

  it("is on My page for the person, read-only", async () => {
    stubStaffApi({ role: "access", me: 101, accounts: { 101: account() } });
    renderRoute("/staff/me?tab=account");
    const card = await region("Account flag");
    expect(within(card).queryByRole("switch")).not.toBeInTheDocument();
    expect(within(card).getByText("Yes")).toBeInTheDocument();
  });

  it("is absent for a manager", async () => {
    const fetchMock = stubStaffApi({ role: "view", me: 101 });
    renderRoute("/staff/people/102?tab=account");
    await screen.findByRole("heading", { name: "Grace Hopper", level: 2 });
    expect(screen.queryByRole("tab", { name: "Account" })).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Profile", selected: true })).toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/account$/)).toHaveLength(0);
  });

  it("is absent for an Approver", async () => {
    stubStaffApi({ role: "approve" });
    renderRoute("/staff/people/101?tab=account");
    await screen.findByRole("heading", { name: "Ada Lovelace", level: 2 });
    expect(screen.queryByRole("tab", { name: "Account" })).not.toBeInTheDocument();
  });

  it("offers HR only Disable now on an erased record", async () => {
    stubStaffApi({
      role: "manage",
      ...withErased(erasedPerson()),
      accounts: { 106: account({ address: { kind: "work", present: false }, link: managed({ user: undefined }) }) },
    });
    renderRoute("/staff/people/106?tab=account");
    const card = await region("Account flag");
    expect(within(card).queryByRole("switch")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save the account flag" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Run now" })).not.toBeInTheDocument();
    expect(within(await region("User account")).getByRole("button", { name: "Disable now" })).toBeInTheDocument();
  });
});

describe("Profile — the self-service link and Staff's accounts", () => {
  it("says link_managed in words when linking a user Staff manages", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "PUT" && path.endsWith("/102/user")
          ? staffProblem(409, { code: "link_managed", title: "Managed by Staff", detail: "The server's English" })
          : undefined,
    });
    renderRoute("/staff/people/102");
    const group = await region("Self-service");
    await userEvent.type(within(group).getByRole("combobox", { name: "User" }), "Grace");
    await userEvent.click(await screen.findByRole("option", { name: "Grace at Fjord" }, { timeout: 10_000 }));
    await userEvent.click(within(group).getByRole("button", { name: "Link" }));
    expect(
      await screen.findByText(/managed by Staff's accounts, so it is not linked or unlinked by hand/, {}, slow),
    ).toBeInTheDocument();
  });

  it("says accounts_unavailable in words", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "PUT" && path.endsWith("/user")
          ? staffProblem(503, { code: "accounts_unavailable", title: "Accounts are unavailable" })
          : undefined,
    });
    renderRoute("/staff/people/101");
    const group = await region("Self-service");
    await userEvent.click(within(group).getByRole("button", { name: "Unlink" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: /Unlink/ })).getByRole("button", { name: "Unlink" }),
    );
    expect(await screen.findByText(/Identity could not be reached/, {}, slow)).toBeInTheDocument();
  });

  it("names a managed link as Staff's, with no unlink", async () => {
    stubStaffApi({
      accounts: {
        102: account({ link: managed({ user: { ...ADA_USER, id: "u-102", displayName: "Grace at Fjord" } }) }),
      },
    });
    renderRoute("/staff/people/102");
    const group = await region("Self-service");
    expect(within(group).getByText(/Grace at Fjord — managed by Staff/)).toBeInTheDocument();
    expect(within(group).queryByRole("button", { name: "Unlink" })).not.toBeInTheDocument();
    expect(within(group).queryByRole("button", { name: "Link" })).not.toBeInTheDocument();
  });
});
