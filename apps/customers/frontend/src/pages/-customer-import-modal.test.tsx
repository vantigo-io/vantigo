import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { saveCsv } from "../api/import-export";
import { CustomerImportModal } from "./-customer-import-modal";

vi.mock("../api/import-export", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/import-export")>()),
  saveCsv: vi.fn(),
}));

// The all-blank record sits before the failing one: it takes no row number, so
// row 2 is `1001;…` only if the browser numbers rows exactly as the server does.
const FILE_TEXT =
  "\ufeffcustomerNumber;name;email\r\n;Ny Kunde AS;post@ny.no\r\n;;\r\n1001;Gammel AS;nope\r\n;Tredje AS;\r\n";
const EMAIL_ERROR = "An email address must look like name@example.com, but was 'nope'";

const checked = {
  dryRun: true,
  rows: 3,
  created: 2,
  updated: 0,
  failed: 1,
  errors: [{ row: 2, column: "email", message: EMAIL_ERROR }],
};
const imported = { ...checked, dryRun: false };
// Literally the server's body: the row-level error carries no column key.
const nothingImportable = {
  dryRun: true,
  rows: 1,
  created: 0,
  updated: 0,
  failed: 1,
  errors: [{ row: 1, message: "This row has 2 cells, but the header has 3" }],
};

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status === 200 ? "application/json" : "application/problem+json" },
  });

/** `real: "pending"` is a real run that never answers. */
const stubImport = (answers: { dry: Response; real?: Response | "pending" }) => {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.startsWith("/api/v1/customers/import?dryRun=true")) return answers.dry.clone();
    if (url.startsWith("/api/v1/customers/import?dryRun=false") && answers.real) {
      return answers.real === "pending" ? new Promise<Response>(() => {}) : answers.real.clone();
    }
    if (url.startsWith("/api/v1/customers/import/template")) {
      return new Response("\ufeffcustomerNumber;name\r\n", {
        status: 200,
        headers: { "Content-Disposition": 'attachment; filename="customers-import-template.csv"' },
      });
    }
    return new Response(null, { status: 404 });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
};

const renderModal = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const invalidate = vi.spyOn(queryClient, "invalidateQueries");
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <CustomerImportModal opened onClose={() => {}} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { invalidate };
};

/** The figure shown under `label`: the counts are labelled numbers, not a sentence. */
const figure = (label: string) => screen.getByText(label).nextElementSibling;
const checkShown = () => screen.findByText("Would be created");
const importShown = () => screen.findByText("Created");

const problem = (status: number, title: string, detail: string) =>
  new Response(JSON.stringify({ title, status, detail }), {
    status,
    headers: { "Content-Type": "application/problem+json" },
  });
// Literally the server's body for a second import while one runs.
const alreadyRunning = () =>
  problem(409, "An import is already running", "Another customer import is running. Try again once it has finished.");

const chooseFile = async (file = new File([FILE_TEXT], "kunder.csv", { type: "text/csv" })) =>
  userEvent.upload(screen.getByLabelText("Choose CSV file"), file);

/** The page's shape: the modal stays mounted, and only `opened` goes back and forth. */
const Reopenable = () => {
  const [opened, setOpened] = useState(true);
  return (
    <>
      <button type="button" onClick={() => setOpened(true)}>
        Reopen
      </button>
      <CustomerImportModal opened={opened} onClose={() => setOpened(false)} />
    </>
  );
};

