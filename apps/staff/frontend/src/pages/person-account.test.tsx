import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { components } from "../api-schema";
import { entraProvider, erasedPerson, meta, withErased } from "../test/fixtures";
import { choose, region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

type Account = components["schemas"]["StaffPersonAccount"];
type Link = components["schemas"]["StaffAccountLink"];
type ProviderLink = components["schemas"]["StaffProviderLink"];

/** A round trip — a write, the refetch it causes, the render — on a loaded runner. */
const slow = { timeout: 5000 };

const ADA_USER = { id: "8d5c6a3e-0000-4000-8000-000000000101", displayName: "Ada at Fjord", active: true };

/** Ada's account as HR turned it on: the work address on file, the day from her employment. */
const account = (overrides: Partial<Account> = {}): Account => ({
  provisionAccount: true,
  accountEmailKind: "work",
  address: { kind: "work", present: true },
  provisionFromEffective: "2025-01-01",
  accountKind: "local",
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

  it("says the local kind in words", async () => {
    stubStaffApi({ accounts: { 101: account({ link: managed() }) } });
    await openTab();
    expect(await screen.findByText(/Local account — Staff creates the user/)).toBeInTheDocument();
  });

  it("says the external kind in words", async () => {
    stubStaffApi({ accounts: { 101: account({ accountKind: "external", link: managed() }) } });
    await openTab();
    expect(await screen.findByText(/External identity provider — the provider owns the account/)).toBeInTheDocument();
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
    ["mode_changed", /created for a local account, but the person's account kind is now one that never invites/],
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
    expect(within(card).getByText(/Failed attempts in a row: 3\./)).toBeInTheDocument();
    expect(within(card).getByText(/The latest invitation lapsed unanswered · 2 sent/)).toBeInTheDocument();
    expect(within(card).getByText(/Oct 12, 2026/)).toBeInTheDocument();
    expect(within(card).getByText(/Oct 11, 2026/)).toBeInTheDocument();
  });

  it("shows no adoption step for a hand link while the flag is off", async () => {
    const hand = { state: "linked", managed: false, attempts: 0, user: ADA_USER };
    stubStaffApi({ accounts: { 101: account({ provisionAccount: false, link: hand }) } });
    renderRoute("/staff/people/101?tab=account");
    expect(within(await region("User account")).queryByText(/adopts the user for Staff/)).not.toBeInTheDocument();
  });

  it("shows no adoption step on an erased record", async () => {
    stubStaffApi({
      ...withErased(erasedPerson()),
      accounts: { 106: account({ link: managed({ managed: false, reason: "account_not_managed" }) }) },
    });
    renderRoute("/staff/people/106?tab=account");
    expect(within(await region("User account")).queryByText(/adopts the user for Staff/)).not.toBeInTheDocument();
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

  it("offers no Disable now on HR's self-service link, which is not Staff's to disable", async () => {
    stubStaffApi({
      accounts: {
        101: account({
          provisionAccount: false,
          link: { state: "linked", managed: false, attempts: 0, user: ADA_USER },
        }),
      },
    });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    expect(within(card).queryByRole("button", { name: "Disable now" })).not.toBeInTheDocument();
  });

  it("offers no Disable now on HR's self-service link even with the flag on", async () => {
    stubStaffApi({
      accounts: { 101: account({ link: { state: "linked", managed: false, attempts: 0, user: ADA_USER } }) },
    });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    expect(within(card).getByRole("button", { name: "Run now" })).toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "Disable now" })).not.toBeInTheDocument();
  });

  it("says there was no account to disable when the person had no user", async () => {
    stubStaffApi({ accounts: { 101: account() } });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    await userEvent.click(within(card).getByRole("button", { name: "Disable now" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: /Disable Ada Lovelace's account now/ })).getByRole("button", {
        name: "Disable now",
      }),
    );
    expect(
      await within(card).findByText("The account flag is off; there was no account to disable.", {}, slow),
    ).toBeInTheDocument();
  });

  it("says the directory account is disabled when only the provider's account was", async () => {
    stubStaffApi({
      ...withEntra,
      accounts: { 101: entraAccount({ link: undefined }) },
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
      await within(card).findByText("The account flag is off and the directory account is disabled.", {}, slow),
    ).toBeInTheDocument();
    expect(within(card).queryByText(/there was no account to disable/)).not.toBeInTheDocument();
  });

  it.each([
    ["Run now", /\/account\/run$/],
    ["Disable now", /\/account\/disable$/],
  ])("reloads the account after %s is refused accounts_busy", async (button, pattern) => {
    const fetchMock = stubStaffApi({
      accounts: { 101: account({ link: managed() }) },
      write: (method, path) =>
        method === "POST" && pattern.test(path)
          ? staffProblem(409, { code: "accounts_busy", title: "Accounts busy" })
          : undefined,
    });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("User account");
    await userEvent.click(within(card).getByRole("button", { name: button }));
    if (button === "Disable now")
      await userEvent.click(
        within(await screen.findByRole("dialog", { name: /Disable Ada/ })).getByRole("button", { name: button }),
      );
    expect(await within(card).findByText(/acting on this account right now/, {}, slow)).toBeInTheDocument();
    await waitFor(() => expect(sent(fetchMock, "GET", /\/people\/101\/account$/).length).toBeGreaterThan(1), slow);
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
    ["account_not_managed", managed({ managed: false }), /Staff does not manage them/],
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

describe("The fake server's account writes", () => {
  it.each([
    ["PUT", "/api/v1/staff/people/101/account"],
    ["POST", "/api/v1/staff/people/101/account/run"],
    ["POST", "/api/v1/staff/people/101/account/disable"],
  ])("answers %s %s for the person themselves with the access layer's 403", async (method, url) => {
    stubStaffApi({ role: "access", me: 101, accounts: { 101: account() } });
    const answer = await fetch(url, {
      method,
      headers: { "Content-Type": "application/json", "If-Match": "4" },
      body: JSON.stringify({ provisionAccount: false, provisionFrom: null, accountEmailKind: "work" }),
    });
    expect(answer.status).toBe(403);
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

  it("offers the picker for a link still being prepared, which names no user", async () => {
    stubStaffApi({ accounts: { 102: account({ link: { state: "preparing", managed: true, attempts: 0 } }) } });
    renderRoute("/staff/people/102");
    const group = await region("Self-service");
    expect(within(group).getByText("Not linked to a user")).toBeInTheDocument();
    expect(within(group).getByRole("combobox", { name: "User" })).toBeInTheDocument();
  });

  it("names a link by whether Staff manages it, not by its state", async () => {
    stubStaffApi({
      accounts: {
        102: account({
          link: managed({ managed: false, user: { ...ADA_USER, id: "u-102", displayName: "Grace at Fjord" } }),
        }),
      },
    });
    renderRoute("/staff/people/102");
    const group = await region("Self-service");
    expect(within(group).getByText(/Grace at Fjord — linked, not managed by Staff/)).toBeInTheDocument();
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

/** Ada's account in Microsoft Entra ID as the worker left it: enabled, a pass not yet shown. */
const entraLink = (overrides: Partial<ProviderLink> = {}): ProviderLink => ({
  provider: "entra",
  state: "enabled",
  attempts: 0,
  externalUpn: "ada.lovelace@fjord.example",
  hasPass: true,
  passIssuedAt: "2026-10-11T06:00:00Z",
  announcedAt: "2026-10-01T03:00:00Z",
  lastSyncedAt: "2026-10-10T03:00:00Z",
  reportedCodes: [],
  ...overrides,
});

const entraAccount = (overrides: Partial<Account> = {}): Account =>
  account({ accountKind: "entra", link: managed(), providerLink: entraLink(), ...overrides });

/** HR's meta once the Entra module offers its provider. */
const withEntra = { meta: meta("manage", { providers: [entraProvider()] }) };

describe("Person page — the account kind", () => {
  it("offers local and external, not entra, when no module offers the provider", async () => {
    stubStaffApi();
    const card = await openTab();
    await userEvent.click(within(card).getByRole("combobox", { name: /Account kind/ }));
    expect(await screen.findByRole("option", { name: "Local" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "External identity provider" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "Microsoft Entra ID" })).not.toBeInTheDocument();
  });

  it("offers entra when /meta lists its provider, and sends the changed kind", async () => {
    const fetchMock = stubStaffApi(withEntra);
    const card = await openTab();
    await choose(card, /Account kind/, "Microsoft Entra ID");
    await userEvent.click(within(card).getByRole("button", { name: "Save the account flag" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/people\/101\/account$/)).toHaveLength(1), slow);
    expect(sent(fetchMock, "PUT", /\/people\/101\/account$/)[0].body).toEqual({
      provisionAccount: false,
      provisionFrom: null,
      accountEmailKind: "work",
      accountKind: "entra",
    });
    expect(fetchMock.state.accounts[101].accountKind).toBe("entra");
  });

  it("says account_kind_locked on the select when leaving entra while its account is held", async () => {
    const fetchMock = stubStaffApi({ ...withEntra, accounts: { 101: entraAccount() } });
    const card = await openTab();
    await choose(card, /Account kind/, "Local");
    await userEvent.click(within(card).getByRole("button", { name: "Save the account flag" }));
    expect(
      await within(card).findByText(/cannot leave Microsoft Entra ID while Microsoft Entra ID holds an account/, {}, slow),
    ).toBeInTheDocument();
    expect(fetchMock.state.accounts[101].accountKind).toBe("entra");
  });

  it("says provider_missing on the select when the server no longer offers the provider", async () => {
    stubStaffApi({
      ...withEntra,
      write: (method, path) =>
        method === "PUT" && path.endsWith("/account")
          ? staffProblem(400, {
              code: "provider_missing",
              title: "Invalid account",
              errors: { accountKind: ["The server's English"] },
            })
          : undefined,
    });
    const card = await openTab();
    await choose(card, /Account kind/, "Microsoft Entra ID");
    await userEvent.click(within(card).getByRole("button", { name: "Save the account flag" }));
    expect(
      await within(card).findByText(/No enabled module offers this identity provider/, {}, slow),
    ).toBeInTheDocument();
    expect(screen.queryByText("The server's English")).not.toBeInTheDocument();
  });

  it("leaves the kind out of a save that does not change it", async () => {
    const fetchMock = stubStaffApi({ accounts: { 101: entraAccount() } });
    const card = await openTab();
    // No provider in /meta: the person's kind is still said, and kept.
    expect(within(card).getByRole("combobox", { name: /Account kind/ })).toHaveValue("Microsoft Entra ID");
    await userEvent.click(within(card).getByRole("switch", { name: /Staff provides an account/ }));
    await userEvent.click(within(card).getByRole("button", { name: "Save the account flag" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/people\/101\/account$/)).toHaveLength(1), slow);
    expect(sent(fetchMock, "PUT", /\/people\/101\/account$/)[0].body).not.toHaveProperty("accountKind");
  });

  it("says the kind read-only to the person themselves", async () => {
    stubStaffApi({ role: "view", me: 101, accounts: { 101: entraAccount() } });
    renderRoute("/staff/people/101?tab=account");
    const card = await region("Account flag");
    expect(within(card).getByText("Microsoft Entra ID")).toBeInTheDocument();
    expect(within(card).queryByRole("combobox")).not.toBeInTheDocument();
  });
});

describe("Person page — the two links", () => {
  it("shows Vantigo's link and the provider's side by side, the provider's named by /meta", async () => {
    stubStaffApi({
      meta: meta("manage", { providers: [entraProvider({ name: "Entra at Fjord" })] }),
      accounts: { 101: entraAccount() },
    });
    renderRoute("/staff/people/101?tab=account");
    const group = await region("User account");
    expect(within(group).getByText(/Microsoft Entra ID — Staff creates the person's account/)).toBeInTheDocument();
    const vantigo = within(group).getByRole("region", { name: "Vantigo" });
    expect(within(vantigo).getByText(/^Active$/)).toBeInTheDocument();
    expect(within(vantigo).getByText("Ada at Fjord")).toBeInTheDocument();
    const entra = within(group).getByRole("region", { name: "Entra at Fjord" });
    expect(within(entra).getByText("Enabled")).toBeInTheDocument();
    expect(within(entra).getByText("ada.lovelace@fjord.example")).toBeInTheDocument();
    expect(within(entra).getByText("Enabled since")).toBeInTheDocument();
    expect(within(entra).getByText(/Oct 10, 2026/)).toBeInTheDocument();
    expect(within(entra).getByText("Announced")).toBeInTheDocument();
    expect(within(group).getByRole("button", { name: "Run now (new pass)" })).toBeInTheDocument();
  });

  it("names the provider's card by the kind when /meta lists no providers (the person's own reading)", async () => {
    stubStaffApi({ role: "view", me: 101, accounts: { 101: entraAccount() } });
    renderRoute("/staff/people/101?tab=account");
    const group = await region("User account");
    expect(within(group).getByRole("region", { name: "Microsoft Entra ID" })).toBeInTheDocument();
    expect(within(group).queryByRole("button", { name: "Show the pass" })).not.toBeInTheDocument();
  });

  it("says no directory account yet for an entra person the worker has not reached", async () => {
    stubStaffApi({ accounts: { 101: account({ accountKind: "entra" }) } });
    renderRoute("/staff/people/101?tab=account");
    const group = await region("User account");
    const entra = within(group).getByRole("region", { name: "Microsoft Entra ID" });
    expect(within(entra).getByText(/No account in the directory yet/)).toBeInTheDocument();
  });

  it("shows one card for a local person", async () => {
    stubStaffApi({ accounts: { 101: account({ link: managed() }) } });
    renderRoute("/staff/people/101?tab=account");
    const group = await region("User account");
    expect(within(group).queryByRole("region", { name: "Vantigo" })).not.toBeInTheDocument();
    expect(within(group).getByRole("button", { name: "Run now" })).toBeInTheDocument();
  });

  const providerWords: [string, Partial<ProviderLink>, RegExp][] = [
    [
      "provider_unavailable",
      { state: "blocked", reason: "provider_unavailable" },
      /is not connected, or its last check/,
    ],
    ["external_ambiguous", { state: "blocked", reason: "external_ambiguous" }, /Two accounts in the directory hold/],
    ["external_address_taken", { state: "blocked", reason: "external_address_taken" }, /another account's in the/],
    ["external_synced_on_premises", { state: "blocked", reason: "external_synced_on_premises" }, /on-premises/],
    [
      "external_privileged",
      { state: "blocked", reason: "external_privileged" },
      /administrative role in the directory, which/,
    ],
    ["external_enabled", { state: "disabled", reason: "external_enabled" }, /still enabled in the directory/],
    ["user_gone", { state: "failed", reason: "user_gone" }, /The account is gone from the directory/],
    ["hire_date_not_written", { reportedCodes: ["hire_date_not_written"] }, /refused the hire date/],
    ["licence_unavailable", { reportedCodes: ["licence_unavailable:SPE_E3"] }, /none is left\. \(SPE_E3\)/],
    ["privileged", { reportedCodes: ["privileged"] }, /leaves it to an administrator/],
    ["pass_not_allowed", { reportedCodes: ["pass_not_allowed"] }, /does not allow a Temporary Access Pass/],
    ["manager_not_in_entra", { reportedCodes: ["manager_not_in_entra"] }, /manager has no account in the directory/],
  ];
  it.each(providerWords)("says the provider's %s in words", async (_, overrides, words) => {
    stubStaffApi({ accounts: { 101: entraAccount({ providerLink: entraLink({ hasPass: false, ...overrides }) }) } });
    renderRoute("/staff/people/101?tab=account");
    const entra = await region("Microsoft Entra ID");
    expect(within(entra).getByText(words)).toBeInTheDocument();
  });

  it("says provider_refused on Run now with the provider's code in words", async () => {
    stubStaffApi({
      accounts: { 101: entraAccount() },
      write: (method, path) =>
        method === "POST" && path.endsWith("/account/run")
          ? staffProblem(409, { code: "provider_refused", title: "Refused", detail: "external_privileged" })
          : undefined,
    });
    renderRoute("/staff/people/101?tab=account");
    const group = await region("User account");
    await userEvent.click(within(group).getByRole("button", { name: "Run now (new pass)" }));
    expect(
      await within(group).findByText(/refused the step; .*administrative role in the directory/, {}, slow),
    ).toBeInTheDocument();
  });

  it("offers Disable now while the provider's account is enabled, even with the flag off", async () => {
    stubStaffApi({ accounts: { 101: entraAccount({ provisionAccount: false, link: undefined }) } });
    renderRoute("/staff/people/101?tab=account");
    const group = await region("User account");
    expect(within(group).getByRole("button", { name: "Disable now" })).toBeInTheDocument();
  });
});

describe("Person page — the pass", () => {
  it("shows the pass once, with its expiry and a copy button, and never asks again", async () => {
    const fetchMock = stubStaffApi({ accounts: { 101: entraAccount() } });
    renderRoute("/staff/people/101?tab=account");
    const entra = await region("Microsoft Entra ID");
    expect(within(entra).getByText("Pass issued")).toBeInTheDocument();
    await userEvent.click(within(entra).getByRole("button", { name: "Show the pass" }));

    const dialog = await screen.findByRole("dialog", { name: "Temporary Access Pass" }, slow);
    expect(within(dialog).getByText("Fjord-7Q2k-9xPa")).toBeInTheDocument();
    expect(within(dialog).getByText(/Works until Oct 12, 2026/)).toBeInTheDocument();
    expect(within(dialog).getByText("It will not be shown again.")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Copy" })).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/people\/101\/account\/pass$/)).toHaveLength(1);
    // The account says no pass is waiting any more, without asking for the pass again.
    expect(within(entra).queryByRole("button", { name: "Show the pass" })).not.toBeInTheDocument();
    expect(within(entra).queryByText("Pass issued")).not.toBeInTheDocument();

    await userEvent.click(within(dialog).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByText("Fjord-7Q2k-9xPa")).not.toBeInTheDocument());
    expect(sent(fetchMock, "POST", /\/account\/pass$/)).toHaveLength(1);
    expect(fetchMock.state.passesRevealed).toBe(1);
  });

  it("says pass_gone in words", async () => {
    stubStaffApi({
      accounts: { 101: entraAccount() },
      write: (method, path) =>
        method === "POST" && path.endsWith("/account/pass")
          ? staffProblem(409, { code: "pass_gone", title: "Pass gone", detail: "The server's English" })
          : undefined,
    });
    renderRoute("/staff/people/101?tab=account");
    const entra = await region("Microsoft Entra ID");
    await userEvent.click(within(entra).getByRole("button", { name: "Show the pass" }));
    expect(await within(entra).findByText(/The pass was shown already, has expired/, {}, slow)).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("offers no pass when none is waiting", async () => {
    stubStaffApi({
      accounts: { 101: entraAccount({ providerLink: entraLink({ hasPass: false, passIssuedAt: undefined }) }) },
    });
    renderRoute("/staff/people/101?tab=account");
    const entra = await region("Microsoft Entra ID");
    expect(within(entra).queryByRole("button", { name: "Show the pass" })).not.toBeInTheDocument();
  });
});

describe("Person page — the directory profile", () => {
  const profile: components["schemas"]["StaffDirectoryField"][] = [
    { key: "givenName", on: true, switchable: false, value: "Ada" },
    { key: "employeeId", on: true, switchable: false, value: "E-1" },
    { key: "jobTitle", on: true, switchable: true, value: "Analyst" },
    { key: "workAddress", on: false, switchable: true, value: "Storgata 1, 0155 Oslo, NO" },
    { key: "manager", on: true, switchable: true },
  ];

  it("lists every field read-only, greys the fixed ones and marks the ones switched off", async () => {
    stubStaffApi({ accounts: { 101: entraAccount({ directoryProfile: profile }) } });
    renderRoute("/staff/people/101?tab=account");
    const table = await screen.findByRole("table", { name: "Directory profile" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(5);
    expect(within(rows[0]).getByText("Given name")).toBeInTheDocument();
    expect(within(rows[0]).getByText("Always")).toBeInTheDocument();
    expect(within(rows[1]).getByText("E-1")).toBeInTheDocument();
    expect(within(rows[2]).getByText("Job title")).toBeInTheDocument();
    expect(within(rows[2]).getByText("Analyst")).toBeInTheDocument();
    expect(within(rows[3]).getByText("Off — not sent")).toBeInTheDocument();
    expect(within(rows[4]).getByText("—")).toBeInTheDocument();
    expect(within(table).queryByRole("switch")).not.toBeInTheDocument();
  });

  it("is shown to the person themselves too", async () => {
    stubStaffApi({ role: "view", me: 101, accounts: { 101: entraAccount({ directoryProfile: profile }) } });
    renderRoute("/staff/people/101?tab=account");
    expect(await screen.findByRole("table", { name: "Directory profile" })).toBeInTheDocument();
  });

  it("is absent when the account carries none", async () => {
    stubStaffApi({ accounts: { 101: account({ link: managed() }) } });
    renderRoute("/staff/people/101?tab=account");
    await region("User account");
    expect(screen.queryByRole("table", { name: "Directory profile" })).not.toBeInTheDocument();
  });
});
