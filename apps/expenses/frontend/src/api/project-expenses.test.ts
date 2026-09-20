import { describe, expect, it } from "vitest";
import { runQuery } from "../test/api";
import { meta, outlay, PROJECT } from "../test/fixtures";
import { stubExpensesApi } from "../test/server";
import { expensesQueryOptions } from "./entries";
import { projectExpensesSummaryQueryOptions } from "./project-expenses";
import type { ApiError } from "./request";

const BOOKED = { id: PROJECT, code: "KVEM1000", name: "Kverneland web" };

/**
 * The two reads the project page's Expenses tab is built on, against the fake
 * — which is where the server's rules are written down for this package. A
 * panel test can only say what the panel does with an answer; these say the
 * answer is the one the server gives.
 */
describe("a project's expense reads", () => {
  it("refuses the ready-to-invoice list to whoever the totals are refused to", async () => {
    // What a line bills is the project's money, so `toInvoice=true` is for the
    // same callers the summary is: whoever has financial rights on the
    // project. Everyone else is refused outright rather than answered with an
    // empty page, which would read as "nothing is ready".
    stubExpensesApi({
      entries: [outlay({ project: BOOKED, status: "approved", billable: true, billAmount: 800 })],
      projectSummary: new Response(null, { status: 404 }),
    });

    await expect(runQuery(projectExpensesSummaryQueryOptions(PROJECT))).rejects.toThrow();
    const refused = await runQuery(expensesQueryOptions({ projectId: PROJECT, toInvoice: true })).catch(
      (error: ApiError) => error,
    );
    expect((refused as ApiError).status).toBe(403);
    // The same list without the filter is nobody's business but the ordinary
    // visibility rule's, and is answered.
    await expect(runQuery(expensesQueryOptions({ projectId: PROJECT }))).resolves.toBeDefined();
  });

  it("answers one bare 404 for the totals when the installation has no projects module", async () => {
    stubExpensesApi({ entries: [], meta: meta({ projectsAvailable: false }) });

    await expect(runQuery(projectExpensesSummaryQueryOptions(PROJECT))).rejects.toThrow();
  });

  it("counts a billable line with no bill amount as unpriced, whatever the caller may see of it", async () => {
    // `bill_amount IS NULL` never travels: the server renders `billAmount: 0`
    // for it and hides the whole block from a caller who may not see billing.
    // So the figures follow the column, which the fixture states outright.
    stubExpensesApi({
      entries: [
        outlay({ id: 521, project: BOOKED, status: "approved", billable: true, billAmount: null, netAmount: 400 }),
        outlay({ id: 522, project: BOOKED, status: "approved", billable: true, billAmount: 900, netAmount: 500 }),
      ],
      projectCurrency: "NOK",
    });

    const summary = (await runQuery(projectExpensesSummaryQueryOptions(PROJECT))) as {
      currencies: { unpricedCount: number; readyCount: number; readyAmount: number }[];
    };
    expect(summary.currencies[0].unpricedCount).toBe(1);
    expect(summary.currencies[0].readyCount).toBe(1);
    expect(summary.currencies[0].readyAmount).toBe(900);

    const ready = (await runQuery(expensesQueryOptions({ projectId: PROJECT, toInvoice: true }))) as {
      data: { id: number }[];
    };
    expect(ready.data.map((one) => one.id)).toEqual([522]);
  });
});
