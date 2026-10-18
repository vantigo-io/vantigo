import { afterEach, describe, expect, it, vi } from "vitest";
import {
  type CustomerImport,
  importQueryOptions,
  importStep,
  isImportRunning,
  normalizeImport,
  saveImportMapping,
  uploadImport,
} from "./imports";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** Literally the wire body of a fresh upload: the optional keys absent. */
const wireDraft = {
  id: 1001,
  state: "draft",
  fileName: "kunder.csv",
  sizeBytes: 120,
  encoding: "windows-1252",
  separator: ";",
  rows: 2,
  header: ["Navn", "E-post"],
  sample: [{ row: 1, cells: ["Acme AS", "post@acme.no"], errors: [{ message: "A row-level problem" }] }],
  suggested: { mode: "create", allowDuplicateIdentity: false, columns: [{ field: "name", column: 0 }] },
  processed: 0,
  created: 0,
  updated: 0,
  failed: 0,
  skipped: 0,
  stopRequested: false,
  createdAt: "2026-10-13T10:00:00Z",
};

afterEach(() => vi.unstubAllGlobals());

describe("normalizeImport", () => {
  it("fills in what the wire omits with null, an error's column included", () => {
    const imp = normalizeImport(wireDraft as never);
    expect(imp.mapping).toBeNull();
    expect(imp.error).toBeNull();
    expect(imp.createdByName).toBeNull();
    expect(imp.requestedAt).toBeNull();
    expect(imp.startedAt).toBeNull();
    expect(imp.finishedAt).toBeNull();
    expect(imp.sample[0].errors).toEqual([{ column: null, message: "A row-level problem" }]);
  });
});

describe("uploadImport and saveImportMapping", () => {
  it("sends the file as the part named file, and the mapping as JSON", async () => {
    const fetchMock = vi.fn(async () => json(wireDraft, 201));
    vi.stubGlobal("fetch", fetchMock);
    const file = new File(["Navn;E-post\r\nAcme AS;post@acme.no\r\n"], "kunder.csv", { type: "text/csv" });

    const imp = await uploadImport(file);
    expect(imp.id).toBe(1001);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/customers/imports");
    expect(init.method).toBe("POST");
    expect(((init.body as FormData).get("file") as File).name).toBe("kunder.csv");
    expect(new Headers(init.headers).has("Content-Type")).toBe(false);

    const mapping = { mode: "create" as const, allowDuplicateIdentity: true, columns: [{ field: "name", column: 0 }] };
    await saveImportMapping(1001, mapping);
    const [putUrl, putInit] = fetchMock.mock.calls[1] as unknown as [string, RequestInit];
    expect(putUrl).toBe("/api/v1/customers/imports/1001/mapping");
    expect(putInit.method).toBe("PUT");
    expect(JSON.parse(putInit.body as string)).toEqual(mapping);
  });
});

describe("the import's step and polling", () => {
  it("derives the step from the state", () => {
    expect(importStep("draft")).toBe(0);
    expect(importStep("checking")).toBe(1);
    expect(importStep("checked")).toBe(1);
    expect(importStep("importing")).toBe(2);
    expect(importStep("done")).toBe(2);
  });

  it("polls only while the worker works on the import", () => {
    const interval = importQueryOptions(1).refetchInterval as (query: {
      state: { data?: CustomerImport };
    }) => number | false;
    const at = (state: CustomerImport["state"]) =>
      interval({ state: { data: { ...normalizeImport(wireDraft as never), state } } });
    expect(at("checking")).toBe(2000);
    expect(at("importing")).toBe(2000);
    expect(at("draft")).toBe(false);
    expect(at("checked")).toBe(false);
    expect(at("done")).toBe(false);
    expect(interval({ state: {} })).toBe(false);
    expect(isImportRunning("checking")).toBe(true);
    expect(isImportRunning("done")).toBe(false);
  });
});
