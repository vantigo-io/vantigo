import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import { ApiConflictError } from "./request";
import { createWorkType, updateWorkType, type WorkType, workTypesQueryOptions } from "./work-types";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

/** Literally the body the server sends: every field of WorkTypeResponse is required. */
const overtime: WorkType = {
  id: 11,
  projectId: 7,
  name: "Overtid 50 %",
  billMultiplierPercent: 150,
  costMultiplierPercent: 140,
  active: true,
  createdAt: "2026-09-01T08:00:00Z",
  updatedAt: "2026-09-01T08:00:00Z",
};

describe("workTypesQueryOptions", () => {
  it("reads the project's work types under the projects root", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [overtime])));

    const options = workTypesQueryOptions(7);
    expect(await runQuery(options)).toEqual([overtime]);
    expect(options.queryKey).toEqual(["projects", "detail", 7, "work-types"]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/work-types", { signal: undefined });
  });
});

describe("createWorkType and updateWorkType", () => {
  it("POSTs a new type and PUTs a change, active included", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, overtime)));
    const input = { name: "Overtid 50 %", billMultiplierPercent: 150, costMultiplierPercent: 140 };

    await createWorkType(7, input);
    await updateWorkType(7, 11, { ...input, active: false });

    expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/v1/projects/7/work-types", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/v1/projects/7/work-types/11", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...input, active: false }),
    });
  });

  it("surfaces the 409 a taken name answers as a conflict", async () => {
    // Literally the server's problem: a title, no code, no fields.
    stubFetch(() => Promise.resolve(jsonResponse(409, { title: "Work type exists", status: 409 })));

    const error = await createWorkType(7, {
      name: "overtid 50 %",
      billMultiplierPercent: 150,
      costMultiplierPercent: 150,
    }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiConflictError);
    expect(error).toMatchObject({ status: 409, title: "Work type exists" });
  });
});
