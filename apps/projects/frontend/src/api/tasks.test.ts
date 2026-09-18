import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import { ApiValidationError } from "./request";
import {
  addChecklistItem,
  addComment,
  checklistQueryOptions,
  commentsQueryOptions,
  createTask,
  deleteChecklistItem,
  deleteComment,
  deleteTask,
  moveTask,
  myTasksQueryOptions,
  projectTasksQueryOptions,
  type Task,
  taskQueryOptions,
  taskUpdateFrom,
  updateChecklistItem,
  updateComment,
  updateTask,
} from "./tasks";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

const ADA = "11111111-1111-1111-1111-111111111111";

describe("projectTasksQueryOptions", () => {
  it("reads the project's task tree", async () => {
    const tree = [{ id: 1, title: "Write the docs" }];
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, tree)));

    const options = projectTasksQueryOptions(7);
    const result = await runQuery(options);

    expect(result).toEqual(tree);
    expect(options.queryKey).toEqual(["projects", "tasks", "project", 7, {}]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/tasks", { signal: undefined });
  });

  it("carries only the filters that are set, and keys on them", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));

    const options = projectTasksQueryOptions(7, { status: "in-progress", assigneeUserId: ADA });
    await runQuery(options);

    expect(options.queryKey).toEqual([
      "projects",
      "tasks",
      "project",
      7,
      { status: "in-progress", assigneeUserId: ADA },
    ]);
    expect(fetchMock).toHaveBeenCalledWith(`/api/v1/projects/7/tasks?status=in-progress&assigneeUserId=${ADA}`, {
      signal: undefined,
    });
  });
});

describe("taskQueryOptions", () => {
  it("reads one task with its subtasks", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 12 })));

    const options = taskQueryOptions(12);
    await runQuery(options);

    expect(options.queryKey).toEqual(["projects", "tasks", "detail", 12]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/tasks/12", { signal: undefined });
  });
});

describe("myTasksQueryOptions", () => {
  it("reads the caller's open tasks across projects", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));

    const options = myTasksQueryOptions();
    await runQuery(options);

    expect(options.queryKey).toEqual(["projects", "tasks", "mine"]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/my-tasks", { signal: undefined });
  });
});

describe("checklistQueryOptions", () => {
  it("reads one task's checklist", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));

    const options = checklistQueryOptions(12);
    await runQuery(options);

    expect(options.queryKey).toEqual(["projects", "tasks", "detail", 12, "checklist"]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/tasks/12/checklist", { signal: undefined });
  });
});

describe("commentsQueryOptions", () => {
  it("reads one page of a task's comments", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { data: [], pagination: {} })));

    const options = commentsQueryOptions(12, 2);
    await runQuery(options);

    expect(options.queryKey).toEqual(["projects", "tasks", "detail", 12, "comments", 2]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/tasks/12/comments?page=2&pageSize=20", {
      signal: undefined,
    });
  });
});

describe("createTask", () => {
  it("POSTs the task to the project", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, { id: 1 })));
    const input = { title: "Write the docs", status: "todo" as const };

    await createTask(7, input);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/tasks", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });

  it("throws ApiValidationError with the offending field on a 400", async () => {
    stubFetch(() => Promise.resolve(jsonResponse(400, { title: "Invalid task", errors: { title: ["Too long"] } })));

    const error = await createTask(7, { title: "x" }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiValidationError);
    expect((error as ApiValidationError).fieldErrors).toEqual({ title: "Too long" });
  });
});

describe("updateTask", () => {
  it("PUTs every field of the task with the revision it was read at", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 12 })));
    const input = { title: "Write the docs", status: "done" as const, revision: 3 };

    await updateTask(12, input);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/tasks/12", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });
});

describe("deleteTask", () => {
  it("DELETEs the task", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(new Response(null, { status: 204 })));

    await deleteTask(12);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/tasks/12", { method: "DELETE" });
  });
});

describe("moveTask", () => {
  it("PUTs where the task should sit", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 12 })));

    await moveTask(12, { parentTaskId: 4, position: 2 });

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/tasks/12/position", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ parentTaskId: 4, position: 2 }),
    });
  });
});

describe("checklist mutations", () => {
  it("POSTs, PUTs and DELETEs one item", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 5 })));

    await addChecklistItem(12, { text: "Draft the outline" });
    expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/v1/projects/tasks/12/checklist", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: "Draft the outline" }),
    });

    await updateChecklistItem(12, 5, { done: true });
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/v1/projects/tasks/12/checklist/5", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ done: true }),
    });

    await deleteChecklistItem(12, 5);
    expect(fetchMock).toHaveBeenNthCalledWith(3, "/api/v1/projects/tasks/12/checklist/5", { method: "DELETE" });
  });
});

describe("comment mutations", () => {
  it("POSTs, PUTs and DELETEs one comment", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { id: 9 })));

    await addComment(12, { body: "Looks good" });
    expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/v1/projects/tasks/12/comments", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ body: "Looks good" }),
    });

    await updateComment(12, 9, { body: "Looks better" });
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/v1/projects/tasks/12/comments/9", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ body: "Looks better" }),
    });

    await deleteComment(12, 9);
    expect(fetchMock).toHaveBeenNthCalledWith(3, "/api/v1/projects/tasks/12/comments/9", { method: "DELETE" });
  });
});

describe("taskUpdateFrom", () => {
  const task = {
    title: "Write the docs",
    description: "The public ones",
    status: "todo",
    assignee: { userId: ADA, displayName: "Ada Lovelace", active: true },
    startDate: "2026-02-01",
    dueDate: "2026-02-28",
    estimateHours: 4,
    revision: 3,
  } as Task;

  it("replaces every field the task stands at, so a PUT never drops one", () => {
    expect(taskUpdateFrom(task)).toEqual({
      title: "Write the docs",
      description: "The public ones",
      status: "todo",
      assigneeUserId: ADA,
      startDate: "2026-02-01",
      dueDate: "2026-02-28",
      estimateHours: 4,
      revision: 3,
    });
  });

  it("applies the one change the caller asked for", () => {
    expect(taskUpdateFrom(task, { status: "done" })).toMatchObject({ status: "done", revision: 3 });
  });

  it("clears an assignment the task never had", () => {
    expect(taskUpdateFrom({ ...task, assignee: undefined })).toMatchObject({ assigneeUserId: null });
  });
});
