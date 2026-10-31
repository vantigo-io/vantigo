import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  applyProjectTemplate,
  createProjectTemplate,
  deleteProjectTemplate,
  projectTemplateQueryOptions,
  projectTemplatesQueryOptions,
  updateProjectTemplate,
} from "./templates";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

describe("projectTemplatesQueryOptions", () => {
  it("reads all project templates", async () => {
    const list = {
      templates: [
        {
          id: 1,
          name: "Standard Onboarding",
          revision: 1,
          milestones: [],
          tasks: [],
          dependencies: [],
          createdAt: "2026-10-31T00:00:00Z",
          updatedAt: "2026-10-31T00:00:00Z",
        },
      ],
    };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, list)));

    const options = projectTemplatesQueryOptions();
    const result = await runQuery(options);

    expect(result).toEqual(list);
    expect(options.queryKey).toEqual(["projects", "templates", "list"]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/templates", { signal: undefined });
  });
});

describe("projectTemplateQueryOptions", () => {
  it("reads a single template by id", async () => {
    const tmpl = {
      id: 5,
      name: "SaaS Launch Blueprint",
      revision: 2,
      milestones: [],
      tasks: [],
      dependencies: [],
      createdAt: "2026-10-31T00:00:00Z",
      updatedAt: "2026-10-31T00:00:00Z",
    };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, tmpl)));

    const options = projectTemplateQueryOptions(5);
    const result = await runQuery(options);

    expect(result).toEqual(tmpl);
    expect(options.queryKey).toEqual(["projects", "templates", "detail", 5]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/templates/5", { signal: undefined });
  });
});

describe("createProjectTemplate", () => {
  it("posts to /api/v1/projects/templates", async () => {
    const created = {
      id: 10,
      name: "New Template",
      revision: 1,
      milestones: [],
      tasks: [],
      dependencies: [],
      createdAt: "2026-10-31T00:00:00Z",
      updatedAt: "2026-10-31T00:00:00Z",
    };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, created)));

    const result = await createProjectTemplate({ name: "New Template" });

    expect(result).toEqual(created);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/templates", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "New Template" }),
    });
  });
});

describe("updateProjectTemplate", () => {
  it("puts to /api/v1/projects/templates/{id}", async () => {
    const updated = {
      id: 10,
      name: "Updated Template",
      revision: 2,
      milestones: [],
      tasks: [],
      dependencies: [],
      createdAt: "2026-10-31T00:00:00Z",
      updatedAt: "2026-10-31T00:00:00Z",
    };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, updated)));

    const result = await updateProjectTemplate(10, { name: "Updated Template", revision: 1 });

    expect(result).toEqual(updated);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/templates/10", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Updated Template", revision: 1 }),
    });
  });
});

describe("deleteProjectTemplate", () => {
  it("deletes /api/v1/projects/templates/{id}", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(new Response(null, { status: 204 })));

    await deleteProjectTemplate(10);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/templates/10", {
      method: "DELETE",
    });
  });
});

describe("applyProjectTemplate", () => {
  it("posts to /api/v1/projects/{projectId}/apply-template", async () => {
    const applyResponse = {
      milestonesCreated: 2,
      tasksCreated: 4,
      dependenciesCreated: 2,
      createdMilestoneIds: [1, 2],
      createdTaskIds: [10, 11, 12, 13],
    };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, applyResponse)));

    const result = await applyProjectTemplate(42, {
      templateId: 5,
      kickoffDate: "2026-11-01",
    });

    expect(result).toEqual(applyResponse);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/42/apply-template", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ templateId: 5, kickoffDate: "2026-11-01" }),
    });
  });
});
