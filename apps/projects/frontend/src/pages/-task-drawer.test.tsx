import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ChecklistItem, Task, TaskComment } from "../api/tasks";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { TaskDrawer } from "./-task-drawer";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const ADA = "11111111-1111-1111-1111-111111111111";

const task: Task = {
  id: 12,
  projectId: 7,
  title: "Write the docs",
  description: "The public ones",
  status: "todo",
  position: 1,
  revision: 3,
  assignee: { userId: ADA, displayName: "Ada Lovelace", active: true },
  startDate: "2026-02-01",
  dueDate: "2026-02-28",
  estimateHours: 4,
  checklist: { done: 0, total: 1 },
  commentCount: 1,
  createdAt: "2026-01-01T10:00:00Z",
  updatedAt: "2026-01-02T10:00:00Z",
  subtasks: [
    {
      id: 13,
      projectId: 7,
      parentTaskId: 12,
      title: "Draft the outline",
      status: "todo",
      position: 1,
      revision: 1,
      checklist: { done: 0, total: 0 },
      commentCount: 0,
      createdAt: "2026-01-01T10:00:00Z",
      updatedAt: "2026-01-02T10:00:00Z",
    },
  ],
};

const checklist: ChecklistItem[] = [{ id: 5, text: "Outline the pages", done: false, position: 1 }];

const comments: TaskComment[] = [
  {
    id: 9,
    author: { userId: ADA, displayName: "Ada Lovelace", active: true },
    body: "Looks good to me",
    createdAt: "2026-01-03T10:00:00Z",
  },
];

interface StubOptions {
  update?: Response;
  totalPages?: number;
}

const stubTask = ({ update, totalPages = 1 }: StubOptions = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const page = Number(url.searchParams.get("page") ?? 1);
    if (url.pathname === "/api/v1/projects/tasks/12") {
      if (init?.method === "PUT") return Promise.resolve(update ? update.clone() : jsonResponse(200, task));
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(200, task));
    }
    if (url.pathname === "/api/v1/projects/tasks/12/checklist") return Promise.resolve(jsonResponse(200, checklist));
    if (url.pathname.startsWith("/api/v1/projects/tasks/12/checklist/")) {
      return Promise.resolve(jsonResponse(200, { ...checklist[0], done: true }));
    }
    if (url.pathname === "/api/v1/projects/tasks/12/comments") {
      if (init?.method === "POST") return Promise.resolve(jsonResponse(201, comments[0]));
      return Promise.resolve(
        jsonResponse(200, {
          data: page === 1 ? comments : [{ ...comments[0], id: 10, body: "And one more" }],
          pagination: {
            page,
            pageSize: 20,
            totalCount: totalPages,
            totalPages,
            hasNextPage: page < totalPages,
            hasPreviousPage: page > 1,
          },
        }),
      );
    }
    if (url.pathname.startsWith("/api/v1/projects/tasks/12/comments/")) {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(200, { ...comments[0], body: "Looks better" }));
    }
    if (url.pathname === "/api/v1/projects/7/tasks") return Promise.resolve(jsonResponse(200, [task]));
    if (url.pathname === "/api/v1/projects/7/roles") return Promise.resolve(jsonResponse(200, []));
    if (url.pathname === "/api/v1/projects/7/assignable-users") return Promise.resolve(jsonResponse(200, []));
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const renderDrawer = (props: Partial<Parameters<typeof TaskDrawer>[0]> = {}) =>
  renderWithProviders(
    <TaskDrawer
      projectId={7}
      taskId={12}
      opened
      onClose={vi.fn()}
      canContribute
      canManage={false}
      currentUserId={ADA}
      {...props}
    />,
  );

