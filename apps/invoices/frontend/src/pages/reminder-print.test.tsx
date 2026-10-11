import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PrintBatchLeftOut, PrintBatchWaiver } from "../api/overdue";
import { invoicesCatalog } from "../i18n";
import { jsonResponse, refusal, sent } from "../test/api";
import { pageOf, printBatch } from "../test/fixtures";
import { overdueServer } from "../test/overdue-server";
import { renderRoute } from "../test/route-tree";

const BATCHES = "/api/v1/invoices/reminder-print-batches";

/** Every value of a contract enum, so a value the catalog lacks words for fails here. */
const LEFT_OUT: Record<PrintBatchLeftOut["reason"], true> = {
  not_awaiting_print: true,
  print_batch_closed: true,
  collection_rates_outdated: true,
  collection_regime_unreviewed: true,
  settled: true,
  on_hold: true,
  handed_off: true,
  policy_none: true,
  customer_anonymised: true,
  action_changed: true,
};
const WAIVED: Record<PrintBatchWaiver["reason"], true> = {
  settled: true,
  on_hold: true,
  handed_off: true,
  policy_none: true,
  customer_anonymised: true,
  charges_barred: true,
  action_changed: true,
};

// jsdom has no object URLs: the batch's PDF makes and revokes one.
const { createObjectURL, revokeObjectURL } = URL;
beforeEach(() => {
  URL.createObjectURL = vi.fn(() => "blob:file");
  URL.revokeObjectURL = vi.fn();
});
afterEach(() => {
  URL.createObjectURL = createObjectURL;
  URL.revokeObjectURL = revokeObjectURL;
});

/** The batches list answering one batch. */
const batches = (batch = printBatch()) => ({ [`GET ${BATCHES}`]: jsonResponse(200, pageOf([batch])) });

