import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import type { OverdueAction, OverdueWarning, RunPreviewLetter, RunSkip } from "../api/overdue";
import { invoicesCatalog } from "../i18n";
import { jsonResponse, path, refusal } from "../test/api";
import {
  freshness,
  letterFacts,
  overdueList,
  pageOf,
  previewLetter,
  reminderRun,
  runPreview,
  runResult,
} from "../test/fixtures";
import { overdueServer } from "../test/overdue-server";
import { renderRoute } from "../test/route-tree";

const row = (number: number) => {
  const found = screen
    .getAllByRole("row")
    .find((r) => within(r).queryByRole("link", { name: String(number) }) !== null);
  if (!found) throw new Error(`no row for invoice ${number}`);
  return found;
};

/** Every value of a contract enum, so a value the catalog lacks words for fails here. */
const ACTIONS: Record<OverdueAction | "none", true> = {
  reminder: true,
  collection_notice: true,
  hand_off: true,
  blocked: true,
  waiting: true,
  none: true,
};
const LIST_WARNINGS: Record<OverdueWarning, true> = {
  collection_rates_outdated: true,
  collection_regime_unreviewed: true,
  collection_rate_differs_from_release: true,
  bank_data_stale: true,
  ocr_without_kid_payments: true,
};
const LETTER_WARNINGS: Record<RunPreviewLetter["warnings"][number], true> = {
  reminder_email_missing: true,
  mail_unavailable: true,
};
const SKIPS: Record<RunSkip["reason"], true> = { customer_anonymised: true, action_changed: true };

