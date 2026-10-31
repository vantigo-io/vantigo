import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { DeliveryMilestone } from "../api/delivery-milestones";
import type { TaskDependency } from "../api/dependencies";
import type { Task } from "../api/tasks";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { TaskTimelineView } from "./-task-timeline-view";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const sampleTasks: Task[] = [
  {
    id: 1,
    projectId: 7,
    title: "Task 1: Spec",
    status: "done",
    position: 1,
    startDate: "2026-11-01",
    dueDate: "2026-11-05",
    checklist: { done: 0, total: 0 },
    commentCount: 0,
    revision: 1,
    createdAt: "2026-10-31T00:00:00Z",
    updatedAt: "2026-10-31T00:00:00Z",
  },
  {
    id: 2,
    projectId: 7,
    title: "Task 2: Build",
    status: "in-progress",
    position: 2,
    startDate: "2026-11-06",
    dueDate: "2026-11-12",
    checklist: { done: 0, total: 0 },
    commentCount: 0,
    revision: 1,
    createdAt: "2026-10-31T00:00:00Z",
    updatedAt: "2026-10-31T00:00:00Z",
  },
  {
    id: 3,
    projectId: 7,
    title: "Task 3: Unscheduled",
    status: "todo",
    position: 3,
    checklist: { done: 0, total: 0 },
    commentCount: 0,
    revision: 1,
    createdAt: "2026-10-31T00:00:00Z",
    updatedAt: "2026-10-31T00:00:00Z",
  },
];

const sampleMilestones: DeliveryMilestone[] = [
  {
    id: 10,
    projectId: 7,
    name: "Phase 1 Delivery",
    targetDate: "2026-11-15",
    status: "open",
    position: 1,
    totalTasks: 2,
    completedTasks: 1,
    progressPercent: 50,
    revision: 1,
    createdAt: "2026-10-31T00:00:00Z",
    updatedAt: "2026-10-31T00:00:00Z",
  },
];

const sampleDependencies: TaskDependency[] = [
  {
    id: 101,
    projectId: 7,
    predecessorTaskId: 1,
    successorTaskId: 2,
    createdAt: "2026-10-31T00:00:00Z",
  },
];

const stubTimelineData = ({
  dependencies = sampleDependencies,
  milestones = sampleMilestones,
}: {
  dependencies?: TaskDependency[];
  milestones?: DeliveryMilestone[];
} = {}) =>
  stubFetch((input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/7/dependencies") {
      return Promise.resolve(jsonResponse(200, { dependencies }));
    }
    if (url.pathname === "/api/v1/projects/7/delivery-milestones") {
      return Promise.resolve(jsonResponse(200, { milestones }));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

describe("TaskTimelineView", () => {
  it("renders tasks and unscheduled labels", async () => {
    stubTimelineData();
    const onOpen = vi.fn();

    renderWithProviders(<TaskTimelineView projectId={7} tasks={sampleTasks} canContribute={true} onOpen={onOpen} />);

    expect(await screen.findAllByText("Task 1: Spec")).toHaveLength(2); // In task list and on bar
    expect(screen.getAllByText("Task 2: Build")).toHaveLength(2);
    expect(screen.getByText("Task 3: Unscheduled")).toBeInTheDocument();
    expect(screen.getByText("Unscheduled tasks")).toBeInTheDocument();
  });

  it("calls onOpen when task is clicked", async () => {
    stubTimelineData();
    const onOpen = vi.fn();

    renderWithProviders(<TaskTimelineView projectId={7} tasks={sampleTasks} canContribute={true} onOpen={onOpen} />);

    const taskElements = await screen.findAllByText("Task 1: Spec");
    await userEvent.click(taskElements[0]);

    expect(onOpen).toHaveBeenCalledWith(1);
  });

  it("identifies schedule conflicts when predecessor finishes after successor starts", async () => {
    // Conflict: t1 ends Nov 10, but t2 starts Nov 6
    const conflictingTasks: Task[] = [
      {
        ...sampleTasks[0],
        startDate: "2026-11-01",
        dueDate: "2026-11-10",
      },
      {
        ...sampleTasks[1],
        startDate: "2026-11-06",
        dueDate: "2026-11-12",
      },
    ];

    stubTimelineData();
    const onOpen = vi.fn();

    renderWithProviders(
      <TaskTimelineView projectId={7} tasks={conflictingTasks} canContribute={true} onOpen={onOpen} />,
    );

    // Conflict legend badge should be visible
    expect(await screen.findByText("Conflict")).toBeInTheDocument();
  });
});
