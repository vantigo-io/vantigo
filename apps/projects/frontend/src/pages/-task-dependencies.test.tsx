import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { TaskDependency } from "../api/dependencies";
import type { Task } from "../api/tasks";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { TaskDependencies } from "./-task-dependencies";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const mainTask: Task = {
  id: 2,
  projectId: 7,
  title: "Main Task: Architecture",
  status: "in-progress",
  position: 2,
  startDate: "2026-11-05",
  dueDate: "2026-11-10",
  checklist: { done: 0, total: 0 },
  commentCount: 0,
  revision: 1,
  createdAt: "2026-10-31T00:00:00Z",
  updatedAt: "2026-10-31T00:00:00Z",
};

const predTask: Task = {
  id: 1,
  projectId: 7,
  title: "Predecessor: Research",
  status: "done",
  position: 1,
  startDate: "2026-11-01",
  dueDate: "2026-11-04",
  checklist: { done: 0, total: 0 },
  commentCount: 0,
  revision: 1,
  createdAt: "2026-10-31T00:00:00Z",
  updatedAt: "2026-10-31T00:00:00Z",
};

const succTask: Task = {
  id: 3,
  projectId: 7,
  title: "Successor: Implementation",
  status: "todo",
  position: 3,
  startDate: "2026-11-12",
  dueDate: "2026-11-20",
  checklist: { done: 0, total: 0 },
  commentCount: 0,
  revision: 1,
  createdAt: "2026-10-31T00:00:00Z",
  updatedAt: "2026-10-31T00:00:00Z",
};

const allProjectTasks: Task[] = [predTask, mainTask, succTask];

const sampleDeps: TaskDependency[] = [
  {
    id: 101,
    projectId: 7,
    predecessorTaskId: 1,
    successorTaskId: 2,
    createdAt: "2026-10-31T00:00:00Z",
  },
  {
    id: 102,
    projectId: 7,
    predecessorTaskId: 2,
    successorTaskId: 3,
    createdAt: "2026-10-31T00:00:00Z",
  },
];

const stubDependencyData = (deps: TaskDependency[] = sampleDeps) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/7/dependencies") {
      return Promise.resolve(jsonResponse(200, { dependencies: deps }));
    }
    if (url.pathname === "/api/v1/projects/7/tasks") {
      return Promise.resolve(jsonResponse(200, allProjectTasks));
    }
    if (url.pathname === "/api/v1/projects/dependencies/101" && init?.method === "DELETE") {
      return Promise.resolve(new Response(null, { status: 204 }));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

describe("TaskDependencies", () => {
  it("renders empty state when there are no dependencies", async () => {
    stubDependencyData([]);

    renderWithProviders(<TaskDependencies projectId={7} task={mainTask} canContribute={true} />);

    expect(await screen.findByText("No predecessors.")).toBeInTheDocument();
    expect(screen.getByText("No successors.")).toBeInTheDocument();
  });

  it("renders linked predecessor and successor tasks", async () => {
    stubDependencyData();

    renderWithProviders(<TaskDependencies projectId={7} task={mainTask} canContribute={true} />);

    expect((await screen.findAllByText("Predecessor: Research")).length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Successor: Implementation").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("Reschedule downstream")).toBeInTheDocument();
  });

  it("shows schedule conflict warning when dates overlap", async () => {
    // Conflict: predTask dueDate is Nov 8, but mainTask startDate is Nov 5
    const conflictingPred: Task = {
      ...predTask,
      dueDate: "2026-11-08",
    };

    stubFetch((input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/projects/7/dependencies") {
        return Promise.resolve(jsonResponse(200, { dependencies: [sampleDeps[0]] }));
      }
      if (url.pathname === "/api/v1/projects/7/tasks") {
        return Promise.resolve(jsonResponse(200, [conflictingPred, mainTask]));
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });

    renderWithProviders(<TaskDependencies projectId={7} task={mainTask} canContribute={true} />);

    expect(await screen.findByText("Conflict")).toBeInTheDocument();
    expect(screen.getByText("Schedule conflict: predecessor finishes after successor starts")).toBeInTheDocument();
  });
});