describe("the Overdue area", () => {
  it("OverduePage_ListFiltersAndFreshness", async () => {
    const queries: string[] = [];
    overdueServer({
      overdue: (query) => {
        queries.push(query.toString());
        return overdueList();
      },
    });
    renderRoute("/invoices/overdue");

    expect(await screen.findByRole("heading", { name: "Overdue" })).toBeInTheDocument();
    expect(await screen.findByText("Bank data booked up to Sep 11, 2026.")).toBeInTheDocument();
    const first = row(1000);
    expect(within(first).getByRole("link", { name: "1000" })).toHaveAttribute("href", "/invoices/1001");
    expect(within(first).getByText("Acme AS")).toBeInTheDocument();
    expect(within(first).getByText("28")).toBeInTheDocument();
    expect(within(first).getByText(/NOK\s?1,250\.00/)).toBeInTheDocument();
    expect(within(first).getByText("Reminder from Aug 30, 2026")).toBeInTheDocument();
    const waiting = row(1003);
    expect(within(waiting).getByText("Waiting until Sep 19, 2026")).toBeInTheDocument();
    expect(
      within(waiting).getByText("The last letter's deadline and the grace days after it have not passed."),
    ).toBeInTheDocument();
    expect(within(waiting).getByText("Letter 1: Sent")).toBeInTheDocument();
    expect(within(waiting).getByText(/NOK\s?35\.00/)).toBeInTheDocument();
    const blocked = row(1004);
    expect(within(blocked).getByText("Blocked")).toBeInTheDocument();
    expect(within(blocked).getByText("On hold")).toBeInTheDocument();
    expect(within(blocked).getByText("Not delivered by the due date")).toBeInTheDocument();
    expect(within(blocked).getByText("Reminded without charges")).toBeInTheDocument();
    // Why a letter would claim less than it might, as the invoice's own card says it (D12).
    expect(
      within(blocked).getByText("No fee, compensation or interest: no delivery by the due date is recorded."),
    ).toBeInTheDocument();
    expect(within(blocked).getByText("The invoice is on hold: the customer disputes it.")).toBeInTheDocument();
    expect(queries[0]).toBe("page=1&pageSize=25");

    // The filters, each sent as the list takes it.
    await userEvent.click(screen.getByRole("combobox", { name: "Next action" }));
    await userEvent.click(await screen.findByRole("option", { name: "Waiting" }));
    await waitFor(() => expect(queries).toContain("action=waiting&page=1&pageSize=25"));
    await userEvent.type(screen.getByRole("textbox", { name: "Due before" }), "Sep 1, 2026");
    await waitFor(() => expect(queries).toContain("dueBefore=2026-09-01&action=waiting&page=1&pageSize=25"));
    await userEvent.click(screen.getByRole("checkbox", { name: "Also paid invoices with charges outstanding" }));
    await waitFor(() =>
      expect(queries).toContain("dueBefore=2026-09-01&action=waiting&charges=outstanding&page=1&pageSize=25"),
    );
  });

  it.each([
    [
      "old",
      freshness({ stale: true, lastBookedOn: "2026-08-30" }),
      /^The latest bank booking imported is from Aug 30, 2026, more than 7 days ago\./,
    ],
    ["never imported", freshness({ stale: true, lastBookedOn: undefined }), /^No bank file was ever imported/],
  ])("says when the bank data is %s", async (_case, fresh, words) => {
    overdueServer({ overdue: () => overdueList({ freshness: fresh, warnings: ["bank_data_stale"] }) });
    renderRoute("/invoices/overdue");

    const banner = await screen.findByTestId("bank-freshness");
    expect(within(banner).getByText("The bank data is old")).toBeInTheDocument();
    expect(within(banner).getByText(words)).toBeInTheDocument();
  });

  it("notes the accounts imported as OCR giro, and every other warning in words", async () => {
    overdueServer({
      overdue: () =>
        overdueList({
          freshness: freshness({ ocrAccounts: ["86011117947"] }),
          warnings: ["ocr_without_kid_payments", "collection_rates_outdated", "collection_regime_unreviewed"],
        }),
    });
    renderRoute("/invoices/overdue");

    expect(
      await screen.findByText(
        "Payments without a KID never reach an OCR giro file (account 8601.11.17947). Register them by hand before a run.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/^A letter in use needs a collection rate for a half-year that has none/),
    ).toBeInTheDocument();
    expect(screen.getByText(/^The collection-law regime's review has lapsed/)).toBeInTheDocument();
  });

  it("says too many overdue invoices in words, asking for a narrower list", async () => {
    overdueServer({ overdue: () => refusal(409, "too_many_overdue") });
    renderRoute("/invoices/overdue");

    expect(
      await screen.findByText("More than 5 000 invoices are overdue. Narrow the list by customer or by due date."),
    ).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("OverduePage_ActionsOnlyForPayments", async () => {
    const fetchMock = overdueServer({ capabilities: { canRunReminders: false } });
    renderRoute("/invoices/overdue");

    await screen.findByRole("link", { name: "1000" });
    expect(screen.queryByRole("button", { name: "Send reminders" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Paper letters" })).not.toBeInTheDocument();
    expect(screen.queryByTestId("reminder-runs")).not.toBeInTheDocument();
    // Nothing of invoices:payments is asked for, so nothing answers 403.
    expect(fetchMock.actualCalls.map(([url]) => String(url).split("?")[0])).not.toContain(
      "/api/v1/invoices/reminder-runs",
    );

    cleanup();
    overdueServer();
    renderRoute("/invoices/overdue");

    expect(await screen.findByRole("button", { name: "Send reminders" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Paper letters" })).toHaveAttribute("href", "/invoices/reminders/print");
    const runs = await screen.findByTestId("reminder-runs");
    expect(await within(runs).findByRole("link", { name: "Run 11" })).toHaveAttribute(
      "href",
      "/invoices/reminder-runs/11",
    );
    expect(within(runs).getByText("2 letters, 1 skipped")).toBeInTheDocument();
  });

  it("previews the run narrowed as the list is, by customer and due date", async () => {
    const bodies: Record<string, unknown>[] = [];
    overdueServer({
      runs: (body) => {
        bodies.push(body);
        return jsonResponse(200, runPreview());
      },
    });
    renderRoute("/invoices/overdue");

    await screen.findByRole("link", { name: "1000" });
    await userEvent.click(screen.getByRole("combobox", { name: "Customer" }));
    await userEvent.click(await screen.findByRole("option", { name: "Acme AS (10001)" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Due before" }), "Sep 1, 2026");
    await userEvent.click(screen.getByRole("button", { name: "Send reminders" }));
    await screen.findByRole("dialog", { name: "Send reminders" });

    await waitFor(() => expect(bodies[0]).toEqual({ dryRun: true, customerId: 2001, dueBefore: "2026-09-01" }));
  });

  it("pages the runs", async () => {
    const pages: string[] = [];
    // The runs answer two pages: the second asked for when chosen.
    const fetchMock = overdueServer({
      answers: {
        "GET /api/v1/invoices/reminder-runs?page=1": () => {
          pages.push("1");
          return jsonResponse(200, {
            ...pageOf([reminderRun()]),
            pagination: {
              page: 1,
              pageSize: 25,
              totalCount: 26,
              totalPages: 2,
              hasNextPage: true,
              hasPreviousPage: false,
            },
          });
        },
        "GET /api/v1/invoices/reminder-runs?page=2": () => {
          pages.push("2");
          return jsonResponse(200, {
            ...pageOf([reminderRun({ id: 3 })]),
            pagination: {
              page: 2,
              pageSize: 25,
              totalCount: 26,
              totalPages: 2,
              hasNextPage: false,
              hasPreviousPage: true,
            },
          });
        },
      },
    });
    renderRoute("/invoices/overdue");

    const runs = await screen.findByTestId("reminder-runs");
    await within(runs).findByRole("link", { name: "Run 11" });
    await userEvent.click(within(runs).getByRole("button", { name: "2" }));
    expect(await within(runs).findByRole("link", { name: "Run 3" })).toBeInTheDocument();
    expect(pages).toEqual(["1", "2"]);
    expect(fetchMock.actualCalls.some(([url]) => path(url) === "/api/v1/invoices/reminder-runs?page=2")).toBe(true);
  });

  describe("RunPreview_DeselectWarnAndAcknowledge", () => {
    it("previews the letters, deselects one, asks for the stale-import confirmation, then runs", async () => {
      const bodies: Record<string, unknown>[] = [];
      overdueServer({
        runs: (body) => {
          bodies.push(body);
          if (body.dryRun)
            return jsonResponse(200, runPreview({ freshness: freshness({ stale: true, lastBookedOn: "2026-08-30" }) }));
          if (!body.acknowledgeStaleImport) return refusal(409, "bank_import_stale", { lastBookedOn: "2026-08-30" });
          return jsonResponse(201, runResult());
        },
      });
      const { router } = renderRoute("/invoices/overdue");

      await userEvent.click(await screen.findByRole("button", { name: "Send reminders" }));
      const dialog = await screen.findByRole("dialog", { name: "Send reminders" });
      expect(bodies[0]).toEqual({ dryRun: true });
      const acme = await within(dialog).findByTestId("preview-letter-1000");
      expect(within(acme).getByText("purring@acme.no")).toBeInTheDocument();
      expect(within(acme).getByText(/NOK\s?1,286\.20/)).toBeInTheDocument();
      expect(within(acme).getByText(/Fee NOK\s?35\.00\./)).toBeInTheDocument();
      expect(within(acme).getByText("Sep 28, 2026")).toBeInTheDocument();
      const kari = within(dialog).getByTestId("preview-letter-1006");
      expect(within(kari).getByText("Paper")).toBeInTheDocument();
      expect(
        within(kari).getByText("The customer wants e-mail and has no reminder address, so it goes on paper."),
      ).toBeInTheDocument();
      expect(within(kari).getByText("No fee: the last letter's deadline was not missed.")).toBeInTheDocument();
      const held = within(dialog).getByTestId("preview-held");
      expect(within(held).getByText("1004")).toBeInTheDocument();
      expect(within(held).getByText("The invoice is on hold: the customer disputes it.")).toBeInTheDocument();
      expect(within(dialog).getByTestId("bank-freshness")).toHaveTextContent(/from Aug 30, 2026/);

      await userEvent.click(within(dialog).getByRole("checkbox", { name: "Send the letter for invoice 1006" }));
      await userEvent.click(within(dialog).getByRole("button", { name: "Send 1 letter" }));
      expect(
        await within(dialog).findByText(
          "The latest bank booking imported is from Aug 30, 2026, and letters of this run claim charges. Import the latest bank file, or confirm the run with the box above.",
        ),
      ).toBeInTheDocument();
      expect(bodies[1]).toEqual({ dryRun: false, items: [{ invoiceId: 1001, action: "reminder" }] });

      await userEvent.click(
        within(dialog).getByRole("checkbox", { name: "The bank data is old: make the run with its charges anyway" }),
      );
      await userEvent.click(within(dialog).getByRole("button", { name: "Send 1 letter" }));
      expect(await within(dialog).findByText("1 letter made.")).toBeInTheDocument();
      expect(bodies[2]).toEqual({
        dryRun: false,
        items: [{ invoiceId: 1001, action: "reminder" }],
        acknowledgeStaleImport: true,
      });
      expect(
        within(dialog).getByText(
          "Invoice 1006: the next step changed since the preview — paid, another letter on its way, put on hold, or now waiting for a rate, the regime review or the confirmation of old bank data.",
        ),
      ).toBeInTheDocument();
      await userEvent.click(within(dialog).getByRole("link", { name: "Open run 11" }));
      await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/reminder-runs/11"));
    });

    it("sends each letter with the action its preview showed", async () => {
      const bodies: Record<string, unknown>[] = [];
      overdueServer({
        runs: (body) => {
          bodies.push(body);
          return body.dryRun
            ? jsonResponse(
                200,
                runPreview({
                  letters: [
                    previewLetter({
                      action: "collection_notice",
                      letter: letterFacts({ level: "collection_notice", feeKind: "none", fee: 0 }),
                    }),
                  ],
                }),
              )
            : jsonResponse(201, runResult());
        },
      });
      renderRoute("/invoices/overdue");

      await userEvent.click(await screen.findByRole("button", { name: "Send reminders" }));
      const dialog = await screen.findByRole("dialog", { name: "Send reminders" });
      expect(await within(dialog).findByText("Debt collection notice")).toBeInTheDocument();
      await userEvent.click(within(dialog).getByRole("button", { name: "Send 1 letter" }));
      await within(dialog).findByTestId("run-result");
      expect(bodies[1]).toEqual({ dryRun: false, items: [{ invoiceId: 1001, action: "collection_notice" }] });
    });

    it("offers no stale-import confirmation on fresh bank data, until the server finds it old", async () => {
      const bodies: Record<string, unknown>[] = [];
      overdueServer({
        runs: (body) => {
          bodies.push(body);
          if (body.dryRun) return jsonResponse(200, runPreview());
          if (!body.acknowledgeStaleImport) return refusal(409, "bank_import_stale", { lastBookedOn: "2026-08-30" });
          return jsonResponse(201, runResult());
        },
      });
      renderRoute("/invoices/overdue");

      await userEvent.click(await screen.findByRole("button", { name: "Send reminders" }));
      const dialog = await screen.findByRole("dialog", { name: "Send reminders" });
      await within(dialog).findByTestId("preview-letter-1000");
      const box = { name: "The bank data is old: make the run with its charges anyway" };
      expect(within(dialog).queryByRole("checkbox", box)).not.toBeInTheDocument();

      await userEvent.click(within(dialog).getByRole("button", { name: "Send 2 letters" }));
      await userEvent.click(await within(dialog).findByRole("checkbox", box));
      await userEvent.click(within(dialog).getByRole("button", { name: "Send 2 letters" }));
      await within(dialog).findByTestId("run-result");
      expect(bodies[2]).toMatchObject({ acknowledgeStaleImport: true });
    });

    it("sends the confirmation only while it is offered: not once the letters with charges are left out", async () => {
      const bodies: Record<string, unknown>[] = [];
      overdueServer({
        runs: (body) => {
          bodies.push(body);
          return body.dryRun
            ? jsonResponse(200, runPreview({ freshness: freshness({ stale: true, lastBookedOn: "2026-08-30" }) }))
            : jsonResponse(201, runResult());
        },
      });
      renderRoute("/invoices/overdue");

      await userEvent.click(await screen.findByRole("button", { name: "Send reminders" }));
      const dialog = await screen.findByRole("dialog", { name: "Send reminders" });
      const box = { name: "The bank data is old: make the run with its charges anyway" };
      await userEvent.click(await within(dialog).findByRole("checkbox", box));
      // Invoice 1000 is the one letter with charges: without it the box goes, and so does the confirmation.
      await userEvent.click(within(dialog).getByRole("checkbox", { name: "Send the letter for invoice 1000" }));
      expect(within(dialog).queryByRole("checkbox", box)).not.toBeInTheDocument();
      await userEvent.click(within(dialog).getByRole("button", { name: "Send 1 letter" }));
      await within(dialog).findByTestId("run-result");
      expect(bodies[1]).toEqual({ dryRun: false, items: [{ invoiceId: 1007, action: "reminder" }] });
    });

    it("holds a run to 500 letters", async () => {
      const letters = Array.from({ length: 501 }, (_, n) =>
        previewLetter({ invoiceId: 5000 + n, number: 4000 + n, letter: letterFacts({ fee: 0, interest: 0 }) }),
      );
      overdueServer({
        runs: (body) =>
          body.dryRun
            ? jsonResponse(200, runPreview({ letters, blockedOrWaiting: [] }))
            : jsonResponse(201, runResult()),
      });
      renderRoute("/invoices/overdue");

      await userEvent.click(await screen.findByRole("button", { name: "Send reminders" }));
      const dialog = await screen.findByRole("dialog", { name: "Send reminders" });
      expect(await within(dialog).findByRole("button", { name: "Send 501 letters" })).toBeDisabled();
      expect(within(dialog).getByText("At most 500 letters go in one run; 501 are chosen.")).toBeInTheDocument();
      await userEvent.click(within(dialog).getByRole("checkbox", { name: "Send the letter for invoice 4000" }));
      expect(within(dialog).getByRole("button", { name: "Send 500 letters" })).toBeEnabled();
    });

    it("reads the overdue list and the runs again after a run", async () => {
      const fetchMock = overdueServer();
      renderRoute("/invoices/overdue");
      await screen.findByTestId("reminder-runs");
      const reads = (p: string) =>
        fetchMock.actualCalls.filter(
          ([url, init]) => path(url).split("?")[0] === p && (init?.method ?? "GET") === "GET",
        ).length;
      await waitFor(() => expect(reads("/api/v1/invoices/reminder-runs")).toBe(1));
      const before = { list: reads("/api/v1/invoices/overdue"), runs: reads("/api/v1/invoices/reminder-runs") };

      await userEvent.click(screen.getByRole("button", { name: "Send reminders" }));
      const dialog = await screen.findByRole("dialog", { name: "Send reminders" });
      await userEvent.click(await within(dialog).findByRole("button", { name: "Send 2 letters" }));
      await within(dialog).findByTestId("run-result");
      await waitFor(() => expect(reads("/api/v1/invoices/overdue")).toBeGreaterThan(before.list));
      await waitFor(() => expect(reads("/api/v1/invoices/reminder-runs")).toBeGreaterThan(before.runs));
    });

    it.each([
      [
        "a missing rate, naming the kind and the half-year",
        refusal(409, "collection_rates_outdated", { kind: "late_interest_percent", halfYear: "2027-H1" }),
        "A letter of this run needs the late interest rate for 2027-H1, and there is none. Add the rate, or wait for the release that brings it. No letter was made.",
      ],
      [
        "the regime unreviewed",
        refusal(409, "collection_regime_unreviewed"),
        "A letter of this run would claim a fee or be a debt collection notice after the collection-law regime was last reviewed. A manager reviews it in the reminder settings. No letter was made.",
      ],
      [
        "reminders switched off",
        refusal(409, "reminders_disabled"),
        "Reminders are switched off in the reminder settings.",
      ],
      [
        "no bank file ever imported",
        refusal(409, "bank_import_stale"),
        "No bank file was ever imported, and letters of this run claim charges. Import the latest bank file, or confirm the run with the box above.",
      ],
    ])("says %s in words", async (_case, answer, words) => {
      overdueServer({ runs: (body) => (body.dryRun ? jsonResponse(200, runPreview()) : answer) });
      renderRoute("/invoices/overdue");

      await userEvent.click(await screen.findByRole("button", { name: "Send reminders" }));
      const dialog = await screen.findByRole("dialog", { name: "Send reminders" });
      await userEvent.click(await within(dialog).findByRole("button", { name: "Send 2 letters" }));
      expect(await within(dialog).findByText(words)).toBeInTheDocument();
      expect(within(dialog).queryByText("The server's English.")).not.toBeInTheDocument();
    });

    it("names a missing rate in Norwegian from the nb catalog", async () => {
      setLanguagePreference("nb");
      try {
        overdueServer({
          runs: (body) =>
            body.dryRun
              ? jsonResponse(200, runPreview())
              : refusal(409, "collection_rates_outdated", { kind: "inkassosats", halfYear: "2027-H1" }),
        });
        renderRoute("/invoices/overdue");

        await userEvent.click(await screen.findByRole("button", { name: "Send purringer" }));
        const dialog = await screen.findByRole("dialog", { name: "Send purringer" });
        await userEvent.click(await within(dialog).findByRole("button", { name: "Send 2 brev" }));
        expect(
          await within(dialog).findByText(
            "Et brev i denne kjøringen trenger inkassosatsen for 2027-H1, og den finnes ikke. Legg inn satsen, eller vent på utgivelsen som bringer den. Ingen brev ble laget.",
          ),
        ).toBeInTheDocument();
      } finally {
        setLanguagePreference("auto");
      }
    });
  });

  it.each([
    ...Object.keys(ACTIONS).map((a) => `overdue.action.${a}`),
    ...Object.keys(LIST_WARNINGS).map((w) => `overdue.warning.${w}`),
    ...Object.keys(LETTER_WARNINGS).map((w) => `run.warning.${w}`),
    ...Object.keys(SKIPS).map((s) => `run.skip.${s}`),
  ])("has words for %s in both catalogs", (key) => {
    expect(invoicesCatalog.en).toHaveProperty([key]);
    expect(invoicesCatalog.nb).toHaveProperty([key]);
  });
});