describe("ReminderPrintPage_BatchDownloadPostedReprint", () => {
  it("prints the chosen letters for a posting day, names those left out, and downloads the batch", async () => {
    const clicked: HTMLAnchorElement[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this);
    });
    const fetchMock = overdueServer({
      answers: {
        [`POST ${BATCHES}`]: jsonResponse(201, {
          batch: printBatch({ postOn: "2026-09-14" }),
          pdfUrl: `${BATCHES}/7/pdf`,
          leftOut: [
            {
              reminderId: 3203,
              invoiceId: 1008,
              reason: "collection_rates_outdated",
              outdated: { kind: "late_interest_percent", halfYear: "2026-H2" },
            },
          ],
        }),
        [`GET ${BATCHES}/7/pdf`]: () =>
          new Response("%PDF-1.7", {
            status: 200,
            headers: { "Content-Type": "application/pdf", "Content-Disposition": 'attachment; filename="bunke-7.pdf"' },
          }),
      },
    });
    renderRoute("/invoices/reminders/print");

    const awaiting = await screen.findByTestId("awaiting-print");
    expect(await within(awaiting).findByTestId("awaiting-3202")).toHaveTextContent("Letter 2");
    expect(within(awaiting).getByTestId("awaiting-3203")).toHaveTextContent("Letter 1");
    expect(within(awaiting).getByRole("link", { name: "Open the invoice of Letter 2" })).toHaveAttribute(
      "href",
      "/invoices/1007",
    );
    // The posting day: today or one of the next seven, never a later one.
    await userEvent.click(within(awaiting).getByRole("combobox", { name: "Posting day" }));
    expect(await screen.findByRole("option", { name: "Sep 19, 2026" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "Sep 20, 2026" })).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "Sep 11, 2026" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("option", { name: "Sep 14, 2026" }));

    await userEvent.click(within(awaiting).getByRole("button", { name: "Print 2 letters" }));
    const result = await screen.findByTestId("print-result");
    expect(sent(fetchMock, "POST").body).toEqual({ reminderIds: [3202, 3203], postOn: "2026-09-14" });
    expect(within(result).getByText("Batch 7: 1 letter printed for Sep 14, 2026.")).toBeInTheDocument();
    expect(
      within(result).getByText(
        "Letter 1: it needs the late interest rate for 2026-H2, which has none, so it stays awaiting print until the rate is added.",
      ),
    ).toBeInTheDocument();

    await userEvent.click(within(result).getByRole("button", { name: "Download batch 7" }));
    await waitFor(() => expect(clicked).toHaveLength(1));
    expect(clicked[0].download).toBe("bunke-7.pdf");
  });

  it("lets a letter be left out of the batch", async () => {
    const fetchMock = overdueServer({
      answers: {
        [`POST ${BATCHES}`]: jsonResponse(201, { batch: printBatch(), pdfUrl: `${BATCHES}/7/pdf`, leftOut: [] }),
      },
    });
    renderRoute("/invoices/reminders/print");

    const awaiting = await screen.findByTestId("awaiting-print");
    await userEvent.click(await within(awaiting).findByRole("checkbox", { name: "Print Letter 1 of document 1008" }));
    await userEvent.click(within(awaiting).getByRole("button", { name: "Print 1 letter" }));
    await screen.findByTestId("print-result");
    expect(sent(fetchMock, "POST").body).toEqual({ reminderIds: [3202], postOn: "2026-09-12" });
  });

  it("confirms a batch posted on its day, naming the charges waived and the letters skipped", async () => {
    const fetchMock = overdueServer({
      answers: {
        ...batches(),
        [`POST ${BATCHES}/7/posted`]: jsonResponse(200, {
          batch: printBatch({ postedOn: "2026-09-12", postedAt: "2026-09-12T15:00:00Z", postedBy: "x" }),
          skipped: [3204],
          waived: [{ reminderId: 3202, invoiceId: 1007, kinds: ["fee"], reason: "settled" }],
        }),
      },
    });
    renderRoute("/invoices/reminders/print");

    const list = await screen.findByTestId("print-batches");
    const row = await within(list).findByTestId("batch-7");
    expect(within(row).getByText("Printed, not confirmed posted")).toBeInTheDocument();
    expect(within(row).getByText("Sep 12, 2026")).toBeInTheDocument();
    await userEvent.click(within(row).getByRole("button", { name: "Confirm batch 7 posted" }));
    const dialog = await screen.findByRole("dialog", { name: "Confirm batch 7 posted" });
    // The days it may have gone, today the latest: a later day is never offered.
    expect(within(dialog).getByRole("radio", { name: "Sep 12, 2026" })).toBeChecked();
    expect(within(dialog).queryByRole("radio", { name: "Sep 13, 2026" })).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Confirm posted" }));

    const posted = await screen.findByTestId("posted-result");
    expect(sent(fetchMock, "POST").body).toEqual({ postedOn: "2026-09-12" });
    expect(within(posted).getByText("Batch 7 is confirmed posted: its letters are sent.")).toBeInTheDocument();
    expect(
      within(posted).getByText("Letter 2: the fee waived as claimed in error — nothing was left to pay."),
    ).toBeInTheDocument();
    expect(within(posted).getByText("Withdrawn since printing, so not sent: letter id 3204.")).toBeInTheDocument();
  });

  it.each([
    [
      "late",
      printBatch({ postOn: "2026-09-10", createdAt: "2026-09-08T09:00:00Z" }),
      "Sep 10, 2026",
      "Sep 12, 2026",
      refusal(409, "reminder_posted_late"),
      "This batch was printed for an earlier day; posted now, its letters would give less time than they say. Reprint it for the day it is posted.",
    ],
    [
      "early",
      printBatch({ postOn: "2026-09-11", createdAt: "2026-09-08T09:00:00Z" }),
      "Sep 11, 2026",
      "Sep 10, 2026",
      refusal(409, "reminder_posted_early"),
      "This batch was printed for a later day, and its fees and deadlines were judged for that day. Reprint it for the day it is posted.",
    ],
  ])("says a batch posted %s in words and offers the reprint", async (_case, batch, postOn, day, answer, words) => {
    const fetchMock = overdueServer({
      answers: {
        ...batches(batch),
        [`POST ${BATCHES}/7/posted`]: answer,
        [`POST ${BATCHES}/7/reprint`]: jsonResponse(200, { ...batch, reprintedAt: "2026-09-12T15:00:00Z" }),
      },
    });
    renderRoute("/invoices/reminders/print");

    const row = await within(await screen.findByTestId("print-batches")).findByTestId("batch-7");
    await userEvent.click(within(row).getByRole("button", { name: "Confirm batch 7 posted" }));
    const dialog = await screen.findByRole("dialog", { name: "Confirm batch 7 posted" });
    // Its own posting day is offered first, as the day it most likely went.
    expect(within(dialog).getByRole("radio", { name: postOn })).toBeChecked();
    await userEvent.click(within(dialog).getByRole("radio", { name: day }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Confirm posted" }));

    expect(await within(dialog).findByText(words)).toBeInTheDocument();
    expect(within(dialog).queryByText("The server's English.")).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Reprint" }));
    expect(await screen.findByText("The letters are awaiting print again.")).toBeInTheDocument();
    expect(
      fetchMock.actualCalls.some(([url, init]) => String(url) === `${BATCHES}/7/reprint` && init?.method === "POST"),
    ).toBe(true);
  });

  it("offers no confirmation for a batch to be posted on a later day, only the reprint", async () => {
    overdueServer({ answers: batches(printBatch({ postOn: "2026-09-14" })) });
    renderRoute("/invoices/reminders/print");

    const row = await within(await screen.findByTestId("print-batches")).findByTestId("batch-7");
    expect(within(row).getByText("To be posted on Sep 14, 2026; confirm it that day.")).toBeInTheDocument();
    expect(within(row).queryByRole("button", { name: "Confirm batch 7 posted" })).not.toBeInTheDocument();
    expect(within(row).getByRole("button", { name: "Reprint batch 7" })).toBeInTheDocument();
  });

  it("shows a posted and a reprinted batch as closed", async () => {
    overdueServer({
      answers: {
        [`GET ${BATCHES}`]: jsonResponse(
          200,
          pageOf([
            printBatch({ postedOn: "2026-09-12", postedAt: "2026-09-12T15:00:00Z", postedBy: "x" }),
            printBatch({ id: 6, reprintedAt: "2026-09-11T15:00:00Z" }),
          ]),
        ),
      },
    });
    renderRoute("/invoices/reminders/print");

    const posted = await within(await screen.findByTestId("print-batches")).findByTestId("batch-7");
    expect(within(posted).getByText("Posted on Sep 12, 2026")).toBeInTheDocument();
    expect(within(posted).queryByRole("button", { name: "Reprint batch 7" })).not.toBeInTheDocument();
    const reprinted = screen.getByTestId("batch-6");
    expect(within(reprinted).getByText("Reprinted")).toBeInTheDocument();
    expect(within(reprinted).queryByRole("button", { name: "Confirm batch 6 posted" })).not.toBeInTheDocument();
  });

  it.each([
    ...Object.keys(LEFT_OUT).map((r) => `print.leftOut.${r}`),
    ...Object.keys(WAIVED).map((r) => `print.waiver.${r}`),
    ...["open", "posted", "reprinted"].map((s) => `print.state.${s}`),
  ])("has words for %s in both catalogs", (key) => {
    expect(invoicesCatalog.en).toHaveProperty([key]);
    expect(invoicesCatalog.nb).toHaveProperty([key]);
  });
});
