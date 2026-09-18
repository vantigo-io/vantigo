import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import { customerQueryOptions, customerSearchQueryOptions } from "./customers";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

describe("customerSearchQueryOptions", () => {
  it("searches the customers API and keeps only the id and name", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(
        jsonResponse(200, {
          data: [
            { id: 1001, name: "Kverneland", status: "active", identity: null },
            { id: 1002, name: "Equinor", status: "active", identity: null },
          ],
          pagination: { page: 1, pageSize: 20, totalCount: 2, totalPages: 1 },
        }),
      ),
    );

    const options = customerSearchQueryOptions(" kve ");
    const result = await runQuery(options);

    expect(result).toEqual([
      { id: 1001, name: "Kverneland" },
      { id: 1002, name: "Equinor" },
    ]);
    expect(options.queryKey).toEqual(["projects", "customers", "search", "kve"]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/customers?page=1&pageSize=20&search=kve");
  });

  it("lists the first page when nothing has been typed", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(jsonResponse(200, { data: [], pagination: { page: 1, pageSize: 20 } })),
    );

    await runQuery(customerSearchQueryOptions(""));

    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/customers?page=1&pageSize=20");
  });
});

describe("customerQueryOptions", () => {
  it("reads one customer by id", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 1001, name: "Kverneland" })));

    const options = customerQueryOptions(1001);
    const result = await runQuery(options);

    expect(result).toEqual({ id: 1001, name: "Kverneland" });
    expect(options.queryKey).toEqual(["projects", "customers", "detail", 1001]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001", { signal: undefined });
  });
});