describe("CustomerImportModal", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.mocked(saveCsv).mockReset();
  });

  it("checks the file, imports it, and hands back the failed rows with an error column", async () => {
    const fetchMock = stubImport({ dry: json(checked), real: json(imported) });
    const { invalidate } = renderModal();

    await chooseFile();
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));

    await checkShown();
    expect(figure("Rows")).toHaveTextContent("3");
    expect(figure("Would be created")).toHaveTextContent("2");
    expect(figure("Would be updated")).toHaveTextContent("0");
    expect(figure("Have errors")).toHaveTextContent("1");
    // The result is announced, and the table says what it lists.
    expect(screen.getByRole("status")).toHaveTextContent("Would be created2");
    const errorRow = within(screen.getByRole("table", { name: "Problems in the file, by row" }))
      .getByText(EMAIL_ERROR)
      .closest("tr") as HTMLElement;
    expect(within(errorRow).getByText("2")).toBeInTheDocument();
    expect(within(errorRow).getByText("email")).toBeInTheDocument();
    const dryCall = fetchMock.mock.calls.find(([input]) => String(input).includes("dryRun=true"));
    expect(String(dryCall?.[0])).toContain("allowDuplicateIdentity=false");
    expect(invalidate).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "Import" }));
    await importShown();
    expect(figure("Created")).toHaveTextContent("2");
    expect(figure("Failed")).toHaveTextContent("1");
    expect(fetchMock.mock.calls.filter(([input]) => String(input).includes("dryRun=false"))).toHaveLength(1);
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["customers"] });

    await userEvent.click(screen.getByRole("button", { name: "Download failed rows" }));
    await waitFor(() => expect(saveCsv).toHaveBeenCalled());
    const [{ blob, fileName }] = vi.mocked(saveCsv).mock.calls[0];
    expect(fileName).toBe("kunder-failed-rows.csv");
    // Blob.text() strips a leading BOM; the file must keep it, so the bytes are decoded with it.
    const text = new TextDecoder("utf-8", { ignoreBOM: true }).decode(await blob.arrayBuffer());
    expect(text).toBe(`\ufefferror;customerNumber;name;email\r\nemail: ${EMAIL_ERROR};1001;Gammel AS;nope\r\n`);
  });

  it("keeps Import disabled when no row of the file could be imported, and shows a row-level error without a column", async () => {
    stubImport({ dry: json(nothingImportable) });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));

    expect(
      await screen.findByText("No row in this file can be imported. Fix the rows below and check again."),
    ).toBeInTheDocument();
    const errorRow = screen.getByText("This row has 2 cells, but the header has 3").closest("tr") as HTMLElement;
    expect(within(errorRow).getByText("—")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  });

  it("shows the server's refusal of a national identity number on its legalId row", async () => {
    const refusal = "A Norwegian national identity number is never stored here";
    stubImport({
      dry: json({
        dryRun: true,
        rows: 1,
        created: 0,
        updated: 0,
        failed: 1,
        errors: [{ row: 1, column: "legalId", message: refusal }],
      }),
    });
    renderModal();
    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    await checkShown();
    const row = within(screen.getByRole("table", { name: "Problems in the file, by row" }))
      .getByText(refusal)
      .closest("tr") as HTMLElement;
    expect(within(row).getByText("legalId")).toBeInTheDocument();
  });

  it("shows the server's own reasons when the file itself is refused", async () => {
    stubImport({
      dry: json(
        {
          title: "Invalid import file",
          status: 400,
          errors: { file: ["Unknown columns: 'nmae'. A column is one the export and the template carry."] },
        },
        400,
      ),
    });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));

    expect(await screen.findByText("The file could not be imported")).toBeInTheDocument();
    expect(screen.getByText(/Unknown columns: 'nmae'/)).toBeInTheDocument();
  });

  it("refreshes the list and asks for a new check when a real run fails part-way", async () => {
    stubImport({ dry: json(checked), real: json({ title: "Internal Server Error", status: 500 }, 500) });
    const { invalidate } = renderModal();

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    await checkShown();
    await userEvent.click(screen.getByRole("button", { name: "Import" }));

    expect(await screen.findByText("The file could not be imported")).toBeInTheDocument();
    // A second check of the same file would call the saved rows new again.
    expect(screen.getByText(/Some rows may already have been saved/)).toBeInTheDocument();
    // Rows before the failure may have been committed: the list is refreshed,
    // and Import cannot be clicked again on the stale check.
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["customers"] }));
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  });

  it("drops a check that answers after the modal was closed, and opens again on nothing", async () => {
    let answer: (response: Response) => void = () => {};
    vi.stubGlobal(
      "fetch",
      vi.fn(
        () =>
          new Promise<Response>((resolve) => {
            answer = resolve;
          }),
      ),
    );
    render(
      <MantineProvider env="test">
        <QueryClientProvider client={new QueryClient()}>
          <Reopenable />
        </QueryClientProvider>
      </MantineProvider>,
    );

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    answer(json(checked));
    await userEvent.click(screen.getByRole("button", { name: "Reopen" }));

    await screen.findByRole("dialog");
    expect(screen.queryByText(/would be created/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Check" })).toBeDisabled();
  });

  it("cannot be cancelled while a real run is going, whose result it would lose", async () => {
    stubImport({ dry: json(checked), real: "pending" });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    await checkShown();
    await userEvent.click(screen.getByRole("button", { name: "Import" }));

    await waitFor(() => expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled());
    // Escape is a close too, and must not forget the run either.
    await userEvent.keyboard("{Escape}");
    expect(screen.getByText("Chosen: kunder.csv")).toBeInTheDocument();
  });

  it("builds the failed rows from the bytes it checked, never a second read of the file", async () => {
    const fetchMock = stubImport({ dry: json(checked), real: json(imported) });
    renderModal();
    const file = new File([FILE_TEXT], "kunder.csv", { type: "text/csv" });
    await chooseFile(file);
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    await checkShown();

    // The file on disk changed or went away after the check: Chrome's NotReadableError.
    const gone = () => Promise.reject(new DOMException("The file could not be read", "NotReadableError"));
    Object.assign(file, { text: gone, arrayBuffer: gone, stream: gone });
    await userEvent.click(screen.getByRole("button", { name: "Import" }));
    await importShown();
    await userEvent.click(screen.getByRole("button", { name: "Download failed rows" }));

    await waitFor(() => expect(saveCsv).toHaveBeenCalled());
    const text = new TextDecoder("utf-8", { ignoreBOM: true }).decode(
      await vi.mocked(saveCsv).mock.calls[0][0].blob.arrayBuffer(),
    );
    expect(text).toBe(`\ufefferror;customerNumber;name;email\r\nemail: ${EMAIL_ERROR};1001;Gammel AS;nope\r\n`);
    // And the real run sent what was checked.
    const realCall = fetchMock.mock.calls.find(([input]) => String(input).includes("dryRun=false"));
    const [, init] = realCall as unknown as [string, RequestInit];
    const sent = (init.body as FormData).get("file") as File;
    expect(new TextDecoder("utf-8", { ignoreBOM: true }).decode(await sent.arrayBuffer())).toBe(FILE_TEXT);
  });

  it("refuses a file over the server's 5 MB limit without sending it", async () => {
    const fetchMock = stubImport({ dry: json(checked) });
    renderModal();

    await chooseFile(new File([new Uint8Array(5 * 1024 * 1024 + 1)], "stor.csv", { type: "text/csv" }));

    expect(screen.getByText("stor.csv is larger than 5 MB, the most an import takes.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Check" })).toBeDisabled();
    expect(fetchMock.mock.calls.filter(([input]) => String(input).includes("/customers/import?"))).toHaveLength(0);
  });

  it("sends allowDuplicateIdentity when the box is ticked", async () => {
    const fetchMock = stubImport({ dry: json(checked) });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByLabelText("Allow a customer to share a legal identity with another customer"));
    await userEvent.click(screen.getByRole("button", { name: "Check" }));

    await checkShown();
    const dryCall = fetchMock.mock.calls.find(([input]) => String(input).includes("dryRun=true"));
    expect(String(dryCall?.[0])).toContain("allowDuplicateIdentity=true");
  });

  it("downloads the template under the server's file name", async () => {
    stubImport({ dry: json(checked) });
    renderModal();

    await userEvent.click(screen.getByRole("button", { name: "Download template" }));

    await waitFor(() => expect(saveCsv).toHaveBeenCalled());
    expect(vi.mocked(saveCsv).mock.calls[0][0].fileName).toBe("customers-import-template.csv");
  });

  it("says an import is already running when a check meets one", async () => {
    stubImport({ dry: alreadyRunning() });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));

    expect(await screen.findByText("An import is already running")).toBeInTheDocument();
    expect(screen.getByText("Another customer import is running. Try again once it has finished.")).toBeInTheDocument();
    expect(screen.queryByText("The file could not be imported")).not.toBeInTheDocument();
    expect(screen.queryByText(/Some rows may already have been saved/)).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: "Check" })).toBeEnabled());
  });

  it("keeps the check when Import meets a running import, which saved nothing", async () => {
    stubImport({ dry: json(checked), real: alreadyRunning() });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    await checkShown();
    await userEvent.click(screen.getByRole("button", { name: "Import" }));

    expect(await screen.findByText("An import is already running")).toBeInTheDocument();
    // The 409 comes before any row runs: nothing was saved, and Import can be pressed again once the other ends.
    expect(screen.queryByText(/Some rows may already have been saved/)).not.toBeInTheDocument();
    expect(figure("Would be created")).toHaveTextContent("2");
    await waitFor(() => expect(screen.getByRole("button", { name: "Import" })).toBeEnabled());
  });

  it("keeps the check and shows the server's reason when Import is refused before any row runs", async () => {
    stubImport({
      dry: json(checked),
      real: json(
        {
          title: "Invalid import file",
          status: 400,
          errors: { file: ["The column 'invoiceEmail' needs the customers:billing-manage permission"] },
        },
        400,
      ),
    });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    await checkShown();
    await userEvent.click(screen.getByRole("button", { name: "Import" }));

    expect(await screen.findByText(/needs the customers:billing-manage permission/)).toBeInTheDocument();
    expect(screen.queryByText(/Some rows may already have been saved/)).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: "Import" })).toBeEnabled());
  });

  it("says in its own words that the server could not be reached, and that rows may have been saved", async () => {
    const fetchMock = stubImport({ dry: json(checked) });
    const answer = fetchMock.getMockImplementation() as (input: RequestInfo | URL) => Promise<Response>;
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      if (String(input).includes("dryRun=false")) throw new TypeError("Failed to fetch");
      return answer(input);
    });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    await checkShown();
    await userEvent.click(screen.getByRole("button", { name: "Import" }));

    expect(await screen.findByText("The server could not be reached. Try again.")).toBeInTheDocument();
    expect(screen.queryByText("Failed to fetch")).not.toBeInTheDocument();
    // The request may have reached the server and run before the answer was lost.
    expect(screen.getByText(/Some rows may already have been saved/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  });

  it("calls off a check that a new file, the duplicate box or a close abandons", async () => {
    const signals: AbortSignal[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
        if (init?.signal) signals.push(init.signal);
        return new Promise<Response>(() => {});
      }),
    );
    renderModal();
    const check = async () => {
      await userEvent.click(screen.getByRole("button", { name: "Check" }));
      await waitFor(() => expect(signals.at(-1)?.aborted).toBe(false));
    };

    await chooseFile();
    await check();
    await chooseFile(new File([FILE_TEXT], "andre.csv", { type: "text/csv" }));
    expect(signals).toHaveLength(1);
    expect(signals[0].aborted).toBe(true);

    await check();
    await userEvent.click(screen.getByLabelText("Allow a customer to share a legal identity with another customer"));
    expect(signals).toHaveLength(2);
    expect(signals[1].aborted).toBe(true);

    await check();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(signals).toHaveLength(3);
    expect(signals[2].aborted).toBe(true);
  });

  it("never calls off a real run, and says why it cannot be closed while it goes", async () => {
    const fetchMock = stubImport({ dry: json(checked), real: "pending" });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    await checkShown();
    expect(document.querySelector(".mantine-Modal-close")).not.toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Import" }));

    expect(
      await screen.findByText("Importing — this window can be closed once the import has finished."),
    ).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Importing");
    expect(document.querySelector(".mantine-Modal-close")).toBeNull();
    const realCall = fetchMock.mock.calls.find(([input]) => String(input).includes("dryRun=false"));
    const [, init] = realCall as unknown as [string, RequestInit];
    expect(init.signal).toBeUndefined();
  });

  it("shows the counts as numbers in the reader's own format", async () => {
    stubImport({ dry: json({ ...checked, rows: 5000, created: 4999 }) });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));

    await checkShown();
    expect(figure("Rows")).toHaveTextContent("5,000");
    expect(figure("Would be created")).toHaveTextContent("4,999");
  });

  it("says the template could not be downloaded, not that the file could not be imported", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => problem(500, "Internal Server Error", "Something went wrong")),
    );
    renderModal();

    await userEvent.click(screen.getByRole("button", { name: "Download template" }));

    expect(await screen.findByText("The template could not be downloaded")).toBeInTheDocument();
    expect(screen.queryByText("The file could not be imported")).not.toBeInTheDocument();
  });
});
