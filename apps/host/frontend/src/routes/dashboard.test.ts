import { i18n } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import { attentionHref, attentionTitle, attentionTitleKey, attentionWeek, awaitingApprovalHint } from "./dashboard";
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
