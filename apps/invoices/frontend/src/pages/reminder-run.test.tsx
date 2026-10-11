import { cleanup, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { jsonResponse } from "../test/api";
import { reminderRun, reminderRunDetail } from "../test/fixtures";
import { overdueServer } from "../test/overdue-server";
import { renderRoute } from "../test/route-tree";

describe("a reminder run's page", () => {
  it("ReminderRunPage_LettersAndCounts", async () => {
    overdueServer();
    renderRoute("/invoices/reminder-runs/11");

    expect(await screen.findByRole("heading", { name: "Reminder run 11" })).toBeInTheDocument();
    expect(await screen.findByText("Made on Sep 12, 2026 by you.")).toBeInTheDocument();
    expect(screen.getByText("2 letters made, 1 skipped.")).toBeInTheDocument();
    expect(screen.getByText("Bank data booked up to Sep 11, 2026.")).toBeInTheDocument();
    const counts = screen.getByTestId("run-counts");
    expect(within(counts).getByText("Sent: 1")).toBeInTheDocument();
    expect(within(counts).getByText("Awaiting print: 1")).toBeInTheDocument();

    const letters = screen.getByTestId("run-letters");
    const first = within(letters).getByTestId("run-letter-3201");
    expect(within(first).getByText("Letter 1")).toBeInTheDocument();
    expect(within(first).getByText("Sent")).toBeInTheDocument();
    expect(within(first).getByText("purring@acme.no")).toBeInTheDocument();
    expect(within(first).getByRole("link", { name: "Open the invoice of Letter 1" })).toHaveAttribute(
      "href",
      "/invoices/1001",
    );
    const second = within(letters).getByTestId("run-letter-3202");
    expect(within(second).getByText("Awaiting print")).toBeInTheDocument();
    expect(within(second).getByText("Paper")).toBeInTheDocument();
    expect(within(second).getByRole("link", { name: "Open the invoice of Letter 2" })).toHaveAttribute(
      "href",
      "/invoices/1007",
    );
    // A letter not yet sent may be withdrawn; a sent one may not.
    expect(within(second).getByRole("button", { name: "Withdraw Letter 2" })).toBeInTheDocument();
    expect(within(first).queryByRole("button", { name: "Withdraw Letter 1" })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Print the letters awaiting print" })).toHaveAttribute(
      "href",
      "/invoices/reminders/print",
    );
  });

  it("says a run that stopped half-way, and one confirmed on stale bank data", async () => {
    overdueServer({
      answers: {
        "GET /api/v1/invoices/reminder-runs/11": jsonResponse(200, {
          ...reminderRunDetail(),
          run: reminderRun({
            letters: undefined,
            skipped: undefined,
            lastBookedOn: undefined,
            staleImportAcknowledged: true,
          }),
        }),
      },
    });
    renderRoute("/invoices/reminder-runs/11");

    expect(
      await screen.findByText("The run stopped before it finished; the letters it made are below, its skips unknown."),
    ).toBeInTheDocument();
    expect(screen.getByText("No bank file had been imported.")).toBeInTheDocument();
    expect(screen.getByText("Made with charges on old bank data, confirmed.")).toBeInTheDocument();
  });

  // The run's and the paper letters' pages fall under the nav's /invoices
  // rule, invoices:access: without invoices:payments they say so, and never
  // ask for what would be a 403.
  it.each(["/invoices/reminder-runs/11", "/invoices/reminders/print"])(
    "RunAndPrintPages_GatedOnCanRunReminders %s",
    async (url) => {
      const fetchMock = overdueServer({ capabilities: { canRunReminders: false } });
      renderRoute(url);

      expect(await screen.findByText("Not allowed")).toBeInTheDocument();
      expect(
        screen.getByText("Reminder runs and paper letters need the invoices:payments permission."),
      ).toBeInTheDocument();
      expect(fetchMock.actualCalls.map(([called]) => String(called))).toEqual(["/api/v1/invoices/meta"]);

      cleanup();
      overdueServer();
      renderRoute(url);
      expect((await screen.findAllByRole("table")).length).toBeGreaterThan(0);
      expect(screen.queryByText("Not allowed")).not.toBeInTheDocument();
    },
  );
});
