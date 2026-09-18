import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Project } from "../api/projects";
import type { Task } from "../api/tasks";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { ProjectTasks } from "./project-tasks";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const ADA = "11111111-1111-1111-1111-111111111111";

const project = (canContribute: boolean): Project =>
  ({
    id: 7,
    code: "KVEWEBS",
    name: "Website",
    status: "active",
    billingType: "time-and-materials",
    internal: false,
    capabilities: { canManage: canContribute, canContribute, canSeeFinancials: canContribute },
    billingLinesAvailable: true,
    managers: [],
    revision: 3,
    createdAt: "2026-01-01T10:00:00Z",
    updatedAt: "2026-01-02T10:00:00Z",
  }) as Project;

const task = (overrides: Partial<Task>): Task =>
  ({
    id: 1,
    projectId: 7,
    title: "Write the docs",
    status: "todo",
    position: 1,
    revision: 1,
    checklist: { done: 0, total: 0 },
    commentCount: 0,
    createdAt: "2026-01-01T10:00:00Z",
    updatedAt: "2026-01-02T10:00:00Z",
    ...overrides,
  }) as Task;

const tree: Task[] = [
  task({
    id: 1,
    title: "Write the docs",
    status: "todo",
    assignee: { userId: ADA, displayName: "Ada Lovelace", active: true },
    dueDate: "2026-02-28",
    estimateHours: 4,
    checklist: { done: 1, total: 3 },
    commentCount: 2,
    subtasks: [task({ id: 3, title: "Draft the outline", status: "in-progress", parentTaskId: 1, position: 1 })],
  }),
  task({ id: 2, title: "Ship the release", status: "done", position: 2, subtasks: [] }),
];

