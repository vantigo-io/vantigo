import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
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
  conflict = false,
}: {
  dependencies?: TaskDependency[];
  milestones?: DeliveryMilestone[];
  /** Every task save answers 409, as a revision that moved on does. */
  conflict?: boolean;
} = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const taskPut = url.pathname.match(/^\/api\/v1\/projects\/tasks\/(\d+)$/);
    if (taskPut && init?.method === "PUT" && conflict) {
      return Promise.resolve(jsonResponse(409, { title: "Revision conflict", status: 409 }));
    }
    if (taskPut && init?.method === "PUT") {
      const body = JSON.parse(String(init.body));
      const task = sampleTasks.find((tk) => tk.id === Number(taskPut[1]));
      return Promise.resolve(jsonResponse(200, { ...task, ...body, revision: (task?.revision ?? 0) + 1 }));
    }
    if (url.pathname.endsWith("/reschedule-downstream")) {
      return Promise.resolve(jsonResponse(200, { shiftedTasksCount: 1, updatedTaskIds: [2] }));
    }
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

/** The PUT and POST bodies the timeline sent, with their paths. */
const writes = (fetchMock: ReturnType<typeof stubFetch>) =>
  fetchMock.actualCalls
    .filter(([, init]) => init?.method === "PUT" || init?.method === "POST")
    .map(([input, init]) => ({
      method: init?.method,
      path: new URL(String(input), "http://localhost").pathname,
      body: JSON.parse(String(init?.body)),
    }));

/** Lets a mutation a drop would have started reach fetch, so "nothing was sent" means it. */
const settle = () => new Promise((resolve) => setTimeout(resolve, 50));

// Day zoom is 40px a day: a drag of 80px is two days.
const dragBar = (handle: HTMLElement, bar: HTMLElement, fromX: number, toX: number) => {
  fireEvent.pointerDown(handle, { pointerId: 1, button: 0, clientX: fromX });
  fireEvent.pointerMove(bar, { pointerId: 1, clientX: toX });
  fireEvent.pointerUp(bar, { pointerId: 1, clientX: toX });
};

