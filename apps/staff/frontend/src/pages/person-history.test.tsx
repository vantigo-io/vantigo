import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { StaffEvent } from "../api/people";
import { region } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubStaffApi } from "../test/server";

const hr = { kind: "user", displayName: "Hilde HR" };

/** Grace's history as HR reads it: a private and a work field changed, then a pay correction alone. */
const graceHistory = (): Record<number, StaffEvent[]> => ({
  102: [
    {
      id: 502,
      kind: "terms_corrected",
      at: "2026-09-02T08:00:00Z",
      actor: hr,
      payload: { employmentId: 202, termsId: 303, fields: { payGrade: { from: "L2", to: "L3" } } },
    },
    {
      id: 501,
      kind: "person_updated",
      at: "2026-09-01T08:00:00Z",
      actor: hr,
      payload: {
        fields: {
          birthDate: { from: "1985-06-02", to: "1985-06-01" },
          workEmail: { from: "grace@old.example", to: "grace@fjord.example" },
        },
      },
    },
  ],
});

const events = () => screen.findAllByRole("article");

describe("History", () => {
  it("lists HR every change newest first, with the fields from and to, the actor and the time", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=history");

    const [revealed, corrected, updated] = await events();
    // A reveal names no field for anyone: it is the kind itself, nothing withheld.
    expect(within(revealed).getByText("National identity number revealed")).toBeInTheDocument();
    expect(revealed).toHaveTextContent("Hilde HR");
    expect(revealed).toHaveTextContent("Oct 10, 2026");
    expect(within(corrected).getByText("Pay grade: L6 → L7")).toBeInTheDocument();
    expect(within(corrected).getByText("Title: Engineer II → Senior engineer")).toBeInTheDocument();
    expect(within(updated).getByText("Work email: ada@old.example → ada@fjord.example")).toBeInTheDocument();
    expect(within(updated).getByText("Birth date: Dec 1, 1990 → Dec 10, 1990")).toBeInTheDocument();
    expect(within(updated).getByText("HR's notes: — → Asked for a standing desk")).toBeInTheDocument();
  });

  it("shows a manager a private change as changed, a work change with its values, and no pay", async () => {
    stubStaffApi({ role: "access", me: 101, events: graceHistory() });
    renderRoute("/staff/people/102?tab=history");

    const [corrected, updated] = await events();
    // The pay correction is there as its kind alone, said to be so: the day
    // a pay field changed is pay.
    expect(within(corrected).getByText("Terms corrected (details not visible to you)")).toBeInTheDocument();
    expect(corrected).not.toHaveTextContent(/Pay grade|L3/);
    expect(within(updated).getByText("Birth date changed")).toBeInTheDocument();
    expect(updated).not.toHaveTextContent("1985");
    expect(within(updated).getByText("Work email: grace@old.example → grace@fjord.example")).toBeInTheDocument();
  });

  it("offers a colleague no history at all", async () => {
    stubStaffApi({ role: "view" });
    renderRoute("/staff/people/101");

    await region("Work");
    expect(screen.getByRole("tab", { name: "Employments" })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "History" })).not.toBeInTheDocument();
  });

  it("pages through a long history", async () => {
    const many: StaffEvent[] = Array.from({ length: 30 }, (_, index) => ({
      id: 600 + index,
      kind: "roles_updated",
      at: `2026-09-${String(30 - index).padStart(2, "0")}T08:00:00Z`,
      actor: { ...hr, displayName: `Actor ${index}` },
      payload: {},
    }));
    stubStaffApi({ role: "manage", events: { 101: many }, pageSize: 25 });
    renderRoute("/staff/people/101?tab=history");

    expect(await screen.findByText("Actor 0")).toBeInTheDocument();
    expect(screen.queryByText("Actor 29")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "2" }));
    expect(await screen.findByText("Actor 29")).toBeInTheDocument();
  });

  it("names the structure and the people a change points at, and never says a bare id", async () => {
    stubStaffApi({
      role: "manage",
      events: {
        101: [
          {
            id: 701,
            kind: "terms_corrected",
            at: "2026-09-03T08:00:00Z",
            actor: hr,
            payload: {
              employmentId: 201,
              termsId: 302,
              fields: {
                departmentId: { from: 21, to: 22 },
                locationId: { from: 31 },
                managerPersonId: { from: 999, to: 102 },
                businessUnitId: { from: 11, to: 12 },
              },
            },
          },
        ],
      },
    });
    renderRoute("/staff/people/101?tab=history");

    const [corrected] = await events();
    expect(within(corrected).getByText("Department: Engineering → Operations")).toBeInTheDocument();
    expect(within(corrected).getByText("Location: Oslo HQ → —")).toBeInTheDocument();
    expect(within(corrected).getByText("Business unit: Oslo office → Bergen office")).toBeInTheDocument();
    // Person 999 is nobody this page knows: the change is said, the number is not.
    expect(within(corrected).getByText("Manager changed")).toBeInTheDocument();
    expect(corrected).not.toHaveTextContent(/999|\b21\b|\b102\b/);
  });

  it("shows the History tab and its error when the history fails for another reason than not being the reader's", async () => {
    stubStaffApi({
      role: "manage",
      read: (path) =>
        path.endsWith("/people/101/events")
          ? new Response(JSON.stringify({ title: "Boom" }), { status: 500 })
          : undefined,
    });
    renderRoute("/staff/people/101");

    await userEvent.click(await screen.findByRole("tab", { name: "History" }));
    expect(await screen.findByText("Could not load the history")).toBeInTheDocument();
  });

  it("keeps the tab in the URL", async () => {
    stubStaffApi({ role: "manage" });
    const { router } = renderRoute("/staff/people/101");

    await userEvent.click(await screen.findByRole("tab", { name: "History" }));
    expect(router.state.location.search).toMatchObject({ tab: "history" });
    expect((await events()).length).toBe(3);
  });
});

