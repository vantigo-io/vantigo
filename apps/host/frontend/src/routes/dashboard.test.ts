import { i18n } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import { attentionHref, attentionTitleKey, attentionWeek } from "./dashboard";
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

  it("reads the week out of either entity id shape", () => {
    expect(attentionWeek("2026-08-31")).toBe("2026-08-31");
    expect(attentionWeek("3f1b0c2e-0000-4000-8000-000000000001/2026-09-07")).toBe("2026-09-07");
  });

  it("has both titles in English and Norwegian", () => {
    for (const key of ["dashboard.timeWeekUnsubmitted", "dashboard.timeApprovalWaiting"]) {
      for (const lng of ["en", "nb"]) {
        expect(i18n.t(key, { ns: "host", lng, date: "2026-08-31" })).not.toBe(key);
        expect(i18n.t(key, { ns: "host", lng, date: "2026-08-31" })).toContain("2026-08-31");
      }
    }
  });
});