describe("TaskTimelineView zoom and rescheduling", () => {
  // A storage of our own: the zoom is remembered per viewer, and Node's own
  // global localStorage can shadow jsdom's.
  beforeEach(() => {
    const store = new Map<string, string>();
    vi.stubGlobal("localStorage", {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => store.set(key, value),
      removeItem: (key: string) => store.delete(key),
      clear: () => store.clear(),
    });
  });

  // Waits for the links and milestones to load: who depends on whom is what
  // the downstream question is asked from.
  const renderTimeline = async (canContribute = true, onOpen: (taskId: number) => void = vi.fn()) => {
    const rendered = renderWithProviders(
      <TaskTimelineView projectId={7} tasks={sampleTasks} canContribute={canContribute} onOpen={onOpen} />,
    );
    await waitFor(() => expect(rendered.queryClient.isFetching()).toBe(0));
    return rendered;
  };

  it("switches between day, week and month and remembers the choice", async () => {
    stubTimelineData();
    const { unmount } = await renderTimeline();

    expect(await screen.findByRole("radio", { name: "Day" })).toBeChecked();
    expect(screen.queryByText("W45")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("radio", { name: "Week" }));
    expect(screen.getByText("W45")).toBeInTheDocument();
    expect(localStorage.getItem("projects.tasks.timelineZoom")).toBe("week");

    await userEvent.click(screen.getByRole("radio", { name: "Month" }));
    expect(screen.getByText("Nov 2026")).toBeInTheDocument();
    expect(screen.queryByText("W45")).not.toBeInTheDocument();

    unmount();
    await renderTimeline();
    expect(await screen.findByRole("radio", { name: "Month" })).toBeChecked();
  });

  it("moves both dates when the bar's body is dragged, and saves them with the revision", async () => {
    const fetchMock = stubTimelineData({ dependencies: [] });
    await renderTimeline();
    const bar = await screen.findByTestId("timeline-bar-1");

    fireEvent.pointerDown(bar, { pointerId: 1, button: 0, clientX: 100 });
    fireEvent.pointerMove(bar, { pointerId: 1, clientX: 180 });
    expect(screen.getByRole("status")).toHaveTextContent("Nov 3, 2026 – Nov 7, 2026");
    fireEvent.pointerUp(bar, { pointerId: 1, clientX: 180 });

    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1));
    expect(writes(fetchMock)[0]).toMatchObject({
      method: "PUT",
      path: "/api/v1/projects/tasks/1",
      body: { title: "Task 1: Spec", startDate: "2026-11-03", dueDate: "2026-11-07", revision: 1 },
    });
  });

  it("moves only the due date with the right edge, and only the start with the left", async () => {
    const fetchMock = stubTimelineData({ dependencies: [] });
    await renderTimeline();
    const bar = await screen.findByTestId("timeline-bar-2");

    dragBar(screen.getByTestId("timeline-bar-2-end"), bar, 300, 260);
    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1));
    expect(writes(fetchMock)[0].body).toMatchObject({ startDate: "2026-11-06", dueDate: "2026-11-11" });

    const first = screen.getByTestId("timeline-bar-1");
    dragBar(screen.getByTestId("timeline-bar-1-start"), first, 100, 20);
    await waitFor(() => expect(writes(fetchMock)).toHaveLength(2));
    expect(writes(fetchMock)[1].body).toMatchObject({ startDate: "2026-10-30", dueDate: "2026-11-05" });
  });

  it("cancels a drag on Escape and saves nothing", async () => {
    const fetchMock = stubTimelineData();
    await renderTimeline();
    const bar = await screen.findByTestId("timeline-bar-1");

    fireEvent.pointerDown(bar, { pointerId: 1, button: 0, clientX: 100 });
    fireEvent.pointerMove(bar, { pointerId: 1, clientX: 220 });
    expect(screen.getByRole("status")).toBeInTheDocument();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    fireEvent.pointerUp(bar, { pointerId: 1, clientX: 220 });

    await settle();
    expect(writes(fetchMock)).toHaveLength(0);
  });

  it("saves nothing when the bar ends where it started", async () => {
    const fetchMock = stubTimelineData();
    await renderTimeline();
    const bar = await screen.findByTestId("timeline-bar-1");

    dragBar(bar, bar, 100, 110); // a quarter of a day snaps back to none
    fireEvent.pointerDown(bar, { pointerId: 1, button: 0, clientX: 100 });
    fireEvent.pointerMove(bar, { pointerId: 1, clientX: 200 });
    fireEvent.pointerMove(bar, { pointerId: 1, clientX: 100 });
    fireEvent.pointerUp(bar, { pointerId: 1, clientX: 100 });

    await settle();
    expect(writes(fetchMock)).toHaveLength(0);
  });

  it("asks before shifting the dependent tasks, and shifts them by the due date's move", async () => {
    const fetchMock = stubTimelineData();
    await renderTimeline();
    const bar = await screen.findByTestId("timeline-bar-1");

    dragBar(bar, bar, 100, 180);

    expect(await screen.findByText("Shift 1 dependent task 2 days later?")).toBeInTheDocument();
    expect(writes(fetchMock)).toHaveLength(1);
    await userEvent.click(screen.getByRole("button", { name: "Shift dependent tasks" }));

    await waitFor(() => expect(writes(fetchMock)).toHaveLength(2));
    expect(writes(fetchMock)[1]).toEqual({
      method: "POST",
      path: "/api/v1/projects/tasks/1/reschedule-downstream",
      body: { daysDelta: 2 },
    });
    expect(await screen.findByText("Shifted 1 downstream task 2 days later")).toBeInTheDocument();
  });

  it("counts only the dependent tasks that have a date to shift", async () => {
    // Task 3 depends on task 1 too, but has neither date: the server leaves it alone.
    stubTimelineData({
      dependencies: [...sampleDependencies, { ...sampleDependencies[0], id: 102, successorTaskId: 3 }],
    });
    await renderTimeline();
    const bar = await screen.findByTestId("timeline-bar-1");

    dragBar(bar, bar, 100, 140);

    expect(await screen.findByText("Shift 1 dependent task 1 day later?")).toBeInTheDocument();
  });

  it("rereads the tasks after a 409 and lets the bar be moved again", async () => {
    const fetchMock = stubTimelineData({ dependencies: [], conflict: true });
    const { queryClient } = await renderTimeline();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    const bar = await screen.findByTestId("timeline-bar-1");
    const before = bar.style.left;

    dragBar(bar, bar, 100, 180);

    expect(
      await screen.findByText("The task was changed by someone else. It has been reloaded; try again."),
    ).toBeInTheDocument();
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["projects", "tasks"] });
    await waitFor(() => expect(screen.getByTestId("timeline-bar-1").style.left).toBe(before));

    dragBar(screen.getByTestId("timeline-bar-1"), screen.getByTestId("timeline-bar-1"), 100, 140);
    await waitFor(() => expect(writes(fetchMock)).toHaveLength(2));
  });

  it("keeps each saved move in place until the list catches up, not just the latest", async () => {
    // The stub answers the saves, but the tasks prop never refreshes: both
    // moves stay pending, as when their refetches are still in flight.
    const fetchMock = stubTimelineData({ dependencies: [] });
    await renderTimeline();
    const first = await screen.findByTestId("timeline-bar-1");
    const firstLeft = Number.parseFloat(first.style.left);

    dragBar(first, first, 100, 180);
    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1));
    await settle();
    const second = screen.getByTestId("timeline-bar-2");
    dragBar(second, second, 300, 340);
    await waitFor(() => expect(writes(fetchMock)).toHaveLength(2));
    await settle();

    expect(Number.parseFloat(screen.getByTestId("timeline-bar-1").style.left)).toBe(firstLeft + 80);
    // Still holding the revision its save was made from, the first bar cannot be moved again.
    dragBar(screen.getByTestId("timeline-bar-1"), screen.getByTestId("timeline-bar-1"), 100, 180);
    await settle();
    expect(writes(fetchMock)).toHaveLength(2);
  });

  it("treats a press under the drag threshold as a click, even at the month scale", async () => {
    const fetchMock = stubTimelineData();
    const onOpen = vi.fn();
    await renderTimeline(true, onOpen);
    await userEvent.click(screen.getByRole("radio", { name: "Month" }));
    const bar = screen.getByTestId("timeline-bar-1");

    dragBar(bar, bar, 100, 103); // 3px is more than half a 5px day, but not a drag
    fireEvent.click(bar);

    await settle();
    expect(writes(fetchMock)).toHaveLength(0);
    expect(onOpen).toHaveBeenCalledWith(1);
  });

  it("does not swallow the next click after an Escape or a cancelled pointer", async () => {
    stubTimelineData();
    const onOpen = vi.fn();
    await renderTimeline(true, onOpen);
    const bar = await screen.findByTestId("timeline-bar-1");

    fireEvent.pointerDown(bar, { pointerId: 1, button: 0, clientX: 100 });
    fireEvent.pointerMove(bar, { pointerId: 1, clientX: 200 });
    fireEvent.keyDown(window, { key: "Escape" });
    // The pointer is let go elsewhere: no click reaches the bar.
    dragBar(bar, bar, 100, 100);
    fireEvent.click(bar);
    expect(onOpen).toHaveBeenCalledTimes(1);

    fireEvent.pointerDown(bar, { pointerId: 2, button: 0, clientX: 100 });
    fireEvent.pointerMove(bar, { pointerId: 2, clientX: 200 });
    fireEvent.pointerCancel(bar, { pointerId: 2 });
    fireEvent.click(bar);
    expect(onOpen).toHaveBeenCalledTimes(2);
  });

  it("leaves the dependent tasks alone when told to", async () => {
    const fetchMock = stubTimelineData();
    await renderTimeline();
    const bar = await screen.findByTestId("timeline-bar-1");

    dragBar(bar, bar, 100, 60);

    expect(await screen.findByText("Shift 1 dependent task 1 day earlier?")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Leave them" }));

    await settle();
    expect(writes(fetchMock).map((w) => w.method)).toEqual(["PUT"]);
    expect(screen.queryByText("Shift 1 dependent task 1 day earlier?")).not.toBeInTheDocument();
  });

  it("does not ask when only the start moved", async () => {
    const fetchMock = stubTimelineData();
    await renderTimeline();
    const bar = await screen.findByTestId("timeline-bar-1");

    dragBar(screen.getByTestId("timeline-bar-1-start"), bar, 100, 140);

    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1));
    await settle();
    expect(screen.queryByText(/dependent task/)).not.toBeInTheDocument();
  });

  it("moves a focused bar a day with Alt+Arrow and asks the same question", async () => {
    const fetchMock = stubTimelineData();
    await renderTimeline();
    const bar = await screen.findByRole("button", { name: /^Task 1: Spec, .*Alt\+Left or Alt\+Right/ });

    bar.focus();
    fireEvent.keyDown(bar, { key: "ArrowRight" }); // no Alt: nothing moves
    fireEvent.keyDown(bar, { key: "ArrowRight", altKey: true });

    await waitFor(() => expect(writes(fetchMock)).toHaveLength(1));
    expect(writes(fetchMock)[0].body).toMatchObject({ startDate: "2026-11-02", dueDate: "2026-11-06" });
    expect(await screen.findByText("Shift 1 dependent task 1 day later?")).toBeInTheDocument();
  });

  it("lets a viewer look but not move", async () => {
    const fetchMock = stubTimelineData();
    await renderTimeline(false);
    const bar = await screen.findByTestId("timeline-bar-1");

    expect(screen.queryByTestId("timeline-bar-1-end")).not.toBeInTheDocument();
    expect(bar).toHaveAccessibleName("Task 1: Spec, Nov 1, 2026 – Nov 5, 2026");
    dragBar(bar, bar, 100, 180);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    fireEvent.keyDown(bar, { key: "ArrowRight", altKey: true });

    await settle();
    expect(writes(fetchMock)).toHaveLength(0);
  });
});
