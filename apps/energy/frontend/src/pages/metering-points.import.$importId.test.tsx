import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import type { ImportCapabilities } from "./-import-mapping-step";
import { ImportPage } from "./metering-points.import.$importId";

// The whole router module is replaced: the real `Link` needs a router context
// this test has no reason to build.
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
  useNavigate: () => vi.fn(),
}));
vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));
vi.mock("@mantine/modals", () => ({
  modals: { openConfirmModal: ({ onConfirm }: { onConfirm: () => void }) => onConfirm() },
}));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status < 400 ? "application/json" : "application/problem+json" },
  });

const all: ImportCapabilities = { canCreate: true, canUpdate: true, canLinkCustomers: true };

const mapping = {
  mode: "createOrUpdate",
  columns: [
    { field: "gsrn", column: 0 },
    { field: "customerLegalId", column: 1 },
  ],
};

/** Literally the wire body (the optional keys absent), in the state asked for. */
const wireImport = (overrides: Record<string, unknown> = {}) => ({
  id: 7,
  state: "draft",
  fileName: "anlegg.csv",
  sizeBytes: 120,
  encoding: "utf-8",
  separator: ";",
  rows: 3,
  header: ["MÅLEPUNKTID", "ORG. NR.", "KOMMUNE"],
  sample: [
    { row: 1, cells: ["707057500000000001", "923609016", "Oslo"], errors: [] },
    {
      row: 2,
      cells: ["123", "923609016", "Bergen"],
      errors: [{ column: 0, message: "GSRN must be exactly 18 digits" }],
    },
  ],
  suggested: mapping,
  mapping,
  processed: 0,
  created: 0,
  updated: 0,
  failed: 0,
  skipped: 0,
  switched: 0,
  pending: 0,
  pendingSwitches: 0,
  attached: 0,
  stopRequested: false,
  createdAt: "2026-10-18T10:00:00Z",
  createdByName: "Kari Nordmann",
  ...overrides,
});

const rowsPage = (rows: unknown[]) => ({
  data: rows,
  pagination: { page: 1, pageSize: 50, totalCount: rows.length, totalPages: 1 },
});

const switchedRow = {
  row: 1,
  cells: [],
  errors: [],
  outcome: "updated",
  link: "switched",
  gsrn: "707057500000000001",
  meteringPointId: 41,
  customerId: 12,
  customerName: "Nytt Kraft AS",
  switchFromCustomerId: 11,
  switchFromCustomerName: "Gammel Kraft AS",
  supplyStart: "2026-11-01T00:00:00Z",
};

/** Answers the import's reads and whatever `extra` answers; records every request. */
const stubImport = (
  imp: Record<string, unknown>,
  extra: (url: string, init?: RequestInit) => Response | undefined = () => undefined,
) =>
  stubFetch(async (input, init) => {
    const url = String(input);
    const handled = extra(url, init);
    if (handled) return handled;
    if (url === "/api/v1/energy/imports/7" && (!init?.method || init.method === "GET")) return json(imp);
    if (url.startsWith("/api/v1/energy/imports/7/rows")) return json(rowsPage([]));
    return new Response(null, { status: 404 });
  });

const renderPage = (capabilities = all) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <ImportPage importId={7} capabilities={capabilities} />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

const posted = (fetchMock: ReturnType<typeof stubFetch>, suffix: string) =>
  fetchMock.mock.calls
    .filter(([url, init]) => String(url).endsWith(suffix) && (init as RequestInit)?.method === "POST")
    .map(([, init]) => JSON.parse(((init as RequestInit).body as string) ?? "null"));

