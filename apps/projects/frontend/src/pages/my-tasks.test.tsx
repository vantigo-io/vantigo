import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { MyTask } from "../api/tasks";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { MyTasksPage } from "./my-tasks";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const myTask = (overrides: Partial<MyTask>): MyTask =>
  ({
    id: 12,
    projectId: 7,
    projectCode: "KVEWEBS",
    projectName: "Website",
    title: "Write the docs",
    status: "todo",
    position: 1,
    revision: 3,
    checklist: { done: 0, total: 0 },
    commentCount: 0,
    createdAt: "2026-01-01T10:00:00Z",
    updatedAt: "2026-01-02T10:00:00Z",
    ...overrides,
  }) as MyTask;

const mine: MyTask[] = [
  myTask({ id: 12, dueDate: "2026-02-28", estimateHours: 4 }),
  myTask({
    id: 30,
    projectId: 9,
    projectCode: "ACMEAPP",
    projectName: "Mobile app",
    title: "Ship the beta",
    status: "in-progress",
  }),
];

const stubMyTasks = (list: Response) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/my-tasks") return Promise.resolve(list.clone());
    if (url.pathname.startsWith("/api/v1/projects/tasks/") && init?.method === "PUT") {
      return Promise.resolve(jsonResponse(200, mine[0]));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

describe("MyTasksPage", () => {
  it("lists the caller's open tasks across projects, linking the project and the task", async () => {
    stubMyTasks(jsonResponse(200, mine));
    renderWithProviders(<MyTasksPage />);

    const row = (await screen.findByText("Write the docs")).closest("tr") as HTMLElement;
    expect(within(row).getByRole("link", { name: "KVEWEBS" })).toHaveAttribute("href", "/projects/7");
    expect(within(row).getByRole("link", { name: "Write the docs" })).toHaveAttribute(
      "href",
      "/projects/7/tasks?task=12",
    );
    expect(screen.getByText("Mobile app")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Ship the beta" })).toHaveAttribute("href", "/projects/9/tasks?task=30");
  });

  it("changes a task's status straight from the row", async () => {
    const fetchMock = stubMyTasks(jsonResponse(200, mine));
    renderWithProviders(<MyTasksPage />);

    const row = (await screen.findByText("Write the docs")).closest("tr") as HTMLElement;
    await userEvent.click(within(row).getByRole("combobox", { name: "Change the status of Write the docs" }));
    await userEvent.click(await screen.findByRole("option", { name: "Done" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "PUT") ?? [];
      expect(String(url)).toBe("/api/v1/projects/tasks/12");
      expect(JSON.parse(String(init?.body))).toMatchObject({
        title: "Write the docs",
        status: "done",
        revision: 3,
        dueDate: "2026-02-28",
      });
    });
  });

  it("names the table after the page, and each row's status control after its task", async () => {
    stubMyTasks(jsonResponse(200, mine));
    renderWithProviders(<MyTasksPage />);

    expect(await screen.findByRole("table", { name: "My tasks" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Change the status of Write the docs" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Change the status of Ship the beta" })).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Change status" })).not.toBeInTheDocument();
  });

  it("says when nothing is assigned to the caller", async () => {
    stubMyTasks(jsonResponse(200, []));
    renderWithProviders(<MyTasksPage />);

    expect(await screen.findByText("Nothing assigned to you")).toBeInTheDocument();
  });

  it("reports tasks it could not read", async () => {
    stubMyTasks(jsonResponse(500, { title: "Tasks are unavailable" }));
    renderWithProviders(<MyTasksPage />);

    expect(await screen.findByText("Could not load your tasks")).toBeInTheDocument();
  });
});
