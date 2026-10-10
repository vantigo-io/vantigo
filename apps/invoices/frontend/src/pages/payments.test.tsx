import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import { jsonResponse, path, problemResponse, refusal } from "../test/api";
import { bankServer } from "../test/bank-server";
import { requestTo } from "../test/document-server";
import { bankAccount, bankFile, bankImportResult, OTHER_USER_ID, pageOf } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

/** Chooses a file in the upload's input and clicks Import. */
const upload = async (name = "innbetalinger.xml") => {
  await screen.findByRole("heading", { name: "Import a bank file" });
  const input = document.querySelector<HTMLInputElement>('input[type="file"]');
  if (!input) throw new Error("no file input");
  await userEvent.upload(input, new File(["<Document/>"], name, { type: "application/xml" }));
  await userEvent.click(screen.getByRole("button", { name: "Import" }));
};

describe("the Payments area", () => {
  it("PaymentsPage_UploadsAFileAndShowsTheResult", async () => {
    const fetchMock = bankServer({
      answers: { "POST /api/v1/invoices/bank-files": jsonResponse(201, bankImportResult()) },
    });
    renderRoute("/invoices/payments");

    await upload();

    const result = await screen.findByTestId("import-result");
    expect(within(result).getByTestId("count-transactions")).toHaveTextContent("4");
    expect(within(result).getByTestId("count-matched")).toHaveTextContent("3");
    expect(within(result).getByTestId("count-exceptions")).toHaveTextContent("1");
    expect(within(result).getByTestId("count-pending")).toHaveTextContent("0");
    expect(within(result).getByText(/NOK\s?3,750\.00 registered as payments\./)).toBeInTheDocument();
    expect(within(result).getByText(/NOK\s?1,250\.00 waiting in the exception queue\./)).toBeInTheDocument();
    expect(within(result).getByRole("link", { name: "Open the file" })).toHaveAttribute(
      "href",
      "/invoices/payments/files/1001",
    );
    // The one multipart part, named `file`, the browser writing the boundary.
    const call = fetchMock.actualCalls.find(
      ([url, init]) => path(url) === "/api/v1/invoices/bank-files" && init?.method === "POST",
    );
    const body = call?.[1]?.body;
    expect(body).toBeInstanceOf(FormData);
    expect(((body as FormData).get("file") as File).name).toBe("innbetalinger.xml");
    expect(new Headers(call?.[1]?.headers).get("Content-Type")).toBeNull();
  });

  describe("PaymentsPage_EveryUploadRefusalInWords", () => {
    it.each([
      [
        "a file its own rules refuse, with where",
        problemResponse(400, "Invalid file", { file: ["record 7: the control total does not add up"] }),
        [
          /^The file was refused as a whole, and nothing was imported/,
          "Where: record 7: the control total does not add up",
        ],
      ],
      ["a body that is not multipart", problemResponse(400, "Bad request"), [/^The file was refused as a whole/]],
      [
        "an account that is not the seller's",
        refusal(409, "bank_account_unknown"),
        [/names an account that is neither the seller's bank account nor one an issued invoice printed/],
      ],
      [
        "the other format",
        refusal(409, "bank_import_format_mismatch"),
        [
          "This account's bank files are imported in the other format. Nothing was imported; a manager can change the account's format.",
        ],
      ],
      [
        "no object store",
        refusal(503, "storage_unavailable"),
        ["The document store is unavailable, so no bank file can be imported now. Nothing was imported."],
      ],
    ])("says %s in words", async (_case, answer, expected) => {
      bankServer({ answers: { "POST /api/v1/invoices/bank-files": answer } });
      renderRoute("/invoices/payments");

      await upload();

      expect(await screen.findByText("The bank file was not imported")).toBeInTheDocument();
      for (const words of expected) expect(screen.getByText(words)).toBeInTheDocument();
      expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
      expect(screen.queryByTestId("import-result")).not.toBeInTheDocument();
    });

    const duplicate = () =>
      refusal(409, "bank_file_duplicate", {
        bankFileId: 1000,
        uploadedAt: "2026-09-01T10:00:00Z",
        uploadedBy: OTHER_USER_ID,
      });

    it("names the earlier import of a duplicate — its file, its time and its uploader — with a link", async () => {
      bankServer({ answers: { "POST /api/v1/invoices/bank-files": duplicate() } });
      renderRoute("/invoices/payments");

      await upload();

      expect(
        await screen.findByText(
          /^This bank file was imported before, as file 1000 uploaded Sep 1, 2026.*Nothing was imported again\.$/,
        ),
      ).toBeInTheDocument();
      expect(screen.getByText(/That import was uploaded by another user\./)).toBeInTheDocument();
      expect(screen.getByRole("link", { name: "Open file 1000" })).toHaveAttribute(
        "href",
        "/invoices/payments/files/1000",
      );
    });

    it("names the duplicate in Norwegian from the nb catalog", async () => {
      setLanguagePreference("nb");
      try {
        bankServer({ answers: { "POST /api/v1/invoices/bank-files": duplicate() } });
        renderRoute("/invoices/payments");
        await screen.findByRole("heading", { name: "Importer en bankfil" });
        const input = document.querySelector<HTMLInputElement>('input[type="file"]');
        await userEvent.upload(input as HTMLInputElement, new File(["x"], "a.xml"));
        await userEvent.click(screen.getByRole("button", { name: "Importer" }));

        expect(
          await screen.findByText(/^Denne bankfilen er importert før, som fil 1000 lastet opp/),
        ).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "Åpne fil 1000" })).toBeInTheDocument();
      } finally {
        setLanguagePreference("auto");
      }
    });
  });

  it("PaymentsPage_AccountsAndTheFormatChange", async () => {
    // A reader without invoices:manage sees the accounts and their formats, never the change.
    bankServer();
    renderRoute("/invoices/payments");
    const accounts = await screen.findByTestId("bank-accounts");
    expect(await within(accounts).findByText("8601.11.17947")).toBeInTheDocument();
    expect(within(accounts).getByText("camt.054")).toBeInTheDocument();
    expect(within(accounts).queryByRole("button", { name: /^Change the format/ })).not.toBeInTheDocument();
    expect(within(accounts).getByText("Changing an account's format needs invoices:manage.")).toBeInTheDocument();
    cleanup();

    // A manager sees the cutover an earlier change kept, and makes a change with the cutover explained.
    const fetchMock = bankServer({
      capabilities: { canManage: true },
      answers: {
        "GET /api/v1/invoices/bank-accounts": jsonResponse(200, {
          data: [bankAccount({ previousFormat: "ocr", cutoverThrough: "2026-09-10" })],
        }),
        "PUT /api/v1/invoices/bank-accounts/86011117947/format": jsonResponse(200, bankAccount({ format: "ocr" })),
      },
    });
    renderRoute("/invoices/payments");
    const managed = await screen.findByTestId("bank-accounts");
    expect(
      await within(managed).findByText(
        "Switched from OCR giro: a payment booked on or before Sep 10, 2026 is held back as a possible duplicate.",
      ),
    ).toBeInTheDocument();
    await userEvent.click(within(managed).getByRole("button", { name: "Change the format of account 8601.11.17947" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/Vantigo records the cutover/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Change the format" }));

    expect(await screen.findByText("The account's format was changed")).toBeInTheDocument();
    expect(requestTo(fetchMock, "PUT", "/api/v1/invoices/bank-accounts/86011117947/format")).toEqual({ format: "ocr" });
  });

  it("PaymentsPage_MatchTheRest", async () => {
    const fetchMock = bankServer({
      answers: {
        "GET /api/v1/invoices/bank-files?page=1": jsonResponse(
          200,
          pageOf([bankFile({ id: 1002, pending: 2, matched: 1 }), bankFile({ id: 1001 })]),
        ),
        "POST /api/v1/invoices/bank-files/1002/match": jsonResponse(
          200,
          bankImportResult({
            file: bankFile({ id: 1002, pending: 0, matched: 3 }),
            matched: 2,
            matchedAmount: 2500,
            exceptions: 0,
            exceptionsAmount: 0,
            pending: 0,
          }),
        ),
      },
    });
    renderRoute("/invoices/payments");

    const files = await screen.findByTestId("bank-files");
    // Only the file with lines not yet matched offers it.
    const buttons = await within(files).findAllByRole("button", { name: /^Match the rest of file/ });
    expect(buttons.map((b) => b.getAttribute("aria-label"))).toEqual(["Match the rest of file 1002"]);
    await userEvent.click(buttons[0]);

    expect(await screen.findByText("Matched 2, queued 0; 0 not yet matched.")).toBeInTheDocument();
    const result = await screen.findByTestId("import-result");
    // The amount is what this request matched, said so — not the file's.
    expect(within(result).getByText(/^NOK\s?2,500\.00 registered as payments by this matching\.$/)).toBeInTheDocument();
    expect(
      fetchMock.actualCalls.filter(
        ([url, init]) => path(url) === "/api/v1/invoices/bank-files/1002/match" && init?.method === "POST",
      ),
    ).toHaveLength(1);
  });

  it("shows a file's result and its lines on the file's own page, with Match the rest while some are pending", async () => {
    bankServer({
      answers: {
        "GET /api/v1/invoices/bank-files/1001": jsonResponse(200, {
          file: bankFile({
            pending: 1,
            ignored: 2,
            ignoredKinds: { debit: 0, notBooked: 0, cardInformation: 2, zeroAmount: 0 },
          }),
          transactions: [],
        }),
      },
    });
    renderRoute("/invoices/payments/files/1001");

    expect(await screen.findByRole("heading", { name: "Bank file 1001" })).toBeInTheDocument();
    expect(await screen.findByText(/^camt\.054, uploaded .* by you\.$/)).toBeInTheDocument();
    expect(screen.getByText("Accounts: 8601.11.17947")).toBeInTheDocument();
    expect(screen.getByText("Card information: 2")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Match the rest of file 1001" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Back to Payments/ })).toHaveAttribute("href", "/invoices/payments");
  });

  it("filters the queue by unapplied rest, letting the status filter go, and by any file, not only the first page's", async () => {
    const queries: string[] = [];
    const filePage = (page: number) => ({
      ...pageOf([bankFile({ id: page === 1 ? 1001 : 900 })]),
      pagination: {
        page,
        pageSize: 100,
        totalCount: 2,
        totalPages: 2,
        hasNextPage: page === 1,
        hasPreviousPage: page === 2,
      },
    });
    bankServer({
      lines: (query) => {
        queries.push(query.toString());
        return [];
      },
      answers: {
        "GET /api/v1/invoices/bank-files?page=1&pageSize=100": jsonResponse(200, filePage(1)),
        "GET /api/v1/invoices/bank-files?page=2&pageSize=100": jsonResponse(200, filePage(2)),
      },
    });
    renderRoute("/invoices/payments");

    expect(await screen.findByText("No payment matches.")).toBeInTheDocument();
    expect(screen.getByText("Duplicates are listed under the status Duplicate.")).toBeInTheDocument();
    await userEvent.click(within(screen.getByTestId("bank-queue")).getByRole("combobox", { name: "File" }));
    await userEvent.click(await screen.findByRole("option", { name: "File 900" }));
    await waitFor(() => expect(queries).toContain("status=exception&bankFileId=900&page=1"));
    expect(queries[0]).toBe("status=exception&page=1");
    await userEvent.click(screen.getByRole("checkbox", { name: "Only payments with an unapplied rest" }));
    await waitFor(() => expect(queries).toContain("bankFileId=900&unapplied=true&page=1"));
  });

  it("says the File filter offers only the latest files when there are more", async () => {
    // Every page of files full, up to the cap: the filter says it stopped.
    const answers: Record<string, () => Response> = {};
    for (let page = 1; page <= 5; page++) {
      answers[`GET /api/v1/invoices/bank-files?page=${page}&pageSize=100`] = () =>
        jsonResponse(200, {
          ...pageOf([bankFile({ id: 2000 - page })]),
          pagination: {
            page,
            pageSize: 100,
            totalCount: 600,
            totalPages: 6,
            hasNextPage: true,
            hasPreviousPage: page > 1,
          },
        });
    }
    bankServer({ lines: () => [], answers });
    renderRoute("/invoices/payments");

    expect(await screen.findByText("Only the latest 500 files are offered.")).toBeInTheDocument();
  });
});
