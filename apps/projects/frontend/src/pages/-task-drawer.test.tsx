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
const ALAN = "22222222-2222-2222-2222-222222222222";

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

  it("ticks a checklist item off", async () => {
    const fetchMock = stubTask();
    renderDrawer();

    await userEvent.click(await screen.findByRole("checkbox", { name: "Outline the pages" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "PUT") ?? [];
      expect(String(url)).toBe("/api/v1/projects/tasks/12/checklist/5");
      expect(JSON.parse(String(init?.body))).toEqual({ done: true });
    });
  });

  it("posts a comment", async () => {
    const fetchMock = stubTask();
    renderDrawer();

    await screen.findByText("Looks good to me");
    await userEvent.type(screen.getByRole("textbox", { name: "Write a comment…" }), "One more thing");
    await userEvent.click(screen.getByRole("button", { name: "Post comment" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "POST") ?? [];
      expect(String(url)).toBe("/api/v1/projects/tasks/12/comments");
      expect(JSON.parse(String(init?.body))).toEqual({ body: "One more thing" });
    });
  });

  it("loads the next page of comments on demand", async () => {
    stubTask({ totalPages: 2 });
    renderDrawer();

    await screen.findByText("Looks good to me");
    await userEvent.click(screen.getByRole("button", { name: "Load more" }));

    expect(await screen.findByText("And one more")).toBeInTheDocument();
    expect(screen.getByText("Looks good to me")).toBeInTheDocument();
  });

  it("offers deleting a comment only to its author or a manager", async () => {
    stubTask();
    const own = renderDrawer();
    await screen.findByText("Looks good to me");
    expect(screen.getByRole("button", { name: "Delete the comment" })).toBeInTheDocument();
    own.unmount();

    stubTask();
    const other = renderDrawer({ currentUserId: ALAN });
    await screen.findByText("Looks good to me");
    expect(screen.queryByRole("button", { name: "Delete the comment" })).not.toBeInTheDocument();
    other.unmount();

    stubTask();
    renderDrawer({ currentUserId: ALAN, canManage: true });
    await screen.findByText("Looks good to me");
    expect(screen.getByRole("button", { name: "Delete the comment" })).toBeInTheDocument();
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
