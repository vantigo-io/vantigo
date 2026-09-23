import { i18n } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import {
  attentionHref,
  attentionTitle,
  attentionTitleKey,
  attentionWeek,
  awaitingApprovalHint,
  expensesUnreimbursedValue,
  projectsCardHint,
  readyMilestonesHint,
} from "./dashboard";
import "../i18n";

/**
 * The attention list mixes every enabled module's items, so the link and the
 * title are derived from `module`, `type` and `entityId` — the only three
 * fields whose meaning the modules agreed on. Time's two types are pinned
 * here because both were decided on the server side (task 4) and nothing else
 * in the host would notice them drifting.
 */
describe("the dashboard's attention links", () => {
  it("sends each module's item to the page that answers it", () => {
    expect(attentionHref({ module: "customers", type: "customerIdle", entityId: "42" })).toBe("/customers/42");
    expect(attentionHref({ module: "products", type: "draftProduct", entityId: "9" })).toBe("/products/9");
    expect(attentionHref({ module: "energy", type: "supplyPeriodExpiring", entityId: "12" })).toBe(
      "/energy/metering-points/12",
    );
    expect(attentionHref({ module: "projects", type: "projectOverdue", entityId: "31" })).toBe("/projects/31");
    expect(attentionHref({ module: "communications", type: "conversationNoReply", entityId: "abc" })).toBe(
      "/communications/inbox?conversationId=abc",
    );
  });

  // The four economy signals all open the project's Economy tab. The two
  // budget types carry a bare project id, same as projectOverdue; the two
  // milestone types carry `<projectId>/<milestoneId>`, which is not a URL of
  // its own — naively URL-encoding it (as a bare `encodeURIComponent` would)
  // turns the slash into %2F and breaks the link, so it must be split first.
  it("sends every economy signal to the project's Economy tab, splitting a milestone id", () => {
    expect(attentionHref({ module: "projects", type: "budgetWarning", entityId: "31" })).toBe("/projects/31/economy");
    expect(attentionHref({ module: "projects", type: "budgetExceeded", entityId: "31" })).toBe("/projects/31/economy");
    expect(attentionHref({ module: "projects", type: "milestoneReady", entityId: "31/2001" })).toBe(
      "/projects/31/economy",
    );
    expect(attentionHref({ module: "projects", type: "milestoneOverdue", entityId: "31/2001" })).toBe(
      "/projects/31/economy",
    );
  });

  // A provider bug that sends an empty or otherwise unaddressable entityId
  // must not reach the router as a broken path with a doubled slash; the
  // app's own list is the safest fallback for anything this build cannot
  // address precisely.
  it("falls back to the app's own list for a malformed economy entityId", () => {
    expect(attentionHref({ module: "projects", type: "budgetWarning", entityId: "" })).toBe("/projects");
    expect(attentionHref({ module: "projects", type: "milestoneReady", entityId: "/2001" })).toBe("/projects");
  });

  // An unsubmitted week carries its Monday, so the link opens that very week
  // of the grid; a submission waiting for approval carries `<userId>/<Monday>`,
  // which is the queue's own group key and not a URL, so it opens the queue.
  it("opens the week the grid is missing hours for, and the queue for a waiting approval", () => {
    expect(attentionHref({ module: "time", type: "weekUnsubmitted", entityId: "2026-08-31" })).toBe(
      "/time?week=2026-08-31",
    );
    expect(
      attentionHref({
        module: "time",
        type: "approvalWaiting",
        entityId: "3f1b0c2e-0000-4000-8000-000000000001/2026-09-07",
      }),
    ).toBe("/time/approvals");
  });

  // The server's titles are English (they are built from the data, not from a
  // catalog), so time's two items are named again on this side, in the
  // caller's language, from the type and the week the entity id carries.
  it("names time's items from a catalog rather than the server's English title", () => {
    expect(attentionTitleKey({ module: "time", type: "weekUnsubmitted" })).toBe("dashboard.timeWeekUnsubmitted");
    expect(attentionTitleKey({ module: "time", type: "approvalWaiting" })).toBe("dashboard.timeApprovalWaiting");
    expect(attentionTitleKey({ module: "time", type: "somethingNew" })).toBeUndefined();
    expect(attentionTitleKey({ module: "projects", type: "projectOverdue" })).toBeUndefined();
  });

  // A type this build has never heard of must not be filed under the queue
  // just because it came from time: the app's home fits any of them.
  it("sends an unknown time item to the app rather than to the approval queue", () => {
    expect(attentionHref({ module: "time", type: "somethingNew", entityId: "2026-08-31" })).toBe("/time");
  });

  it("reads the week out of either entity id shape", () => {
    expect(attentionWeek("2026-08-31")).toBe("2026-08-31");
    expect(attentionWeek("3f1b0c2e-0000-4000-8000-000000000001/2026-09-07")).toBe("2026-09-07");
  });

  /**
   * A week start is a plain calendar date, and `formatters.formatDate` renders
   * it in whatever zone the reader's browser is in. This stands in for a
   * reader west of Greenwich: the zone the title is asked for wins unless the
   * caller names one, so a title formatted without `timeZone: "UTC"` would say
   * 30 August while the link opened the week of the 31st.
   */
  const formatInLosAngeles = (value: string, options: Intl.DateTimeFormatOptions) =>
    new Intl.DateTimeFormat("en-GB", { timeZone: "America/Los_Angeles", ...options }).format(new Date(value));

  it("names the same week the link opens, whichever side of Greenwich the reader is on", () => {
    const item = {
      module: "time" as const,
      type: "weekUnsubmitted",
      entityId: "2026-08-31",
      title: "Your week of 2026-08-31 is not submitted",
    };

    const title = attentionTitle(item, (key, values) => `${key}:${values?.date}`, formatInLosAngeles);

    expect(title).toBe("dashboard.timeWeekUnsubmitted:31 Aug 2026");
    expect(title).not.toContain("30 Aug");
  });

  it("leaves every other module's title exactly as the server wrote it", () => {
    const item = { module: "projects" as const, type: "projectOverdue", entityId: "31", title: "ACME1000 is overdue" };

    expect(attentionTitle(item, () => "never", formatInLosAngeles)).toBe("ACME1000 is overdue");
  });

  it("has both titles in English and Norwegian", () => {
    for (const key of ["dashboard.timeWeekUnsubmitted", "dashboard.timeApprovalWaiting"]) {
      for (const lng of ["en", "nb"]) {
        expect(i18n.t(key, { ns: "host", lng, date: "2026-08-31" })).not.toBe(key);
        expect(i18n.t(key, { ns: "host", lng, date: "2026-08-31" })).toContain("2026-08-31");
      }
    }
  });

  // The four economy signals carry the project's or the milestone's own name
  // in `title` (the server's field for it, English or not), and the host
  // builds a full sentence from a catalog key, the way it does for time.
  it("names the four economy signals from a catalog, carrying the project's or milestone's own name", () => {
    expect(attentionTitleKey({ module: "projects", type: "budgetWarning" })).toBe("dashboard.projectBudgetWarning");
    expect(attentionTitleKey({ module: "projects", type: "budgetExceeded" })).toBe("dashboard.projectBudgetExceeded");
    expect(attentionTitleKey({ module: "projects", type: "milestoneReady" })).toBe("dashboard.projectMilestoneReady");
    expect(attentionTitleKey({ module: "projects", type: "milestoneOverdue" })).toBe(
      "dashboard.projectMilestoneOverdue",
    );

    const t = (key: string, values?: Record<string, unknown>) => `${key}:${values?.name}`;
    expect(
      attentionTitle(
        { module: "projects", type: "budgetWarning", entityId: "31", title: "Roof replacement" },
        t,
        formatInLosAngeles,
      ),
    ).toBe("dashboard.projectBudgetWarning:Roof replacement");
    expect(
      attentionTitle(
        { module: "projects", type: "milestoneReady", entityId: "31/2001", title: "Foundation poured" },
        t,
        formatInLosAngeles,
      ),
    ).toBe("dashboard.projectMilestoneReady:Foundation poured");
  });

  it("has all four economy titles in English and Norwegian", () => {
    for (const key of [
      "dashboard.projectBudgetWarning",
      "dashboard.projectBudgetExceeded",
      "dashboard.projectMilestoneReady",
      "dashboard.projectMilestoneOverdue",
    ]) {
      for (const lng of ["en", "nb"]) {
        expect(i18n.t(key, { ns: "host", lng, name: "Roof replacement" })).not.toBe(key);
        expect(i18n.t(key, { ns: "host", lng, name: "Roof replacement" })).toContain("Roof replacement");
      }
    }
  });

  // The four registry facts a stored Brreg record can surface (Brreg in
  // full design D4): server-built titles carrying the customer's own name,
  // the same treatment as the four project-economy signals above.
  it("names the four registry signals from a catalog, carrying the customer's own name", () => {
    expect(attentionTitleKey({ module: "customers", type: "registryBankrupt" })).toBe(
      "dashboard.customerRegistryBankrupt",
    );
    expect(attentionTitleKey({ module: "customers", type: "registryLiquidation" })).toBe(
      "dashboard.customerRegistryLiquidation",
    );
    expect(attentionTitleKey({ module: "customers", type: "registryDeleted" })).toBe(
      "dashboard.customerRegistryDeleted",
    );
    expect(attentionTitleKey({ module: "customers", type: "registryRenamed" })).toBe(
      "dashboard.customerRegistryRenamed",
    );
    expect(attentionTitleKey({ module: "customers", type: "customerIdle" })).toBeUndefined();

    const t = (key: string, values?: Record<string, unknown>) => `${key}:${values?.name}`;
    expect(
      attentionTitle(
        { module: "customers", type: "registryBankrupt", entityId: "42", title: "Acme AS" },
        t,
        formatInLosAngeles,
      ),
    ).toBe("dashboard.customerRegistryBankrupt:Acme AS");
    expect(
      attentionTitle(
        { module: "customers", type: "registryRenamed", entityId: "42", title: "Acme AS" },
        t,
        formatInLosAngeles,
      ),
    ).toBe("dashboard.customerRegistryRenamed:Acme AS");
  });

  it("leaves an unknown customers item's title exactly as the server wrote it", () => {
    const item = { module: "customers" as const, type: "customerIdle", entityId: "42", title: "Acme AS" };
    expect(attentionTitle(item, () => "never", formatInLosAngeles)).toBe("Acme AS");
  });

  it("has all four registry titles in English and Norwegian", () => {
    for (const key of [
      "dashboard.customerRegistryBankrupt",
      "dashboard.customerRegistryLiquidation",
      "dashboard.customerRegistryDeleted",
      "dashboard.customerRegistryRenamed",
    ]) {
      for (const lng of ["en", "nb"]) {
        expect(i18n.t(key, { ns: "host", lng, name: "Acme AS" })).not.toBe(key);
        expect(i18n.t(key, { ns: "host", lng, name: "Acme AS" })).toContain("Acme AS");
      }
    }
  });

  it("sends a follow-up item to its customer, whose page holds the timeline", () => {
    expect(attentionHref({ module: "customers", type: "followUpOverdue", entityId: "42" })).toBe("/customers/42");
    expect(attentionHref({ module: "customers", type: "followUpDue", entityId: "42" })).toBe("/customers/42");
  });

  it("names the two follow-up signals with the customer's own name", () => {
    expect(attentionTitleKey({ module: "customers", type: "followUpOverdue" })).toBe(
      "dashboard.customerFollowUpOverdue",
    );
    expect(attentionTitleKey({ module: "customers", type: "followUpDue" })).toBe("dashboard.customerFollowUpDue");
    // The local stub `t` this file already uses for the four registry cases
    // (`(key, values) => \`${key}:${values?.name}\``), not the real catalog:
    // what is under test is that the right KEY is looked up with the customer's
    // name, and asserting a translated sentence would make this test fail the
    // day somebody rewords the Norwegian.
    const t = (key: string, values?: Record<string, unknown>) => `${key}:${values?.name}`;
    expect(
      attentionTitle(
        { module: "customers", type: "followUpOverdue", entityId: "42", title: "Alpha Co" },
        t,
        formatInLosAngeles,
      ),
    ).toBe("dashboard.customerFollowUpOverdue:Alpha Co");
    expect(
      attentionTitle(
        { module: "customers", type: "followUpDue", entityId: "42", title: "Alpha Co" },
        t,
        formatInLosAngeles,
      ),
    ).toBe("dashboard.customerFollowUpDue:Alpha Co");
  });

  // Ready milestones are a state, not a delta: most active projects have
  // none, so a standing "0 ready to invoice" would be a permanent fixture
  // rather than something worth reading — the same reasoning as Time's
  // approval hint.
  it("hides the Projects card's ready-milestones hint once there is nothing ready", () => {
    const t = (key: string, values?: Record<string, unknown>) => `${key}:${values?.count}`;
    expect(readyMilestonesHint(0, t)).toBeUndefined();
    expect(readyMilestonesHint(undefined, t)).toBeUndefined();
    expect(readyMilestonesHint(4, t)).toBe("dashboard.readyMilestonesHint:4");
  });

  // Ruling: the Projects card shows the ready-milestones hint when there is
  // something ready — it is the more actionable of the two figures — and
  // otherwise falls back to exactly the "N new projects" hint the card
  // carried before this feature, rather than going silent.
  it("shows the ready-milestones hint when something is ready, and falls back to new projects otherwise", () => {
    const t = (key: string, values?: Record<string, unknown>) => `${key}:${values?.count}`;
    expect(projectsCardHint(4, 2, t)).toBe("dashboard.readyMilestonesHint:4");
    expect(projectsCardHint(0, 2, t)).toBe("dashboard.newProjectsHint:2");
    expect(projectsCardHint(undefined, 2, t)).toBe("dashboard.newProjectsHint:2");
    expect(projectsCardHint(0, undefined, t)).toBe("dashboard.newProjectsHint:0");
    expect(projectsCardHint(undefined, undefined, t)).toBe("dashboard.newProjectsHint:0");
  });

  // Zero is the normal case for most people who hold `time:access` but
  // approve nobody's hours, so a standing "0 waiting" hint would be a
  // permanent fixture rather than useful information.
  it("hides the Time card's approval hint once there is nothing waiting", () => {
    const t = (key: string, values?: Record<string, unknown>) => `${key}:${values?.count}`;
    expect(awaitingApprovalHint(0, t)).toBeUndefined();
    expect(awaitingApprovalHint(undefined, t)).toBeUndefined();
    expect(awaitingApprovalHint(3, t)).toBe("dashboard.awaitingApprovalHint:3");
  });

  // Expenses' three attention types: a rejection is the owner's own, an
  // approval group carries the owner's user id (not the expense's), and the
  // payroll item carries the literal "reimbursements". Anything this build
  // does not know about falls back to the app's own list.
  it("sends each expenses item to the page that answers it, and falls back to the app for an unknown type", () => {
    expect(attentionHref({ module: "expenses", type: "approvalWaiting", entityId: "user-1" })).toBe(
      "/expenses/approvals",
    );
    expect(attentionHref({ module: "expenses", type: "expenseRejected", entityId: "42" })).toBe(
      "/expenses?status=rejected",
    );
    // A rejected *trip* carries `claim/<id>`: the two units number
    // independently, so a bare id would collide with an expense's. The link
    // keys on the prefix, never on parsing the number.
    expect(attentionHref({ module: "expenses", type: "expenseRejected", entityId: "claim/12" })).toBe(
      "/expenses/claims/12",
    );
    // A prefixed id with nothing after it is not addressable; the list is.
    expect(attentionHref({ module: "expenses", type: "expenseRejected", entityId: "claim/" })).toBe(
      "/expenses?status=rejected",
    );
    expect(attentionHref({ module: "expenses", type: "reimbursementWaiting", entityId: "reimbursements" })).toBe(
      "/expenses/reimbursements",
    );
    expect(attentionHref({ module: "expenses", type: "somethingNew", entityId: "x" })).toBe("/expenses");
  });

  // The server's `reimbursementWaiting` title is a deliberately untranslated
  // English fallback built from a count it cannot localise; the host renders
  // it from the type and the count instead of showing that sentence. The
  // approval-waiting title also takes a count when the server sent one.
  it("names expenses' items from a catalog, using the count when the server sent one", () => {
    expect(attentionTitleKey({ module: "expenses", type: "expenseRejected" })).toBe("dashboard.expenseRejected");
    // A rejected *trip* is called a travel claim, not "your expense". The
    // title keys on the same `claim/` prefix the link does.
    expect(attentionTitleKey({ module: "expenses", type: "expenseRejected", entityId: "claim/12" })).toBe(
      "dashboard.claimRejected",
    );
    expect(attentionTitleKey({ module: "expenses", type: "expenseRejected", entityId: "42" })).toBe(
      "dashboard.expenseRejected",
    );
    expect(attentionTitleKey({ module: "expenses", type: "approvalWaiting" })).toBe("dashboard.expenseApprovalWaiting");
    expect(attentionTitleKey({ module: "expenses", type: "approvalWaiting", count: 3 })).toBe(
      "dashboard.expenseApprovalWaitingCount",
    );
    expect(attentionTitleKey({ module: "expenses", type: "reimbursementWaiting" })).toBe(
      "dashboard.expenseReimbursementWaiting",
    );
    expect(attentionTitleKey({ module: "expenses", type: "somethingNew" })).toBeUndefined();

    const t = (key: string, values?: Record<string, unknown>) => `${key}:${values?.name}:${values?.count}`;
    expect(
      attentionTitle(
        { module: "expenses", type: "expenseRejected", entityId: "42", title: "Taxi to the airport" },
        t,
        formatInLosAngeles,
      ),
    ).toBe("dashboard.expenseRejected:Taxi to the airport:undefined");
    expect(
      attentionTitle(
        { module: "expenses", type: "approvalWaiting", entityId: "user-1", title: "Anna Ås", count: 3 },
        t,
        formatInLosAngeles,
      ),
    ).toBe("dashboard.expenseApprovalWaitingCount:Anna Ås:3");
    expect(
      attentionTitle(
        {
          module: "expenses",
          type: "reimbursementWaiting",
          entityId: "reimbursements",
          title: "5 expenses are waiting to be reimbursed",
          count: 5,
        },
        t,
        formatInLosAngeles,
      ),
    ).toBe("dashboard.expenseReimbursementWaiting:5 expenses are waiting to be reimbursed:5");
  });

  it("leaves an unknown expenses item's title exactly as the server wrote it", () => {
    const item = { module: "expenses" as const, type: "somethingNew", entityId: "x", title: "A new kind of thing" };

    expect(attentionTitle(item, () => "never", formatInLosAngeles)).toBe("A new kind of thing");
  });

  it("has every expenses attention title in English and Norwegian", () => {
    for (const key of [
      "dashboard.expenseRejected",
      "dashboard.claimRejected",
      "dashboard.expenseApprovalWaiting",
      "dashboard.expenseApprovalWaitingCount",
      "dashboard.expenseReimbursementWaiting",
    ]) {
      for (const lng of ["en", "nb"]) {
        expect(i18n.t(key, { ns: "host", lng, name: "Anna Ås", count: 3 })).not.toBe(key);
      }
    }
  });

  // The card's primary figure: the first currency the caller is owed in, with
  // "+N more" when there is more than one, and a plain "nothing owed" once
  // loaded with nothing in it — never a currency-mixed sum (design §4).
  it("shows the first currency the caller is owed in, and how many more there are", () => {
    const format = (value: number, currency: string) => `${value} ${currency}`;
    const t = (key: string, values?: Record<string, unknown>) => `${key}:${values?.count}`;

    expect(expensesUnreimbursedValue(undefined, format, t)).toBe("dashboard.nothingOwed:undefined");
    expect(expensesUnreimbursedValue([], format, t)).toBe("dashboard.nothingOwed:undefined");
    expect(expensesUnreimbursedValue([{ currency: "NOK", amount: 500 }], format, t)).toBe("500 NOK");
    expect(
      expensesUnreimbursedValue(
        [
          { currency: "NOK", amount: 500 },
          { currency: "EUR", amount: 20 },
        ],
        format,
        t,
      ),
    ).toBe("500 NOK dashboard.moreCurrencies:1");
  });
});
