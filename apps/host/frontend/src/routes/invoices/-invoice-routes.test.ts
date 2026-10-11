import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { describe, expect, it } from "vitest";
import { routeTree } from "../../routeTree.gen";

/**
 * The route ids the generated tree matches for a path, through the real
 * router. The journal and the settings are static segments beside
 * `/invoices/$invoiceId`, whose parser turns anything not digits into NaN and
 * then a not-found — so if the dynamic route ever won the match, both pages
 * would render "not found" instead. TanStack ranks static segments above
 * dynamic ones; this pins that the tree we ship really does.
 */
const matchedRouteIds = (pathname: string) => {
  const router = createRouter({
    routeTree,
    context: { queryClient: new QueryClient() },
    history: createMemoryHistory({ initialEntries: [pathname] }),
  });
  return router.matchRoutes(pathname, {}).map((match) => match.routeId);
};

describe("the invoices route tree", () => {
  it.each(["/invoices/journal", "/invoices/settings"])("matches %s on its static route, not a document's", (path) => {
    const ids = matchedRouteIds(path);

    expect(ids).toContain(path);
    expect(ids).not.toContain("/invoices/$invoiceId");
  });

  // The Payments area and a bank file's page are static segments beside a
  // document's id as well.
  it.each([
    ["/invoices/payments", "/invoices/payments/"],
    ["/invoices/payments/files/1001", "/invoices/payments/files/$bankFileId"],
  ])("matches %s on the Payments route, not a document's", (path, routeId) => {
    const ids = matchedRouteIds(path);

    expect(ids).toContain(routeId);
    expect(ids).not.toContain("/invoices/$invoiceId");
  });

  // So are the Overdue area, a run's page and the paper letters.
  it.each([
    ["/invoices/overdue", "/invoices/overdue"],
    ["/invoices/reminder-runs/11", "/invoices/reminder-runs/$runId"],
    ["/invoices/reminders/print", "/invoices/reminders/print"],
  ])("matches %s on the Overdue area's route, not a document's", (path, routeId) => {
    const ids = matchedRouteIds(path);

    expect(ids).toContain(routeId);
    expect(ids).not.toContain("/invoices/$invoiceId");
  });

  it("still matches a document id on the dynamic route", () => {
    expect(matchedRouteIds("/invoices/1001")).toContain("/invoices/$invoiceId");
  });
});