const stubTasks = (canContribute: boolean, tasks: Task[] = tree, tasksResponse?: Response) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/7") return Promise.resolve(jsonResponse(200, project(canContribute)));
    if (url.pathname === "/api/v1/projects/7/tasks") {
      return Promise.resolve(tasksResponse ? tasksResponse.clone() : jsonResponse(200, tasks));
    }
    if (url.pathname === "/api/v1/projects/7/roles") return Promise.resolve(jsonResponse(200, []));
    if (url.pathname === "/api/v1/projects/7/assignable-users") return Promise.resolve(jsonResponse(200, []));
    if (url.pathname.startsWith("/api/v1/projects/tasks/")) {
      if (init?.method === "PUT") return Promise.resolve(jsonResponse(200, task({ id: 1, status: "in-progress" })));
      if (url.pathname.endsWith("/checklist")) return Promise.resolve(jsonResponse(200, []));
      if (url.pathname.endsWith("/comments")) {
        return Promise.resolve(jsonResponse(200, { data: [], pagination: { page: 1, totalPages: 1 } }));
      }
      return Promise.resolve(jsonResponse(200, tree[0]));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

describe("ProjectTasks", () => {
  beforeEach(() => localStorage.clear());

  it("groups the tree by status and unfolds a task's subtasks", async () => {
    stubTasks(true);
    renderWithProviders(<ProjectTasks projectId={7} />);

    const todo = await screen.findByTestId("task-group-todo");
    expect(within(todo).getByText("Write the docs")).toBeInTheDocument();
    expect(within(todo).getByText("Ada Lovelace")).toBeInTheDocument();
    expect(within(todo).getByText("1/3")).toBeInTheDocument();
    expect(within(screen.getByTestId("task-group-done")).getByText("Ship the release")).toBeInTheDocument();

    expect(screen.queryByText("Draft the outline")).not.toBeInTheDocument();
    await userEvent.click(within(todo).getByRole("button", { name: "Show subtasks" }));
    expect(await screen.findByText("Draft the outline")).toBeInTheDocument();
  });

  it("opens the drawer on the task a row names", async () => {
    stubTasks(true);
    renderWithProviders(<ProjectTasks projectId={7} />);

    await userEvent.click(await screen.findByRole("button", { name: "Write the docs" }));

    const drawer = await screen.findByRole("dialog", { name: "Task" });
    expect(within(drawer).getByRole("heading", { name: "Write the docs" })).toBeInTheDocument();
  });

  // The host route owns `?task=<id>` (lib/tasks.ts's taskUrl builds it), so a
  // link from My tasks lands here with the drawer already open, and closing it
  // tells the host to drop the intent from the URL again.
  it("opens the drawer on the task the host names and closes it when the host drops it", async () => {
    stubTasks(true);
    const changes: (number | undefined)[] = [];
    const Host = () => {
      const [task, setTask] = useState<number | undefined>(1);
      return (
        <ProjectTasks
          projectId={7}
          openTaskId={task}
          onOpenTaskChange={(id) => {
            changes.push(id);
            setTask(id);
          }}
        />
      );
    };
    renderWithProviders(<Host />);

    const drawer = await screen.findByRole("dialog", { name: "Task" });
    expect(await within(drawer).findByRole("heading", { name: "Write the docs" })).toBeInTheDocument();

    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Task" })).not.toBeInTheDocument());
    expect(changes).toEqual([undefined]);
  });

  it("reports the task a row opened, so the host can put it in the URL", async () => {
    stubTasks(true);
    const onOpenTaskChange = vi.fn();
    renderWithProviders(<ProjectTasks projectId={7} onOpenTaskChange={onOpenTaskChange} />);

    await userEvent.click(await screen.findByRole("button", { name: "Ship the release" }));

    expect(onOpenTaskChange).toHaveBeenCalledWith(2);
    expect(await screen.findByRole("dialog", { name: "Task" })).toBeInTheDocument();
  });

  it("moves a card to another status from the card's menu", async () => {
    const fetchMock = stubTasks(true);
    renderWithProviders(<ProjectTasks projectId={7} />);

    await screen.findByTestId("task-group-todo");
    await userEvent.click(screen.getByRole("radio", { name: "Board" }));

    const card = await screen.findByTestId("task-card-1");
    // Initials are all the card has room for, so the avatar has to say who
    // they belong to for anyone not reading them off the screen.
    expect(within(card).getByLabelText("Ada Lovelace")).toHaveTextContent("AL");
    await userEvent.click(within(card).getByRole("button", { name: "Task actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Move to In progress" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "PUT") ?? [];
      expect(String(url)).toBe("/api/v1/projects/tasks/1");
      expect(JSON.parse(String(init?.body))).toMatchObject({
        title: "Write the docs",
        status: "in-progress",
        assigneeUserId: ADA,
        dueDate: "2026-02-28",
        estimateHours: 4,
        revision: 1,
      });
    });
  });

  it("keeps the chosen view across mounts", async () => {
    stubTasks(true);
    const first = renderWithProviders(<ProjectTasks projectId={7} />);
    await screen.findByTestId("task-group-todo");
    await userEvent.click(screen.getByRole("radio", { name: "Board" }));
    await screen.findByTestId("task-board");
    first.unmount();

    renderWithProviders(<ProjectTasks projectId={7} />);
    expect(await screen.findByTestId("task-board")).toBeInTheDocument();
  });

  it("hides adding and moving tasks from someone who may only read", async () => {
    stubTasks(false);
    renderWithProviders(<ProjectTasks projectId={7} />);

    await screen.findByTestId("task-group-todo");
    expect(screen.queryByRole("button", { name: "Add task" })).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("radio", { name: "Board" }));
    const card = await screen.findByTestId("task-card-1");
    expect(within(card).queryByRole("button", { name: "Task actions" })).not.toBeInTheDocument();
  });

  it("reports a tree it could not read", async () => {
    stubTasks(true, [], jsonResponse(500, { title: "Tasks are unavailable" }));
    renderWithProviders(<ProjectTasks projectId={7} />);

    expect(await screen.findByText("Could not load the tasks")).toBeInTheDocument();
  });

  it("says when the project has no tasks yet", async () => {
    stubTasks(true, []);
    renderWithProviders(<ProjectTasks projectId={7} />);

    expect(await screen.findByText("No tasks yet.")).toBeInTheDocument();
  });

  it("reports a project it cannot read, rather than a board nobody may act on", async () => {
    stubFetch((input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/projects/7") return Promise.resolve(jsonResponse(500, { title: "Unavailable" }));
      return Promise.resolve(jsonResponse(200, tree));
    });
    renderWithProviders(<ProjectTasks projectId={7} />);

    expect(await screen.findByText("Failed to load the project")).toBeInTheDocument();
    expect(screen.queryByText("Write the docs")).not.toBeInTheDocument();
  });
});
