import { describe, expect, it } from "vitest";
import { jsonResponse, runQuery } from "../test/api";
import { stubFetch } from "../test/fetch";
import { myOpenTasksQueryOptions, myProjectsQueryOptions, projectBillingLinesQueryOptions } from "./projects";

describe("myProjectsQueryOptions", () => {
  it("lists the projects the caller holds a role on and keeps what a picker needs", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(
        jsonResponse(200, {
          data: [
            {
              id: 1001,
              code: "KVEM1000",
              name: "Kverneland web",
              status: "active",
              billingType: "time-and-materials",
              managers: [],
              internal: false,
            },
          ],
          pagination: { page: 1, pageSize: 100, totalCount: 1, totalPages: 1 },
        }),
      ),
    );

    const options = myProjectsQueryOptions();
    const result = await runQuery(options);

    expect(result).toEqual([
      { id: 1001, code: "KVEM1000", name: "Kverneland web", status: "active", billingType: "time-and-materials" },
    ]);
    expect(options.queryKey[0]).toBe("time");
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/projects?mine=true&pageSize=100");
  });
});

describe("projectBillingLinesQueryOptions", () => {
  it("keeps the active lines only", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(
        jsonResponse(200, [
          { id: 3001, code: "PM", trackableCode: "KVEM1000-PM", productName: "Project management", active: true },
          { id: 3003, code: "OLD", trackableCode: "KVEM1000-OLD", productName: "Old work", active: false },
        ]),
      ),
    );

    const options = projectBillingLinesQueryOptions(1001);
    const result = await runQuery(options);

    expect(result).toEqual([
      { id: 3001, code: "PM", trackableCode: "KVEM1000-PM", productName: "Project management", active: true },
    ]);
    expect(options.queryKey[0]).toBe("time");
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/projects/1001/billing-lines");
  });

  it("reads a 409 — the products module is off — as no lines", async () => {
    stubFetch(() => Promise.resolve(jsonResponse(409, { title: "Billing lines are not available" })));

    expect(await runQuery(projectBillingLinesQueryOptions(1001))).toEqual([]);
  });

  it("still fails on any other error", async () => {
    stubFetch(() => Promise.resolve(jsonResponse(500, { title: "Boom" })));

    await expect(runQuery(projectBillingLinesQueryOptions(1001))).rejects.toThrow("Boom");
  });
});

describe("myOpenTasksQueryOptions", () => {
  it("reads the caller's open tasks and keeps the project each belongs to", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(
        jsonResponse(200, [
          {
            id: 5001,
            title: "Skriv spesifikasjonen",
            status: "todo",
            projectId: 1001,
            projectCode: "KVEM1000",
            projectName: "Kverneland web",
            position: 1,
          },
        ]),
      ),
    );

    const result = await runQuery(myOpenTasksQueryOptions());

    expect(result).toEqual([
      {
        id: 5001,
        title: "Skriv spesifikasjonen",
        projectId: 1001,
        projectCode: "KVEM1000",
        projectName: "Kverneland web",
      },
    ]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/projects/my-tasks");
  });
});
