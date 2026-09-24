import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { saveCsv } from "../api/import-export";
import { CustomerImportModal } from "./-customer-import-modal";

vi.mock("../api/import-export", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/import-export")>()),
  saveCsv: vi.fn(),
}));

const FILE_TEXT =
  "\ufeffcustomerNumber;name;email\r\n;Ny Kunde AS;post@ny.no\r\n1001;Gammel AS;nope\r\n;;\r\n;Tredje AS;\r\n";
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

const stubImport = (answers: { dry: Response; real?: Response }) => {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.startsWith("/api/v1/customers/import?dryRun=true")) return answers.dry.clone();
    if (url.startsWith("/api/v1/customers/import?dryRun=false") && answers.real) return answers.real.clone();
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

const chooseFile = async () =>
  userEvent.upload(screen.getByLabelText("Choose CSV file"), new File([FILE_TEXT], "kunder.csv", { type: "text/csv" }));

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

    expect(
      await screen.findByText("3 rows — 2 would be created, 0 would be updated, 1 have errors"),
    ).toBeInTheDocument();
    const errorRow = screen.getByText(EMAIL_ERROR).closest("tr") as HTMLElement;
    expect(within(errorRow).getByText("2")).toBeInTheDocument();
    expect(within(errorRow).getByText("email")).toBeInTheDocument();
    const dryCall = fetchMock.mock.calls.find(([input]) => String(input).includes("dryRun=true"));
    expect(String(dryCall?.[0])).toContain("allowDuplicateIdentity=false");
    expect(invalidate).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "Import" }));
    expect(await screen.findByText("3 rows — 2 created, 0 updated, 1 failed")).toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([input]) => String(input).includes("dryRun=false"))).toHaveLength(1);
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["customers"] });

    await userEvent.click(screen.getByRole("button", { name: "Download failed rows" }));
    await waitFor(() => expect(saveCsv).toHaveBeenCalled());
    const [{ blob, fileName }] = vi.mocked(saveCsv).mock.calls[0];
    expect(fileName).toBe("kunder-failed-rows.csv");
    // Blob.text() strips a leading BOM; the file must keep it, so the bytes are decoded with it.
    const text = new TextDecoder("utf-8", { ignoreBOM: true }).decode(await blob.arrayBuffer());
    expect(text).toBe(`\ufeffcustomerNumber;name;email;error\r\n1001;Gammel AS;nope;email: ${EMAIL_ERROR}\r\n`);
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
    await screen.findByText("3 rows — 2 would be created, 0 would be updated, 1 have errors");
    await userEvent.click(screen.getByRole("button", { name: "Import" }));

    expect(await screen.findByText("The file could not be imported")).toBeInTheDocument();
    // Rows before the failure may have been committed: the list is refreshed,
    // and Import cannot be clicked again on the stale check.
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["customers"] }));
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  });

  it("sends allowDuplicateIdentity when the box is ticked", async () => {
    const fetchMock = stubImport({ dry: json(checked) });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByLabelText("Allow a customer to share a legal identity with another customer"));
    await userEvent.click(screen.getByRole("button", { name: "Check" }));

    await screen.findByText("3 rows — 2 would be created, 0 would be updated, 1 have errors");
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
});
