import { MantineProvider } from "@mantine/core";
import { modals } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { importRowsQueryOptions } from "../api/imports";
import type { ImportCapabilities } from "./-import-mapping-step";
import { ImportPage } from "./import.$importId";

// The whole router module is replaced: the real `Link` needs a router context
// this test has no reason to build (the follow-ups test's pattern).
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
  useNavigate: () => vi.fn(),
}));
vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));
// A confirmation confirms at once: what the person would do.
vi.mock("@mantine/modals", () => ({
  modals: { openConfirmModal: ({ onConfirm }: { onConfirm: () => void }) => onConfirm() },
}));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const all: ImportCapabilities = { canCreate: true, canUpdate: true, canMapIdentity: true, canMapBilling: true };

/** Literally the wire body (the optional keys absent), in the state asked for. */
const wireImport = (overrides: Record<string, unknown> = {}) => ({
  id: 7,
  state: "draft",
  fileName: "kunder.csv",
  sizeBytes: 120,
  encoding: "utf-8",
  separator: ";",
  rows: 3,
  header: ["Navn", "E-post", "Noe"],
  sample: [
    { row: 1, cells: ["Acme AS", "post@acme.no", "x"], errors: [] },
    {
      row: 2,
      cells: ["Beta AS", "nope", "y"],
      errors: [{ column: 1, message: "An email address must look like name@example.com, but was 'nope'" }],
    },
  ],
  suggested: {
    mode: "create",
    allowDuplicateIdentity: false,
    columns: [
      { field: "name", column: 0 },
      { field: "email", column: 1 },
    ],
  },
  mapping: {
    mode: "create",
    allowDuplicateIdentity: false,
    columns: [
      { field: "name", column: 0 },
      { field: "email", column: 1 },
    ],
  },
  processed: 0,
  created: 0,
  updated: 0,
  failed: 0,
  skipped: 0,
  stopRequested: false,
  createdAt: "2026-10-13T10:00:00Z",
  createdByName: "Kari Nordmann",
  ...overrides,
});

const rowsPage = (rows: unknown[]) => ({
  data: rows,
  pagination: {
    page: 1,
    pageSize: 50,
    totalCount: rows.length,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
  },
});

/** Answers the import's reads and records every other request. */
const stubImport = (
  imp: Record<string, unknown>,
  extra: (url: string, init?: RequestInit) => Response | undefined = () => undefined,
) => {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const handled = extra(url, init);
    if (handled) return handled;
    if (url === "/api/v1/customers/imports/7" && (!init?.method || init.method === "GET")) return json(imp);
    return new Response(null, { status: 404 });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
};

