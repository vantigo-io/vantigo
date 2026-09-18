import { describe, expect, it } from "vitest";
import { isTaskStatus, taskStatusColor, taskStatuses, taskStatusLabelKey, taskUrl } from "./tasks";

describe("taskStatuses", () => {
  it("are the three the API accepts, in the order work moves through them", () => {
    expect(taskStatuses).toEqual(["todo", "in-progress", "done"]);
  });
});

describe("taskStatusLabelKey", () => {
  it("names the catalog key of every status", () => {
    expect(taskStatuses.map(taskStatusLabelKey)).toEqual(["taskStatusTodo", "taskStatusInProgress", "taskStatusDone"]);
  });
});

describe("taskStatusColor", () => {
  it("gives each status the one colour every view uses for it", () => {
    expect(taskStatuses.map(taskStatusColor)).toEqual(["gray", "blue", "green"]);
  });
});

describe("taskUrl", () => {
  it("points at the project's Tasks tab, naming the task the drawer should open on", () => {
    expect(taskUrl(7, 12)).toBe("/projects/7/tasks?task=12");
  });
});

describe("isTaskStatus", () => {
  it("accepts the statuses this frontend knows and nothing else", () => {
    expect(isTaskStatus("in-progress")).toBe(true);
    expect(isTaskStatus("done")).toBe(true);
    expect(isTaskStatus("blocked")).toBe(false);
    expect(isTaskStatus("")).toBe(false);
  });
});