describe("TaskDrawer", () => {
  it("renames the task, carrying the revision it was read at", async () => {
    const fetchMock = stubTask();
    renderDrawer();

    await screen.findByRole("heading", { name: "Write the docs" });
    await userEvent.click(screen.getByRole("button", { name: "Edit the title" }));
    const input = screen.getByRole("textbox", { name: "Title" });
    await userEvent.clear(input);
    await userEvent.type(input, "Write the manual");
    await userEvent.click(screen.getByRole("button", { name: "Save the title" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "PUT") ?? [];
      expect(String(url)).toBe("/api/v1/projects/tasks/12");
      expect(JSON.parse(String(init?.body))).toMatchObject({ title: "Write the manual", revision: 3, status: "todo" });
    });
  });

  it("says the task was changed elsewhere when the revision has moved on", async () => {
    stubTask({ update: jsonResponse(409, { title: "Revision conflict" }) });
    renderDrawer();

    await screen.findByRole("heading", { name: "Write the docs" });
    await userEvent.click(screen.getByRole("button", { name: "Edit the title" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Title" }), "!");
    await userEvent.click(screen.getByRole("button", { name: "Save the title" }));

    expect(
      await screen.findByText("The task was changed by someone else. Reload it and try again."),
    ).toBeInTheDocument();
  });

  it("saves the details against the revision the form was seeded from, not a newer one", async () => {
    // The drawer reads the task at revision 3, the caller starts editing, and
    // ticking a checklist item invalidates and refetches a task somebody else
    // has since moved to revision 4. The fields in the form are still the old
    // ones, so the save has to carry revision 3 and be refused.
    let revision = 3;
    const fetchMock = stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/projects/tasks/12") {
        if (init?.method === "PUT") return Promise.resolve(jsonResponse(409, { title: "Revision conflict" }));
        return Promise.resolve(jsonResponse(200, { ...task, revision }));
      }
      if (url.pathname === "/api/v1/projects/tasks/12/checklist") return Promise.resolve(jsonResponse(200, checklist));
      if (url.pathname.startsWith("/api/v1/projects/tasks/12/checklist/")) {
        revision = 4;
        return Promise.resolve(jsonResponse(200, { ...checklist[0], done: true }));
      }
      if (url.pathname === "/api/v1/projects/tasks/12/comments") {
        return Promise.resolve(
          jsonResponse(200, { data: comments, pagination: { page: 1, totalCount: 1, totalPages: 1 } }),
        );
      }
      if (url.pathname === "/api/v1/projects/7/roles") return Promise.resolve(jsonResponse(200, []));
      if (url.pathname === "/api/v1/projects/7/assignable-users") return Promise.resolve(jsonResponse(200, []));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderDrawer();

    const reads = () =>
      fetchMock.actualCalls.filter(
        ([url, init]) => String(url) === "/api/v1/projects/tasks/12" && (init?.method ?? "GET") === "GET",
      );
    await screen.findByRole("heading", { name: "Write the docs" });
    expect(reads()).toHaveLength(1);

    await userEvent.click(await screen.findByRole("checkbox", { name: "Outline the pages" }));
    await waitFor(() => expect(reads().length).toBeGreaterThan(1));

    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      const [, init] =
        fetchMock.actualCalls.find(
          ([url, request]) => String(url) === "/api/v1/projects/tasks/12" && request?.method === "PUT",
        ) ?? [];
      expect(JSON.parse(String(init?.body))).toMatchObject({ revision: 3, title: "Write the docs" });
    });
    expect(
      await screen.findByText("The task was changed by someone else. Reload it and try again."),
    ).toBeInTheDocument();
  });

  it("adds a subtask under the task it is opened on", async () => {
    const fetchMock = stubTask();
    renderDrawer();

    await screen.findByText("Draft the outline");
    await userEvent.type(screen.getByRole("textbox", { name: "Subtask title" }), "Review the draft");
    await userEvent.click(screen.getByRole("button", { name: "Add subtask" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "POST") ?? [];
      expect(String(url)).toBe("/api/v1/projects/7/tasks");
      expect(JSON.parse(String(init?.body))).toMatchObject({ title: "Review the draft", parentTaskId: 12 });
    });
  });

  it("offers no subtask section on a task that is itself a subtask", async () => {
    // One level of nesting: the API refuses a subtask of a subtask, so the
    // drawer must not offer the add at all — and a subtask can never have
    // children of its own, so the whole section goes.
    const subtask = (task.subtasks ?? [])[0];
    stubFetch((input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/projects/tasks/13") return Promise.resolve(jsonResponse(200, subtask));
      if (url.pathname === "/api/v1/projects/tasks/13/checklist") return Promise.resolve(jsonResponse(200, []));
      if (url.pathname === "/api/v1/projects/tasks/13/comments") {
        return Promise.resolve(
          jsonResponse(200, { data: [], pagination: { page: 1, totalCount: 0, totalPages: 0, hasNextPage: false } }),
        );
      }
      if (url.pathname === "/api/v1/projects/7/roles") return Promise.resolve(jsonResponse(200, []));
      if (url.pathname === "/api/v1/projects/7/assignable-users") return Promise.resolve(jsonResponse(200, []));
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderDrawer({ taskId: 13 });

    await screen.findByRole("heading", { name: "Draft the outline" });
    expect(screen.queryByText("Subtasks")).not.toBeInTheDocument();
    expect(screen.queryByText("No subtasks yet.")).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Subtask title" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add subtask" })).not.toBeInTheDocument();
  });

  it("asks before deleting the task, warning what goes with it", async () => {
    const fetchMock = stubTask();
    renderDrawer({ canManage: true });

    await screen.findByRole("heading", { name: "Write the docs" });
    await userEvent.click(screen.getByRole("button", { name: "Delete task" }));

    const confirm = await screen.findByRole("dialog", { name: "Delete Write the docs?" });
    expect(
      within(confirm).getByText("Its subtasks, checklist and comments go with it. This cannot be undone."),
    ).toBeInTheDocument();
    await userEvent.click(within(confirm).getByRole("button", { name: "Delete task" }));

    await waitFor(() => {
      const [url] = fetchMock.actualCalls.find(([, request]) => request?.method === "DELETE") ?? [];
      expect(String(url)).toBe("/api/v1/projects/tasks/12");
    });
  });

  it("hides every action from someone who may only read", async () => {
    stubTask();
    renderDrawer({ canContribute: false, canManage: false });

    await screen.findByRole("heading", { name: "Write the docs" });
    expect(await screen.findByRole("checkbox", { name: "Outline the pages" })).toBeDisabled();
    expect(await screen.findByText("Looks good to me")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit the title" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Post comment" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add subtask" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete task" })).not.toBeInTheDocument();
  });

  it("reports a task it could not read", async () => {
    stubFetch(() => Promise.resolve(jsonResponse(500, { title: "Tasks are unavailable" })));
    renderDrawer();

    expect(await screen.findByText("Could not load the task")).toBeInTheDocument();
  });
});
