import { i18n } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import {
  attentionHref,
  attentionTitle,
  attentionTitleKey,
  attentionWeek,
  awaitingApprovalHint,
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

  // Zero is the normal case for most people who hold `time:access` but
  // approve nobody's hours, so a standing "0 waiting" hint would be a
  // permanent fixture rather than useful information.
  it("hides the Time card's approval hint once there is nothing waiting", () => {
    const t = (key: string, values?: Record<string, unknown>) => `${key}:${values?.count}`;
    expect(awaitingApprovalHint(0, t)).toBeUndefined();
    expect(awaitingApprovalHint(undefined, t)).toBeUndefined();
    expect(awaitingApprovalHint(3, t)).toBe("dashboard.awaitingApprovalHint:3");
  });
});
