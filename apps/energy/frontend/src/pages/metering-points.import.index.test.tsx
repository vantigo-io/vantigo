import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { ImportStartPage } from "./metering-points.import.index";

const router = vi.hoisted(() => ({ navigate: vi.fn() }));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
  useNavigate: () => router.navigate,
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

const page = (rows: unknown[]) => ({
  data: rows,
  pagination: { page: 1, pageSize: 10, totalCount: rows.length, totalPages: 1 },
});
const wireImport = (overrides: Record<string, unknown> = {}) => ({
  id: 7,
  state: "draft",
  fileName: "anlegg.csv",
  sizeBytes: 120,
  encoding: "utf-8",
  separator: ";",
  rows: 3,
  header: ["MÅLEPUNKTID"],
  sample: [],
  suggested: { mode: "createOrUpdate", columns: [] },
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

const renderPage = (answer: (url: string, init?: RequestInit) => Response | undefined) => {
  const fetchMock = stubFetch(
    async (input, init) => answer(String(input), init) ?? new Response(null, { status: 404 }),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <ImportStartPage />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return fetchMock;
};

afterEach(() => router.navigate.mockReset());

describe("ImportStartPage", () => {
  it("lists the recent imports with their state and result, a running one with its progress", async () => {
    renderPage((url) =>
      url.startsWith("/api/v1/energy/imports?")
        ? json(
            page([
              wireImport({ id: 7, state: "importing", processed: 120, rows: 300, requestedAt: "2026-10-18T10:01:00Z" }),
              wireImport({
                id: 6,
                fileName: "gammel.csv",
                state: "done",
                created: 10,
                updated: 2,
                failed: 1,
                skipped: 1,
                finishedAt: "2026-10-17T10:00:00Z",
              }),
              wireImport({ id: 5, fileName: "ventende.csv", state: "attaching", attached: 4, pending: 6 }),
            ]),
          )
        : undefined,
    );
    expect(await screen.findByRole("link", { name: "anlegg.csv" })).toHaveAttribute(
      "href",
      "/energy/metering-points/import/7",
    );
    expect(screen.getByText("120 of 300 rows")).toBeInTheDocument();
    expect(screen.getByText("Created: 10 · Updated: 2 · Skipped: 1 · Failed: 1")).toBeInTheDocument();
    expect(screen.getByText("Attaching")).toBeInTheDocument();
    expect(screen.getByText("Attached: 4 · Still pending: 6")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete the import of anlegg.csv" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Delete the import of ventende.csv" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Delete the import of gammel.csv" })).toBeEnabled();
  });

  it("uploads the chosen file and moves to the import's page", async () => {
    const fetchMock = renderPage((url, init) => {
      if (url === "/api/v1/energy/imports" && init?.method === "POST") return json(wireImport({ id: 9 }), 201);
      if (url.startsWith("/api/v1/energy/imports?")) return json(page([]));
      return undefined;
    });
    expect(await screen.findByText("No file has been imported yet.")).toBeInTheDocument();

    const file = new File(["MÅLEPUNKTID\r\n707057500000000001\r\n"], "anlegg.csv", { type: "text/csv" });
    await userEvent.upload(screen.getByLabelText("Choose CSV file"), file);

    await waitFor(() => expect(router.navigate).toHaveBeenCalledWith({ to: "/energy/metering-points/import/9" }));
    const post = fetchMock.mock.calls.find(([, init]) => (init as RequestInit)?.method === "POST");
    if (!post) throw new Error("the file was not uploaded");
    expect(((post[1] as RequestInit).body as FormData).get("file")).toBeInstanceOf(File);
  });

  it("shows the server's refusal of a file, and refuses one over 50 MB itself", async () => {
    renderPage((url, init) => {
      if (url === "/api/v1/energy/imports" && init?.method === "POST") {
        return json(
          { title: "Invalid import file", errors: { file: ["The file is empty; it needs a header row"] } },
          400,
        );
      }
      if (url.startsWith("/api/v1/energy/imports?")) return json(page([]));
      return undefined;
    });
    await screen.findByText("No file has been imported yet.");

    await userEvent.upload(screen.getByLabelText("Choose CSV file"), new File([""], "tom.csv", { type: "text/csv" }));
    expect(await screen.findByText("The file is empty; it needs a header row")).toBeInTheDocument();
    expect(router.navigate).not.toHaveBeenCalled();

    const big = new File([""], "stor.csv", { type: "text/csv" });
    Object.defineProperty(big, "size", { value: 50 * 1024 * 1024 + 1 });
    await userEvent.upload(screen.getByLabelText("Choose CSV file"), big);
    expect(await screen.findByText("stor.csv is larger than 50 MB, the most an import takes.")).toBeInTheDocument();
  });

  it("deletes an import on confirmation and re-reads the list", async () => {
    let deleted = false;
    renderPage((url, init) => {
      if (url === "/api/v1/energy/imports/6" && init?.method === "DELETE") {
        deleted = true;
        return new Response(null, { status: 204 });
      }
      if (url.startsWith("/api/v1/energy/imports?")) {
        return json(page(deleted ? [] : [wireImport({ id: 6, fileName: "gammel.csv", state: "done" })]));
      }
      return undefined;
    });
    await userEvent.click(await screen.findByRole("button", { name: "Delete the import of gammel.csv" }));
    expect(await screen.findByText("No file has been imported yet.")).toBeInTheDocument();
    expect(deleted).toBe(true);
  });
});