const renderPage = (capabilities = all, prefill: (queryClient: QueryClient) => void = () => {}) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  prefill(queryClient);
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <ImportPage importId={7} capabilities={capabilities} />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("ImportPage", () => {
  it("shows the file's facts and the mapping step for a draft, with the preview's errors marked", async () => {
    stubImport(wireImport());
    renderPage();

    expect(await screen.findByRole("heading", { name: "kunder.csv" })).toBeInTheDocument();
    expect(screen.getByText(/3 rows · UTF-8 · semicolon-separated · uploaded by Kari Nordmann/)).toBeInTheDocument();
    expect(screen.getByText("Columns in the file")).toBeInTheDocument();
    // Every file column has a Select, the suggested field in it; the third is not imported.
    expect(screen.getByRole("combobox", { name: "Field for Navn" })).toHaveValue("Name");
    expect(screen.getByRole("combobox", { name: "Field for E-post" })).toHaveValue("Email");
    expect(screen.getByRole("combobox", { name: "Field for Noe" })).toHaveValue("Not imported");
    // The preview: one clean row of two, the problem listed by row and column.
    expect(screen.getByText("1 of 2 sample rows read cleanly. The check below reads every row.")).toBeInTheDocument();
    expect(
      within(screen.getByRole("list", { name: "Problems in the sample rows" })).getByText(
        "Row 2: E-post: An email address must look like name@example.com, but was 'nope'",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Check file" })).toBeEnabled();
  });

  it("hides the groups the person may not map and disables the modes they may not run", async () => {
    stubImport(wireImport());
    renderPage({ canCreate: true, canUpdate: false, canMapIdentity: false, canMapBilling: false });
    await screen.findByRole("heading", { name: "kunder.csv" });

    await userEvent.click(screen.getByRole("combobox", { name: "Field for Noe" }));
    const listbox = await screen.findByRole("listbox");
    expect(within(listbox).queryByText("Legal identity")).not.toBeInTheDocument();
    expect(within(listbox).queryByText("Billing profile")).not.toBeInTheDocument();
    expect(within(listbox).getByText("Contact info")).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");

    expect(screen.getByRole("radio", { name: "Update existing customers" })).toBeDisabled();
    expect(screen.getByRole("radio", { name: "Create and update" })).toBeDisabled();
    expect(screen.getByRole("radio", { name: "Create new customers" })).toBeEnabled();
  });

  it("saves a change to the mapping and shows the new preview, then checks the file", async () => {
    const changed = wireImport({
      mapping: {
        mode: "create",
        allowDuplicateIdentity: false,
        columns: [
          { field: "name", column: 0 },
          { field: "phone", column: 1 },
        ],
      },
      sample: [
        {
          row: 1,
          cells: ["Acme AS", "post@acme.no", "x"],
          errors: [{ column: 1, message: "A phone number may only contain digits" }],
        },
        {
          row: 2,
          cells: ["Beta AS", "nope", "y"],
          errors: [{ column: 1, message: "A phone number may only contain digits" }],
        },
      ],
    });
    const fetchMock = stubImport(wireImport(), (url, init) => {
      if (url === "/api/v1/customers/imports/7/mapping" && init?.method === "PUT") return json(changed);
      if (url === "/api/v1/customers/imports/7/check" && init?.method === "POST") {
        return json(wireImport({ state: "checking", requestedAt: "2026-10-13T10:01:00Z" }));
      }
      return undefined;
    });
    renderPage();
    await screen.findByRole("heading", { name: "kunder.csv" });

    await userEvent.click(screen.getByRole("combobox", { name: "Field for E-post" }));
    await userEvent.click(await screen.findByRole("option", { name: "Phone" }));
    await waitFor(() => {
      const put = fetchMock.mock.calls.find(([, init]) => (init as RequestInit)?.method === "PUT");
      if (!put) throw new Error("the mapping was not saved");
      expect(JSON.parse((put[1] as RequestInit).body as string)).toEqual(changed.mapping);
    });
    expect(
      await screen.findByText("0 of 2 sample rows read cleanly. The check below reads every row."),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Check file" }));
    expect(await screen.findByText("Checking the file")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
  });

  it("stops a run the worker has not started without asking, and shows the file being checked again", async () => {
    const confirm = vi.spyOn(modals, "openConfirmModal");
    const fetchMock = stubImport(
      wireImport({ state: "importing", requestedAt: "2026-10-13T10:03:00Z" }),
      (url, init) =>
        url === "/api/v1/customers/imports/7/stop" && init?.method === "POST"
          ? json(wireImport({ state: "checking", requestedAt: "2026-10-13T10:04:00Z" }))
          : undefined,
    );
    renderPage();

    await userEvent.click(await screen.findByRole("button", { name: "Stop" }));
    expect(await screen.findByText("Checking the file")).toBeInTheDocument();
    expect(confirm).not.toHaveBeenCalled();
    expect(fetchMock.mock.calls.some(([input]) => String(input) === "/api/v1/customers/imports/7/stop")).toBe(true);
    confirm.mockRestore();
  });

  it("shows the bar while checking, and says when nobody has taken the import up", async () => {
    stubImport(
      wireImport({
        state: "checking",
        requestedAt: "2020-01-01T00:00:00Z",
        startedAt: "2020-01-01T00:00:01Z",
        processed: 1,
        created: 1,
      }),
    );
    renderPage();
    expect(await screen.findByText("1 of 3 rows")).toBeInTheDocument();
    expect(screen.getByText("Created: 1 · Updated: 0 · Skipped: 0 · Failed: 0")).toBeInTheDocument();
    cleanup();

    stubImport(wireImport({ state: "importing", requestedAt: "2020-01-01T00:00:00Z" }));
    renderPage();
    expect(await screen.findByText("Waiting for the background worker")).toBeInTheDocument();
    expect(screen.getByText("Waiting for the worker to start")).toBeInTheDocument();
  });

  it("shows the check's counts and the failed rows, and runs the import on confirmation", async () => {
    const failed = rowsPage([
      { row: 2, cells: ["Beta AS", "nope", "y"], outcome: "failed", errors: [{ column: 1, message: "Not an email" }] },
    ]);
    // What the check resolved: a company named and addressed by Brønnøysund,
    // and an update the file gave everything for.
    const created = rowsPage([
      {
        row: 1,
        cells: ["", "post@equinor.no", "x"],
        outcome: "created",
        customerId: 900,
        errors: [],
        preview: {
          name: "EQUINOR ASA",
          type: "business",
          legalIdentity: { country: "no", id: "923609016", name: "EQUINOR ASA", source: "brreg" },
          postalAddress: { line1: "Postboks 8500", postalCode: "4035", city: "STAVANGER", country: "no" },
          fromRegistry: ["name", "legalName", "postalLine1", "postalPostalCode", "postalCity", "postalCountry"],
        },
      },
      {
        row: 2,
        cells: ["Kontor", "post@kontor.no", "y"],
        outcome: "created",
        errors: [],
        preview: {
          name: "Kontor",
          type: "business",
          legalIdentity: { country: "no", id: "974760673", name: "KONTOR OG KAFFE AS", source: "brreg" },
          postalAddress: {
            line1: "Strandkaien 2",
            postalCode: "5003",
            city: "BERGEN",
            region: "Vestland",
            country: "no",
          },
          fromRegistry: ["legalName", "postalLine1", "postalPostalCode", "postalCity", "postalCountry"],
        },
      },
      // A row read before the preview existed: its name as the file has it.
      { row: 5, cells: ["Delta AS", "post@delta.no", "v"], outcome: "created", errors: [] },
    ]);
    const updated = rowsPage([
      {
        row: 4,
        cells: ["Acme AS", "post@acme.no", "w"],
        outcome: "updated",
        customerId: 12,
        customerNumber: 1012,
        errors: [],
        preview: {
          name: "Acme AS",
          type: "business",
          postalAddress: { line1: "Storgata 1", line2: "Bakgården", postalCode: "0150", city: "Oslo", country: "no" },
          fromRegistry: [],
        },
      },
      {
        row: 6,
        cells: ["Epsilon AS", "", ""],
        outcome: "updated",
        customerId: 13,
        customerNumber: 1013,
        errors: [],
        preview: { name: "Epsilon AS", type: "business", postalAddressCleared: true, fromRegistry: [] },
      },
    ]);
    const fetchMock = stubImport(
      wireImport({
        state: "checked",
        processed: 3,
        created: 3,
        updated: 2,
        failed: 1,
        skipped: 2,
        finishedAt: "2026-10-13T10:02:00Z",
      }),
      (url, init) => {
        if (url.startsWith("/api/v1/customers/imports/7/rows?outcome=failed")) return json(failed);
        if (url.startsWith("/api/v1/customers/imports/7/rows?outcome=created")) return json(created);
        if (url.startsWith("/api/v1/customers/imports/7/rows?outcome=updated")) return json(updated);
        if (url.startsWith("/api/v1/customers/imports/7/rows?outcome=skipped")) {
          return json(
            rowsPage([
              {
                row: 3,
                cells: ["Gamma AS", "post@gamma.no", "z"],
                outcome: "skipped",
                errors: [{ message: "Same customer as row 1." }],
              },
            ]),
          );
        }
        if (url === "/api/v1/customers/imports/7/run" && init?.method === "POST") {
          return json(wireImport({ state: "importing", requestedAt: "2026-10-13T10:03:00Z" }));
        }
        return undefined;
      },
    );
    renderPage();
    expect(await screen.findByText("The file has been checked")).toBeInTheDocument();
    expect(screen.getByText("Would be created").nextSibling).toHaveTextContent("3");
    expect(screen.getByText("Have errors").nextSibling).toHaveTextContent("1");
    expect(screen.getByText("Would be skipped").nextSibling).toHaveTextContent("2");
    expect(await screen.findByText("Same customer as row 1.")).toBeInTheDocument();
    expect(screen.getByText("Rows skipped: the same customer as an earlier row")).toBeInTheDocument();
    expect(await screen.findByText("Not an email")).toBeInTheDocument();
    expect(screen.getByText("E-post")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Download failed rows" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Change mapping" })).toBeInTheDocument();

    // The preview: what would be created and updated, Brønnøysund's values marked.
    const willCreate = (await screen.findByText("Will be created")).closest("table") as HTMLElement;
    const equinor = within(willCreate).getByText("EQUINOR ASA").closest("tr") as HTMLElement;
    expect(within(equinor).getByText("923609016")).toBeInTheDocument();
    expect(within(equinor).getByText("Postboks 8500, 4035 STAVANGER, NO")).toBeInTheDocument();
    expect(within(equinor).getAllByText("From Brønnøysund")).toHaveLength(2);
    const willUpdate = (await screen.findByText("Will be updated")).closest("table") as HTMLElement;
    const acme = within(willUpdate).getByText("Acme AS").closest("tr") as HTMLElement;
    expect(within(acme).getByText("Storgata 1, Bakgården, 0150 Oslo, NO")).toBeInTheDocument();
    expect(within(acme).queryByText("From Brønnøysund")).not.toBeInTheDocument();
    // The file's name with the registry's legal name beneath, that alone marked.
    const kontor = within(willCreate).getByText("Kontor").closest("tr") as HTMLElement;
    const legalName = within(kontor).getByText("KONTOR OG KAFFE AS");
    expect(within(legalName.parentElement as HTMLElement).getByText("From Brønnøysund")).toBeInTheDocument();
    expect(within(kontor).getAllByText("From Brønnøysund")).toHaveLength(2);
    expect(within(kontor).getByText("Strandkaien 2, 5003 BERGEN, Vestland, NO")).toBeInTheDocument();
    // The marker is focusable and described by its hint.
    const badge = within(kontor).getAllByText("From Brønnøysund")[0].closest("[tabindex]") as HTMLElement;
    expect(badge).toHaveAttribute("tabindex", "0");
    expect(badge).toHaveAccessibleDescription(/Brønnøysundregistrene/);
    // A row with no preview falls back on the file's name.
    const delta = within(willCreate).getByText("Delta AS").closest("tr") as HTMLElement;
    expect(within(delta).getAllByText("—")).toHaveLength(2);
    // An update that clears the address says so.
    const epsilon = within(willUpdate).getByText("Epsilon AS").closest("tr") as HTMLElement;
    expect(within(epsilon).getByText("Will be removed")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Import" }));
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(
          ([url, init]) => String(url).endsWith("/run") && (init as RequestInit)?.method === "POST",
        ),
      ).toBe(true),
    );
    expect(await screen.findByText("Importing")).toBeInTheDocument();
  });

  it("shows what a finished import did, the customers it created as links, and what a stop left", async () => {
    stubImport(
      wireImport({
        state: "done",
        processed: 4,
        rows: 4,
        created: 2,
        failed: 1,
        skipped: 1,
        finishedAt: "2026-10-13T10:05:00Z",
      }),
      (url) => {
        if (url.startsWith("/api/v1/customers/imports/7/rows?outcome=failed")) {
          return json(
            rowsPage([
              { row: 2, cells: ["Beta AS", "nope", "y"], outcome: "failed", errors: [{ message: "Row problem" }] },
            ]),
          );
        }
        if (url.startsWith("/api/v1/customers/imports/7/rows?outcome=skipped")) {
          return json(
            rowsPage([
              {
                row: 4,
                cells: ["Gamma AS", "post@gamma.no", "w"],
                outcome: "skipped",
                errors: [{ message: "Same customer as row 1." }],
              },
            ]),
          );
        }
        if (url.startsWith("/api/v1/customers/imports/7/rows?outcome=created")) {
          return json(
            rowsPage([
              {
                row: 1,
                cells: ["Acme AS", "post@acme.no", "x"],
                outcome: "created",
                customerId: 42,
                customerNumber: 1042,
                errors: [],
              },
            ]),
          );
        }
        return undefined;
      },
    );
    renderPage();
    expect(await screen.findByText("The import is done")).toBeInTheDocument();
    expect(screen.getByText("Created").nextSibling).toHaveTextContent("2");
    expect(screen.getByText("Skipped").nextSibling).toHaveTextContent("1");
    expect(await screen.findByText("Same customer as row 1.")).toBeInTheDocument();
    expect(await screen.findByRole("link", { name: "#1042" })).toHaveAttribute("href", "/customers/42");
    expect(screen.getByText("Acme AS")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Start another import" })).toHaveAttribute("href", "/customers/import");
    cleanup();

    stubImport(wireImport({ state: "done", processed: 1, created: 1, finishedAt: "2026-10-13T10:05:00Z" }), (url) =>
      url.includes("/rows?") ? json(rowsPage([])) : undefined,
    );
    renderPage();
    expect(await screen.findByText("The import was stopped")).toBeInTheDocument();
    expect(screen.getByText("Not attempted").nextSibling).toHaveTextContent("2");
  });

  it("never shows the check's rows, with their rolled-back customers, as the run's", async () => {
    const finishedAt = "2026-10-13T10:02:00Z";
    stubImport(wireImport({ state: "done", processed: 1, created: 1, finishedAt }), (url) => {
      if (url.startsWith("/api/v1/customers/imports/7/rows?outcome=created")) {
        return json(
          rowsPage([
            {
              row: 1,
              cells: ["Acme AS", "post@acme.no", "x"],
              outcome: "created",
              customerId: 41,
              customerNumber: 1041,
              errors: [],
            },
          ]),
        );
      }
      return url.includes("/rows?") ? json(rowsPage([])) : undefined;
    });
    // The check's page of the same rows, at the same moment, still cached.
    renderPage(all, (queryClient) =>
      queryClient.setQueryData(
        importRowsQueryOptions(7, "created", 1, 50, true, finishedAt).queryKey,
        rowsPage([
          {
            row: 1,
            cells: ["Acme AS", "post@acme.no", "x"],
            outcome: "created",
            customerId: 900,
            customerNumber: null,
            errors: [],
            preview: { name: "Acme AS", type: "business", fromRegistry: [] },
          },
        ]) as never,
      ),
    );
    expect(await screen.findByText("#1041")).toBeInTheDocument();
    expect(screen.queryByText("#900")).not.toBeInTheDocument();
  });

  it("shows a run's error as the worker wrote it", async () => {
    stubImport(
      wireImport({
        state: "importing",
        requestedAt: "2026-10-13T10:03:00Z",
        startedAt: "2026-10-13T10:03:01Z",
        error: "the database went away",
      }),
    );
    renderPage();
    expect(await screen.findByText("The import met an error")).toBeInTheDocument();
    expect(
      screen.getByText("the database went away — it is retried shortly, from where it stood."),
    ).toBeInTheDocument();
  });
});
