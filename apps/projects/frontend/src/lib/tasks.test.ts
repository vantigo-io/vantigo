import { describe, expect, it } from "vitest";
import { excerptForLabel, isTaskStatus, taskStatusColor, taskStatuses, taskStatusLabelKey, taskUrl } from "./tasks";

describe("excerptForLabel", () => {
  it("leaves short text alone", () => {
    expect(excerptForLabel("Buy cable")).toBe("Buy cable");
  });

  it("cuts long text to 60 characters and marks it with an ellipsis", () => {
    const text = "x".repeat(80);
    expect(excerptForLabel(text)).toBe(`${"x".repeat(60)}…`);
  });

  // slice() on a JS string counts UTF-16 code units, which splits a surrogate
  // pair — an emoji sitting right on the cut would come out as a lone,
  // unpaired surrogate. Counting code points instead keeps it whole.
  it("cuts by code point, not UTF-16 code unit, so an emoji at the boundary is kept whole", () => {
    const text = `${"x".repeat(59)}😀y`;
    expect(excerptForLabel(text)).toBe(`${"x".repeat(59)}😀…`);
  });
});

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
