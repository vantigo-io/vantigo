import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { meta, stubAccountingApi } from "../test/server";

describe("Set up the books", () => {
  it("sends the start, the fiscal year, the VAT term and the chart's language, then opens the opening balances", async () => {
    const fetchMock = stubAccountingApi({ meta: meta({ setUp: false, settings: null }) });
    const { router } = renderRoute("/accounting/setup");
    const start = await screen.findByLabelText(/The books start on/);
    await userEvent.clear(start);
    await userEvent.type(start, "2026-03-01");
    await userEvent.click(screen.getByRole("button", { name: "Set up the books" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/setup$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/setup$/)[0].body).toEqual({
      booksStart: "2026-03-01",
      fiscalYearStartMonth: 1,
      vatTerm: "bimonthly",
      language: "en",
    });
    await waitFor(() => expect(router.state.location.pathname).toBe("/accounting/opening"));
  });
});
