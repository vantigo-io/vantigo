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
  it("refuses the ready-to-invoice list with the access layer's own 403", async () => {
    // What a line bills is the project's money, so `toInvoice=true` is for the
    // same callers the summary is. The refusal is the access layer's one
    // uniform answer — `apicommon.ForbiddenBody()`, the `AuthErrorResponse` of
    // openapi/common.yaml — not a ProblemDetails, and it names no field.
    stubExpensesApi({
      entries: [outlay({ project: BOOKED, status: "approved", billable: true, billAmount: 800 })],
      projectSummary: new Response(null, { status: 404 }),
    });

    await expect(runQuery(projectExpensesSummaryQueryOptions(PROJECT))).rejects.toThrow();
    const refused = (await runQuery(expensesQueryOptions({ projectId: PROJECT, toInvoice: true })).catch(
      (error: ApiError) => error,
    )) as ApiError;
    expect(refused.status).toBe(403);
    expect(refused.code).toBe("forbidden");
    expect(refused.message).toBe("You do not have permission to access this resource.");
    expect(refused.fields).toBeUndefined();

    // The same list without the filter is nobody's business but the ordinary
    // visibility rule's, and is answered.
    await expect(runQuery(expensesQueryOptions({ projectId: PROJECT }))).resolves.toBeDefined();
  });

  it("validates the query before it asks who is asking", async () => {
    // The server runs `validateListParams` first and only then consults the
    // directory, so a contradictory query from a caller who would be refused
    // is a **400** about the query — not a 403 about the caller.
    stubExpensesApi({ entries: [], projectSummary: new Response(null, { status: 404 }) });

    const refused = (await runQuery(expensesQueryOptions({ toInvoice: true })).catch(
      (error: ApiError) => error,
    )) as ApiError;
    expect(refused.status).toBe(400);
  });

  it("pages an empty list the way the server does: no pages, and page 0 refused", async () => {
    // `apicommon.TotalPages` is the bare ceiling division, so nothing recorded
    // is `totalPages: 0` — and `validatePageParams` refuses any page below 1.
    // A fake that rounded up to 1 and accepted page 0 hid a clamp that turned
    // every empty list into a red error.
    stubExpensesApi({ entries: [] });

    const empty = (await runQuery(expensesQueryOptions({ projectId: PROJECT }))) as {
      pagination: { totalPages: number; totalCount: number; hasPreviousPage: boolean };
    };
    expect(empty.pagination.totalPages).toBe(0);
    expect(empty.pagination.totalCount).toBe(0);
    expect(empty.pagination.hasPreviousPage).toBe(false);

    const refused = await runQuery(expensesQueryOptions({ projectId: PROJECT, page: 0 })).catch(
      (error: ApiError) => error,
    );
    expect((refused as ApiError).status).toBe(400);
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