describe("ImportPage", () => {
  it("shows the mapping step for a draft, the fields grouped and the preview's errors listed", async () => {
    stubImport(wireImport());
    renderPage();

    expect(await screen.findByRole("heading", { name: "anlegg.csv" })).toBeInTheDocument();
    expect(screen.getByText(/3 rows · UTF-8 · semicolon-separated · uploaded by Kari Nordmann/)).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Field for MÅLEPUNKTID" })).toHaveValue("GSRN");
    expect(screen.getByRole("combobox", { name: "Field for ORG. NR." })).toHaveValue("Customer's organisation number");
    expect(screen.getByRole("combobox", { name: "Field for KOMMUNE" })).toHaveValue("Not imported");
    expect(
      within(screen.getByRole("list", { name: "Problems in the sample rows" })).getByText(
        "Row 2: MÅLEPUNKTID: GSRN must be exactly 18 digits",
      ),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("combobox", { name: "Field for KOMMUNE" }));
    const listbox = await screen.findByRole("listbox");
    expect(within(listbox).getByText("Metering point")).toBeInTheDocument();
    expect(within(listbox).getByText("Address")).toBeInTheDocument();
    expect(within(listbox).getByText("Customer link")).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    expect(screen.getByRole("button", { name: "Check file" })).toBeEnabled();
  });

  it("hides the customer link and disables the modes the person may not run, and saves without them", async () => {
    const fetchMock = stubImport(wireImport({ mapping: undefined }), (url, init) =>
      url.endsWith("/mapping") && init?.method === "PUT" ? json(wireImport()) : undefined,
    );
    renderPage({ canCreate: false, canUpdate: true, canLinkCustomers: false });
    await screen.findByRole("heading", { name: "anlegg.csv" });

    expect(screen.getByRole("radio", { name: "Create new metering points" })).toBeDisabled();
    expect(screen.getByRole("radio", { name: "Create and update" })).toBeDisabled();
    expect(screen.getByRole("radio", { name: "Update existing metering points" })).toBeChecked();
    await userEvent.click(screen.getByRole("combobox", { name: "Field for KOMMUNE" }));
    const listbox = await screen.findByRole("listbox");
    expect(within(listbox).queryByText("Customer link")).not.toBeInTheDocument();
    await userEvent.keyboard("{Escape}");

    await waitFor(() => {
      const put = fetchMock.mock.calls.find(([, init]) => (init as RequestInit)?.method === "PUT");
      if (!put) throw new Error("the mapping was not saved");
      expect(JSON.parse((put[1] as RequestInit).body as string)).toEqual({
        mode: "update",
        columns: [{ field: "gsrn", column: 0 }],
      });
    });
  });

  it("puts the switch warning above everything, lists the switches, and gates Import on the checkbox", async () => {
    const fetchMock = stubImport(
      wireImport({ state: "checked", processed: 3, updated: 2, created: 1, switched: 2, pending: 1 }),
      (url, init) => {
        if (url.startsWith("/api/v1/energy/imports/7/rows?link=switched")) {
          return json(rowsPage([switchedRow, { ...switchedRow, row: 2, gsrn: "707057500000000002" }]));
        }
        if (url === "/api/v1/energy/imports/7/run" && init?.method === "POST") {
          return json(wireImport({ state: "importing", requestedAt: "2026-10-18T10:03:00Z" }));
        }
        return undefined;
      },
    );
    renderPage();

    const alert = await screen.findByText(
      "2 metering points will switch customer. Each one's current supply period ends on the row's start date and the customer in the file takes over.",
    );
    expect(alert).toBeInTheDocument();
    // Above the check's own card.
    expect(alert.compareDocumentPosition(screen.getByText("The file has been checked"))).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    const table = (await screen.findByText("Metering points that will switch customer")).closest("table");
    if (!table) throw new Error("no switch table");
    expect(within(table).getByRole("columnheader", { name: "Current customer" })).toBeInTheDocument();
    expect(within(table).getAllByRole("link", { name: "Gammel Kraft AS" })[0]).toHaveAttribute("href", "/customers/11");
    expect(within(table).getAllByRole("link", { name: "Nytt Kraft AS" })[0]).toHaveAttribute("href", "/customers/12");
    expect(screen.getByText("Would switch customer").nextSibling).toHaveTextContent("2");
    expect(screen.getByText("Would wait for a start date").nextSibling).toHaveTextContent("1");

    await userEvent.click(screen.getByRole("button", { name: "Import" }));
    const dialog = await screen.findByRole("dialog", { name: "Import the file?" });
    expect(within(dialog).getByText(/1 metering points will be created and 2 updated/)).toBeInTheDocument();
    const confirm = within(dialog).getByRole("button", { name: "Import" });
    expect(confirm).toBeDisabled();
    await userEvent.click(
      within(dialog).getByRole("checkbox", { name: "I understand that 2 metering points will switch customer." }),
    );
    expect(confirm).toBeEnabled();
    await userEvent.click(confirm);

    await waitFor(() => expect(posted(fetchMock, "/run")).toEqual([{ confirmSwitches: 2 }]));
    expect(await screen.findByText("Importing")).toBeInTheDocument();
  });

  it("shows the server's word and reads the import again when the switches were not confirmed", async () => {
    const fetchMock = stubImport(wireImport({ state: "checked", processed: 3, updated: 1, switched: 1 }), (url) =>
      url === "/api/v1/energy/imports/7/run"
        ? json(
            {
              title: "Conflict",
              status: 409,
              detail: "The check counted 3 switches; confirm that number to run the import.",
              code: "import_switch_not_confirmed",
              switches: 3,
            },
            409,
          )
        : undefined,
    );
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "Import" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("checkbox"));
    await userEvent.click(within(dialog).getByRole("button", { name: "Import" }));

    expect(
      await screen.findByText("The check counted 3 switches; confirm that number to run the import."),
    ).toBeInTheDocument();
    const reads = () =>
      fetchMock.mock.calls.filter(([url, init]) => String(url) === "/api/v1/energy/imports/7" && !init?.method).length;
    await waitFor(() => expect(reads()).toBeGreaterThan(1));
  });

  it("offers to attach the pending points on a finished import, with the switch warning and its checkbox", async () => {
    const fetchMock = stubImport(
      wireImport({
        state: "done",
        processed: 3,
        created: 3,
        pending: 2,
        pendingSwitches: 1,
        finishedAt: "2026-10-18T10:05:00Z",
      }),
      (url, init) => {
        if (url.startsWith("/api/v1/energy/imports/7/rows?link=pending")) {
          return json(
            rowsPage([
              { ...switchedRow, link: "pending", outcome: "created", supplyStart: undefined },
              {
                ...switchedRow,
                row: 2,
                link: "pending",
                outcome: "created",
                switchFromCustomerId: undefined,
                switchFromCustomerName: undefined,
              },
            ]),
          );
        }
        if (url === "/api/v1/energy/imports/7/attach" && init?.method === "POST") {
          return json(
            wireImport({
              state: "attaching",
              processed: 3,
              created: 3,
              pending: 2,
              requestedAt: "2026-10-18T10:06:00Z",
            }),
          );
        }
        return undefined;
      },
    );
    renderPage();

    expect(await screen.findByText("Attach to customers")).toBeInTheDocument();
    expect(
      screen.getByText(/2 metering points are matched to a customer but have no supply period yet/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "1 metering point will switch customer. Its current supply period ends on the date you choose and the customer in the file takes over.",
      ),
    ).toBeInTheDocument();
    const attach = screen.getByRole("button", { name: "Attach" });
    expect(attach).toBeDisabled();

    await userEvent.type(screen.getByLabelText(/Supply start date/), "2026-11-01");
    expect(attach).toBeDisabled();
    await userEvent.click(
      screen.getByRole("checkbox", { name: "I understand that 1 metering point will switch customer." }),
    );
    expect(attach).toBeEnabled();
    await userEvent.click(attach);

    await waitFor(() =>
      expect(posted(fetchMock, "/attach")).toEqual([{ start: "2026-11-01T00:00:00.000Z", confirmSwitches: 1 }]),
    );
    // Attaching is the last step's work: the bar, with the attach's own counts.
    expect(await screen.findByText("Attaching to customers")).toBeInTheDocument();
    expect(screen.getByText("Waiting for the worker to start")).toBeInTheDocument();
  });

  it("does not let a person without the supply-period permission attach", async () => {
    stubImport(wireImport({ state: "done", processed: 3, created: 3, pending: 2 }));
    renderPage({ canCreate: true, canUpdate: true, canLinkCustomers: false });
    expect(await screen.findByText("Attach to customers")).toBeInTheDocument();
    expect(
      screen.getByText("Attaching needs the energy:supply-periods-manage permission, which you do not have."),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Attach" })).toBeDisabled();
  });

  it("shows what a finished import did, with the switched points and both customers as links", async () => {
    stubImport(
      wireImport({ state: "done", processed: 3, updated: 1, switched: 1, finishedAt: "2026-10-18T10:05:00Z" }),
      (url) => (url.includes("link=switched") ? json(rowsPage([switchedRow])) : undefined),
    );
    renderPage();
    expect(await screen.findByText("The import is done")).toBeInTheDocument();
    expect(screen.queryByText("Attach to customers")).not.toBeInTheDocument();
    expect(screen.getByText("Switched customer").nextSibling).toHaveTextContent("1");
    const table = (await screen.findByText("Metering points that switched customer")).closest("table");
    if (!table) throw new Error("no switch table");
    expect(within(table).getByRole("columnheader", { name: "Previous customer" })).toBeInTheDocument();
    expect(within(table).getByRole("link", { name: "707057500000000001" })).toHaveAttribute(
      "href",
      "/energy/metering-points/41",
    );
    expect(within(table).getByRole("link", { name: "Gammel Kraft AS" })).toHaveAttribute("href", "/customers/11");
  });
});