/** Ada's account history as HR reads it (delivery C): the flag changed, then the worker's steps. */
const accountHistory = (): Record<number, StaffEvent[]> => ({
  101: [
    {
      id: 605,
      kind: "account_deactivated",
      at: "2026-10-05T08:00:00Z",
      actor: { kind: "system", displayName: "Staff accounts" },
      payload: { step: "deactivate", state: "disabled", flagOffMeanwhile: true },
    },
    {
      id: 604,
      kind: "account_failed",
      at: "2026-10-04T08:00:00Z",
      actor: { kind: "system", displayName: "Staff accounts" },
      payload: { step: "activate", state: "pending", code: "accounts_unavailable" },
    },
    {
      id: 603,
      kind: "account_blocked",
      at: "2026-10-03T08:00:00Z",
      actor: { kind: "system", displayName: "Staff accounts" },
      payload: { step: "activate", state: "blocked", code: "employee_role_missing" },
    },
    {
      id: 602,
      kind: "account_activated",
      at: "2026-10-02T08:00:00Z",
      actor: hr,
      payload: { step: "activate", state: "pending", invitation: { state: "active", issued: 1 } },
    },
    {
      id: 601,
      kind: "account_flag_updated",
      at: "2026-10-01T08:00:00Z",
      actor: hr,
      payload: {
        fields: {
          provisionAccount: { from: false, to: true },
          provisionFrom: { from: null, to: "2026-11-02" },
          accountEmailKind: { from: "work", to: "private" },
        },
      },
    },
  ],
});

describe("History — the account events", () => {
  it("names each account event and says its fields and codes in words", async () => {
    stubStaffApi({ role: "manage", events: accountHistory() });
    renderRoute("/staff/people/101?tab=history");

    const [deactivated, failed, blocked, activated, flag] = await events();
    expect(within(flag).getByText("Account flag changed")).toBeInTheDocument();
    expect(within(flag).getByText("Staff provides an account: No → Yes")).toBeInTheDocument();
    expect(within(flag).getByText("Account from: — → Nov 2, 2026")).toBeInTheDocument();
    expect(within(flag).getByText("Address: Work email → Private email")).toBeInTheDocument();

    expect(within(activated).getByText("Account enabled")).toBeInTheDocument();
    expect(within(activated).getByText("Step: Enable the user")).toBeInTheDocument();
    expect(
      within(activated).getByText("State: Pending — waiting for the person to choose a password"),
    ).toBeInTheDocument();
    expect(
      within(activated).getByText("Invitation: An invitation is open and can be accepted · 1 sent"),
    ).toBeInTheDocument();

    expect(within(blocked).getByText("Account held up")).toBeInTheDocument();
    expect(
      within(blocked).getByText("No Owner has named the roles Staff may give, so no account is enabled."),
    ).toBeInTheDocument();
    expect(within(failed).getByText("Account step failed")).toBeInTheDocument();
    expect(within(failed).getByText(/Identity could not be reached; the worker tries again/)).toBeInTheDocument();

    expect(within(deactivated).getByText("Account disabled")).toBeInTheDocument();
    expect(within(deactivated).getByText(/The account flag was turned off meanwhile/)).toBeInTheDocument();
    // No code is shown raw.
    for (const raw of ["deactivate", "employee_role_missing", "accounts_unavailable", "pending"])
      expect(screen.queryByText(raw)).not.toBeInTheDocument();
  });
});

describe("History — the account events' readers", () => {
  it("lists a manager none of the account events, which are the links group's", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      events: {
        102: [
          {
            id: 701,
            kind: "account_flag_updated",
            at: "2026-10-01T08:00:00Z",
            actor: hr,
            payload: { fields: { provisionAccount: { from: false, to: true } } },
          },
          {
            id: 700,
            kind: "account_prepared",
            at: "2026-09-30T08:00:00Z",
            actor: hr,
            payload: { step: "prepare", state: "pending" },
          },
          ...graceHistory()[102],
        ],
      },
    });
    renderRoute("/staff/people/102?tab=history");
    expect(await events()).toHaveLength(2);
    expect(screen.queryByText("Account flag changed")).not.toBeInTheDocument();
    expect(screen.queryByText("Account prepared")).not.toBeInTheDocument();
  });
});
