import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  ApiValidationError,
  codeSuggestionQueryOptions,
  createProject,
  NotFoundError,
  projectQueryOptions,
  projectSearchQueryOptions,
  projectStatsQueryOptions,
  projectsQueryOptions,
  projectTimelineQueryOptions,
  setProjectStatus,
  updateProject,
} from "./projects";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

const emptyPage = { data: [], pagination: { page: 1, pageSize: 25, totalCount: 0, totalPages: 0 } };

describe("projectsQueryOptions", () => {
  it("omits empty filters and sends mine=true when the caller asked for their own projects", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, emptyPage)));
    const params = { page: 2, search: "  vei  ", status: "" as const, mine: true };

    const options = projectsQueryOptions(params);
    await runQuery(options);

    expect(options.queryKey).toEqual(["projects", "list", params]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/projects?page=2&pageSize=25&search=vei&mine=true");
  });

  it("leaves mine out when false and carries the status, customer and internal filters", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, emptyPage)));

    await runQuery(
      projectsQueryOptions({ page: 1, search: "", status: "active", customerId: 1001, internal: false, mine: false }),
    );

    expect(fetchMock.actualCalls[0]?.[0]).toBe(
      "/api/v1/projects?page=1&pageSize=25&status=active&customerId=1001&internal=false",
    );
  });

  it("sends internal=true for the internal-only filter", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, emptyPage)));

    await runQuery(projectsQueryOptions({ page: 1, search: "", status: "", internal: true, mine: false }));

    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/projects?page=1&pageSize=25&internal=true");
  });
});

describe("projectSearchQueryOptions", () => {
  it("asks for one page of the given size and answers the rows themselves", async () => {
    const rows = [{ id: 7, code: "KVEM1000", name: "Kverneland" }];
    const fetchMock = stubFetch(() =>
      Promise.resolve(jsonResponse(200, { data: rows, pagination: { page: 1, pageSize: 5, totalCount: 1 } })),
    );

    const options = projectSearchQueryOptions("kver", 5);
    const result = await runQuery(options);

    expect(options.queryKey).toEqual(["projects", "search", "kver", 5]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/projects?search=kver&pageSize=5");
    expect(result).toEqual(rows);
  });
});

describe("projectQueryOptions", () => {
  it("reads one project by id", async () => {
    const project = { id: 7, code: "KVEM1000", name: "Kverneland" };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, project)));

    const options = projectQueryOptions(7);
    const result = await runQuery(options);

    expect(result).toEqual(project);
    expect(options.queryKey).toEqual(["projects", "detail", 7]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7", { signal: undefined });
  });

  it("throws NotFoundError on 404", async () => {
    stubFetch(() => Promise.resolve(new Response(null, { status: 404 })));

    const error = await runQuery(projectQueryOptions(999999)).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(NotFoundError);
  });
});

describe("projectStatsQueryOptions", () => {
  it("reads the status counts", async () => {
    const counts = { planned: 1, active: 2, onHold: 0, completed: 0, cancelled: 0 };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, counts)));

    const options = projectStatsQueryOptions();
    const result = await runQuery(options);

    expect(result).toEqual(counts);
    expect(options.queryKey).toEqual(["projects", "stats"]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/stats", { signal: undefined });
  });
});

describe("projectTimelineQueryOptions", () => {
  it("pages the timeline", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, emptyPage)));

    const options = projectTimelineQueryOptions(7, 3);
    await runQuery(options);

    expect(options.queryKey).toEqual(["projects", "detail", 7, "timeline", 3]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/projects/7/timeline?page=3&pageSize=20");
  });
});

describe("codeSuggestionQueryOptions", () => {
  it("sends the customer id only when the project has a customer", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { code: "KVEM1000" })));

    await runQuery(codeSuggestionQueryOptions({ name: "Kvernehuset" }));
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/projects/code-suggestion?name=Kvernehuset");

    await runQuery(codeSuggestionQueryOptions({ customerId: 1001, name: "Kvernehuset" }));
    expect(fetchMock.actualCalls[1]?.[0]).toBe("/api/v1/projects/code-suggestion?customerId=1001&name=Kvernehuset");
  });

  it("keys the suggestion by customer and name", () => {
    expect(codeSuggestionQueryOptions({ customerId: 1001, name: "Kai" }).queryKey).toEqual([
      "projects",
      "code-suggestion",
      { customerId: 1001, name: "Kai" },
    ]);
  });
});

describe("createProject", () => {
  it("POSTs the input and returns the created project", async () => {
    const created = { id: 7, code: "KVEM1000", name: "Kverneland" };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, created)));
    const input = { code: "KVEM1000", name: "Kverneland", billingType: "time-and-materials" as const };

    await expect(createProject(input)).resolves.toEqual(created);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });

  it("throws ApiValidationError carrying the offending field on a 400", async () => {
    stubFetch(() =>
      Promise.resolve(
        jsonResponse(400, {
          title: "Invalid project",
          status: 400,
          errors: { code: ["A project code must match ^[A-Z0-9]{2,20}$"] },
        }),
      ),
    );

    const error = await createProject({ code: "x", name: "Kverneland", billingType: "non-billable" }).catch(
      (e: unknown) => e,
    );

    expect(error).toBeInstanceOf(ApiValidationError);
    expect((error as ApiValidationError).fieldErrors).toEqual({
      code: "A project code must match ^[A-Z0-9]{2,20}$",
    });
  });
});

describe("updateProject", () => {
  it("PUTs the input with the revision it was read at", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 7 })));
    const input = { code: "KVEM1000", name: "Kverneland", billingType: "fixed-price" as const, revision: 4 };

    await updateProject(7, input);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });
});

describe("setProjectStatus", () => {
  it("PUTs the new status to the status endpoint", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 7, status: "on-hold" })));

    await setProjectStatus(7, "on-hold");

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/status", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ status: "on-hold" }),
    });
  });

  it("throws NotFoundError when the project is gone", async () => {
    stubFetch(() => Promise.resolve(new Response(null, { status: 404 })));

    await expect(setProjectStatus(999999, "active")).rejects.toBeInstanceOf(NotFoundError);
  });
});

describe("stale writes", () => {
  it("surfaces the 409 a moved revision answers", async () => {
    stubFetch(() =>
      Promise.resolve(jsonResponse(409, { title: "The project was changed by someone else", status: 409 })),
    );

    const error = await updateProject(7, {
      code: "KVEM1000",
      name: "Kverneland",
      billingType: "non-billable",
      revision: 1,
    }).catch((e: unknown) => e as { status?: number });

    expect(error).toMatchObject({ status: 409 });
  });
});

describe("request wiring", () => {
  it("sends the session cookie on writes", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, { id: 7 })));

    await createProject({ code: "KVEM1000", name: "Kverneland", billingType: "non-billable" });

    const [, init] = fetchMock.actualCalls[0];
    expect(init?.credentials).toBe("include");
  });
});
