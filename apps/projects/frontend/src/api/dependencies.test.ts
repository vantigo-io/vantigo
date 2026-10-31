import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  createTaskDependency,
  deleteTaskDependency,
  projectDependenciesQueryOptions,
  rescheduleDownstreamTasks,
} from "./dependencies";
import { ApiValidationError } from "./request";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

describe("projectDependenciesQueryOptions", () => {
  it("reads the project's task dependencies", async () => {
    const list = {
      dependencies: [
        {
          id: 101,
          projectId: 42,
          predecessorTaskId: 1,
          successorTaskId: 2,
          createdAt: "2026-10-31T00:00:00Z",
        },
      ],
    };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, list)));

    const options = projectDependenciesQueryOptions(42);
    const result = await runQuery(options);

    expect(result).toEqual(list);
    expect(options.queryKey).toEqual(["projects", "dependencies", "list", 42]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/42/dependencies", { signal: undefined });
  });
});

describe("createTaskDependency", () => {
  it("posts to the project dependencies collection", async () => {
    const created = {
      id: 105,
      projectId: 42,
      predecessorTaskId: 1,
      successorTaskId: 3,
      createdAt: "2026-10-31T00:00:00Z",
    };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, created)));

    const result = await createTaskDependency(42, { predecessorTaskId: 1, successorTaskId: 3 });

    expect(result).toEqual(created);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/42/dependencies", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ predecessorTaskId: 1, successorTaskId: 3 }),
    });
  });

  it("propagates validation problem details on cycle error", async () => {
    const problem = {
      title: "Cyclic dependency",
      status: 400,
      errors: { cycle: ["Adding this dependency creates a cyclic dependency."] },
    };
    stubFetch(() =>
      Promise.resolve(
        new Response(JSON.stringify(problem), {
          status: 400,
          headers: { "Content-Type": "application/problem+json" },
        }),
      ),
    );

    await expect(createTaskDependency(42, { predecessorTaskId: 3, successorTaskId: 1 })).rejects.toBeInstanceOf(
      ApiValidationError,
    );
  });
});

describe("deleteTaskDependency", () => {
  it("sends DELETE to the dependency resource", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(new Response(null, { status: 204 })));

    await deleteTaskDependency(105);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/dependencies/105", {
      method: "DELETE",
    });
  });
});

describe("rescheduleDownstreamTasks", () => {
  it("posts to reschedule downstream endpoint", async () => {
    const response = {
      shiftedTasksCount: 2,
      updatedTaskIds: [2, 3],
    };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, response)));

    const result = await rescheduleDownstreamTasks(1, { daysDelta: 3 });

    expect(result).toEqual(response);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/tasks/1/reschedule-downstream", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ daysDelta: 3 }),
    });
  });
});
