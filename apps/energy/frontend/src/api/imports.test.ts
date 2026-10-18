import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  attachImport,
  importQueryOptions,
  importRowsQueryOptions,
  importStep,
  isImportRunning,
  normalizeImport,
  normalizeRow,
  runImport,
  saveImportMapping,
} from "./imports";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** Literally the wire body of a fresh upload: the optional keys absent. */
const wireDraft = {
  id: 7,
  state: "draft",
  fileName: "anlegg.csv",
  sizeBytes: 120,
  encoding: "windows-1252",
  separator: ";",
  rows: 2,
  header: ["MÅLEPUNKTID", "ORG. NR."],
  sample: [{ row: 1, cells: ["707057500000000001", "923609016"], errors: [{ message: "A row-level problem" }] }],
  suggested: { mode: "createOrUpdate", columns: [{ field: "gsrn", column: 0 }] },
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
};

const run = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

describe("normalizeImport", () => {
  it("fills in what the wire omits with null, an error's column included", () => {
    const imp = normalizeImport(wireDraft as never);
    expect(imp.mapping).toBeNull();
    expect(imp.attachStart).toBeNull();
    expect(imp.error).toBeNull();
    expect(imp.createdByName).toBeNull();
    expect(imp.requestedAt).toBeNull();
    expect(imp.startedAt).toBeNull();
    expect(imp.finishedAt).toBeNull();
    expect(imp.sample[0].errors).toEqual([{ column: null, message: "A row-level problem" }]);
  });

  it("reads a row's link, point and both customers, null where absent", () => {
    const row = normalizeRow({
      row: 3,
      cells: [],
      errors: [],
      link: "switched",
      gsrn: "707057500000000001",
      customerId: 12,
      customerName: "Nytt AS",
      switchFromCustomerId: 11,
      switchFromCustomerName: "Gammelt AS",
      supplyStart: "2026-01-01T00:00:00Z",
    });
    expect(row).toMatchObject({ link: "switched", meteringPointId: null, outcome: null, switchFromCustomerId: 11 });
  });
});

describe("the import's step", () => {
  it("is derived from the state, attaching on the last step", () => {
    expect(importStep("draft")).toBe(0);
    expect(importStep("checking")).toBe(1);
    expect(importStep("checked")).toBe(1);
    expect(importStep("importing")).toBe(2);
    expect(importStep("done")).toBe(2);
    expect(importStep("attaching")).toBe(2);
  });

  it("polls while the worker checks, imports or attaches", () => {
    expect(isImportRunning("checking")).toBe(true);
    expect(isImportRunning("importing")).toBe(true);
    expect(isImportRunning("attaching")).toBe(true);
    expect(isImportRunning("checked")).toBe(false);
    expect(isImportRunning("done")).toBe(false);
    const options = importQueryOptions(7);
    const interval = options.refetchInterval as (query: unknown) => number | false;
    expect(interval({ state: { data: { state: "attaching" } } })).toBe(2000);
    expect(interval({ state: { data: { state: "done" } } })).toBe(false);
  });
});

describe("the import's requests", () => {
  it("lists rows by link", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(json({ data: [], pagination: { page: 1, pageSize: 50, totalCount: 0, totalPages: 0 } })),
    );
    await run(importRowsQueryOptions(7, { link: "switched" }));
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/v1/energy/imports/7/rows?link=switched&page=1&pageSize=50");
  });

  it("sends the mapping, the confirmed switches and the attach's start as JSON", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(json(wireDraft)));
    await saveImportMapping(7, { mode: "update", columns: [{ field: "gsrn", column: 0 }] });
    await runImport(7, 3);
    await attachImport(7, { start: "2026-11-01T00:00:00.000Z", confirmSwitches: 2 });
    const bodies = fetchMock.mock.calls.map(([url, init]) => [
      String(url),
      (init as RequestInit).method,
      JSON.parse((init as RequestInit).body as string),
    ]);
    expect(bodies).toEqual([
      ["/api/v1/energy/imports/7/mapping", "PUT", { mode: "update", columns: [{ field: "gsrn", column: 0 }] }],
      ["/api/v1/energy/imports/7/run", "POST", { confirmSwitches: 3 }],
      ["/api/v1/energy/imports/7/attach", "POST", { start: "2026-11-01T00:00:00.000Z", confirmSwitches: 2 }],
    ]);
  });
});
