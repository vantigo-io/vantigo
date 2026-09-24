import { afterEach, describe, expect, it, vi } from "vitest";
import { downloadCustomersCsv, downloadImportTemplate, importCustomers, isSessionExpired } from "./import-export";
import { setAuthStateClearer, setUnauthorizedHandler } from "./request";

afterEach(() => {
  vi.unstubAllGlobals();
  setUnauthorizedHandler(undefined);
  setAuthStateClearer(undefined);
});

describe("downloadCustomersCsv", () => {
  it("asks for the list's filters and sort without its paging, and keeps the server's file name", async () => {
    const fetchMock = vi.fn(
      async () =>
        new Response("\ufeffname\r\n", {
          status: 200,
          headers: { "Content-Disposition": 'attachment; filename="customers-2026-09-24.csv"' },
        }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const download = await downloadCustomersCsv({
      page: 3,
      pageSize: 25,
      search: "fjord",
      status: "archived",
      sortBy: "name",
      sortDirection: "desc",
      tagId: "t1",
    });

    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/customers/export?search=fjord&status=archived&sortBy=name&sortDirection=desc&tagId=t1");
    expect(init.credentials).toBe("include");
    expect(download.fileName).toBe("customers-2026-09-24.csv");
  });

  it("throws the refusal's own sentence", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ title: "Too many customers to export", detail: "Narrow it with a filter." }), {
            status: 400,
            headers: { "Content-Type": "application/problem+json" },
          }),
      ),
    );
    await expect(downloadCustomersCsv({})).rejects.toThrow("Narrow it with a filter.");
  });

  it("signs the person out when the session has expired, as every other request does", async () => {
    const cleared = vi.fn();
    const unauthorized = vi.fn();
    setAuthStateClearer(cleared);
    setUnauthorizedHandler(unauthorized);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(null, { status: 401 })),
    );

    const error = await downloadCustomersCsv({}).catch((caught: unknown) => caught);
    expect(cleared).toHaveBeenCalled();
    expect(unauthorized).toHaveBeenCalled();
    // The sign-out is the answer; the page must not also show an error for it.
    expect(isSessionExpired(error)).toBe(true);
    // The shared client's sentence, not whatever the 401's body happened to say.
    expect((error as Error).message).toBe("Your session has expired");
  });

  it("leaves the session alone on a refusal that is about the export", async () => {
    const unauthorized = vi.fn();
    setUnauthorizedHandler(unauthorized);
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ title: "Too many customers to export", detail: "Narrow it with a filter." }), {
            status: 400,
            headers: { "Content-Type": "application/problem+json" },
          }),
      ),
    );

    const error = await downloadCustomersCsv({}).catch((caught: unknown) => caught);
    expect((error as Error).message).toBe("Narrow it with a filter.");
    expect(unauthorized).not.toHaveBeenCalled();
    expect(isSessionExpired(error)).toBe(false);
  });
});

describe("downloadImportTemplate", () => {
  it("takes the encoded file name, and the plain one when the encoded one is malformed", async () => {
    const answer = (disposition: string) =>
      vi.fn(
        async () => new Response("\ufeffname\r\n", { status: 200, headers: { "Content-Disposition": disposition } }),
      );

    vi.stubGlobal("fetch", answer("attachment; filename=\"mal.csv\"; filename*=UTF-8''kunde%C3%A6r-mal.csv"));
    expect((await downloadImportTemplate()).fileName).toBe("kundeær-mal.csv");

    // A stray % is a URIError to decodeURIComponent; the download itself is fine.
    vi.stubGlobal("fetch", answer("attachment; filename=\"mal.csv\"; filename*=UTF-8''kunde%E0%A4%A-mal.csv"));
    expect((await downloadImportTemplate()).fileName).toBe("mal.csv");
  });
});

describe("importCustomers", () => {
  it("sends the file as the part named file, with both flags, and fills in an omitted column with null", async () => {
    // Literally the server's body: a row-level error has no column key at all.
    const fetchMock = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            dryRun: true,
            rows: 1,
            created: 0,
            updated: 0,
            failed: 1,
            errors: [{ row: 1, message: "This row has 1 cells, but the header has 2" }],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
    );
    vi.stubGlobal("fetch", fetchMock);
    const file = new File(["name\r\nA\r\n"], "kunder.csv", { type: "text/csv" });

    const result = await importCustomers(file, { dryRun: true, allowDuplicateIdentity: true });

    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/customers/import?dryRun=true&allowDuplicateIdentity=true");
    expect(init.method).toBe("POST");
    expect(((init.body as FormData).get("file") as File).name).toBe("kunder.csv");
    expect(new Headers(init.headers).has("Content-Type")).toBe(false);
    expect(result.errors).toEqual([{ row: 1, column: null, message: "This row has 1 cells, but the header has 2" }]);
  });
});
