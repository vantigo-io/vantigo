import { describe, expect, it } from "vitest";
import type { ProjectTemplate } from "../api/templates";
import {
  dependenciesHaveCycle,
  draftFromTemplate,
  emptyChecklistItem,
  emptyDependency,
  emptyMilestone,
  emptyTask,
  formPathFromServerField,
  longestDependencyChain,
  moveRow,
  removeMilestone,
  removeTask,
  type TemplateDraft,
  templateChecklistCount,
  templateInputFromDraft,
  templateSubtaskCount,
  validateTemplateDraft,
} from "./templates";

const stored: ProjectTemplate = {
  id: 7,
  name: "Onboarding",
  description: "Three weeks",
  billingType: null,
  milestones: [
    { name: "Kickoff", description: null, targetDayOffset: 0 },
    { name: "Handover", description: "Done", targetDayOffset: 21 },
  ],
  tasks: [
    { title: "Discovery", startDayOffset: 0, dueDayOffset: 3, milestoneIndex: 0, subtasks: [{ title: "Agenda" }] },
    { title: "Design", startDayOffset: 4, dueDayOffset: 10, milestoneIndex: 1, estimateHours: 12.5 },
    { title: "Build", milestoneIndex: 1 },
  ],
  dependencies: [
    { predecessorTaskIndex: 0, successorTaskIndex: 1 },
    { predecessorTaskIndex: 1, successorTaskIndex: 2 },
  ],
  revision: 3,
  createdAt: "2026-10-31T00:00:00Z",
  updatedAt: "2026-10-31T00:00:00Z",
};

describe("draftFromTemplate and templateInputFromDraft", () => {
  it("round-trips a stored template to the same indices", () => {
    const input = templateInputFromDraft(draftFromTemplate(stored));
    expect(input.milestones).toEqual([
      { name: "Kickoff", description: undefined, targetDayOffset: 0 },
      { name: "Handover", description: "Done", targetDayOffset: 21 },
    ]);
    expect(input.tasks?.map((task) => [task.title, task.milestoneIndex])).toEqual([
      ["Discovery", 0],
      ["Design", 1],
      ["Build", 1],
    ]);
    expect(input.tasks?.[0]?.subtasks).toEqual([
      {
        title: "Agenda",
        description: undefined,
        startDayOffset: undefined,
        dueDayOffset: undefined,
        estimateHours: undefined,
      },
    ]);
    expect(input.tasks?.[1]?.estimateHours).toBe(12.5);
    expect(input.dependencies).toEqual(stored.dependencies);
  });

  it("carries checklists through in order, trimmed, and leaves an empty one out", () => {
    const draft = draftFromTemplate({
      ...stored,
      tasks: [
        { title: "A", checklist: ["One", "Two"], subtasks: [{ title: "a", checklist: ["Sub"] }] },
        { title: "B" },
      ],
      dependencies: [],
    });
    const first = draft.tasks[0];
    if (!first) throw new Error("fixture");
    expect(first.checklist.map((item) => item.text)).toEqual(["One", "Two"]);
    first.checklist = moveRow(first.checklist, 1, 0);
    first.checklist.push({ ...emptyChecklistItem(), text: "  Three  " });
    const input = templateInputFromDraft(draft);
    expect(input.tasks?.[0]?.checklist).toEqual(["Two", "One", "Three"]);
    expect(input.tasks?.[0]?.subtasks?.[0]?.checklist).toEqual(["Sub"]);
    expect(input.tasks?.[1]?.checklist).toBeUndefined();
  });

  it("starts a new template empty", () => {
    expect(draftFromTemplate()).toEqual({ name: "", description: "", milestones: [], tasks: [], dependencies: [] });
  });

  it("trims what it sends and drops blank optional text", () => {
    const draft = draftFromTemplate();
    draft.name = "  Spaced  ";
    draft.description = "   ";
    draft.tasks = [{ ...emptyTask(), title: " A ", startDayOffset: "", estimateHours: "" }];
    const input = templateInputFromDraft(draft);
    expect(input.name).toBe("Spaced");
    expect(input.description).toBeUndefined();
    expect(input.tasks?.[0]).toMatchObject({ title: "A", startDayOffset: undefined, estimateHours: undefined });
  });
});

describe("moving and removing rows", () => {
  it("re-points milestone links when a milestone moves", () => {
    const draft = draftFromTemplate(stored);
    const moved = { ...draft, milestones: moveRow(draft.milestones, 1, 0) };
    const input = templateInputFromDraft(moved);
    expect(input.milestones?.map((m) => m.name)).toEqual(["Handover", "Kickoff"]);
    // Discovery still belongs to Kickoff, now at index 1; Design and Build to Handover, now at 0.
    expect(input.tasks?.map((task) => task.milestoneIndex)).toEqual([1, 0, 0]);
  });

  it("re-points dependencies when a task moves", () => {
    const draft = draftFromTemplate(stored);
    const moved = { ...draft, tasks: moveRow(draft.tasks, 0, 2) };
    const input = templateInputFromDraft(moved);
    expect(input.tasks?.map((task) => task.title)).toEqual(["Design", "Build", "Discovery"]);
    // Discovery → Design is now 2 → 0; Design → Build is now 0 → 1.
    expect(input.dependencies).toEqual([
      { predecessorTaskIndex: 2, successorTaskIndex: 0 },
      { predecessorTaskIndex: 0, successorTaskIndex: 1 },
    ]);
  });

  it("unlinks the tasks of a removed milestone and shifts the links after it", () => {
    const draft = draftFromTemplate(stored);
    const input = templateInputFromDraft(removeMilestone(draft, draft.milestones[0]?.key ?? ""));
    expect(input.milestones?.map((m) => m.name)).toEqual(["Handover"]);
    expect(input.tasks?.map((task) => task.milestoneIndex)).toEqual([undefined, 0, 0]);
  });

  it("drops a removed task's dependencies and shifts the indices after it", () => {
    const draft = draftFromTemplate(stored);
    const input = templateInputFromDraft(removeTask(draft, draft.tasks[0]?.key ?? ""));
    expect(input.tasks?.map((task) => task.title)).toEqual(["Design", "Build"]);
    expect(input.dependencies).toEqual([{ predecessorTaskIndex: 0, successorTaskIndex: 1 }]);
  });

  it("ignores a move out of range", () => {
    expect(moveRow([1, 2, 3], 0, 3)).toEqual([1, 2, 3]);
    expect(moveRow([1, 2, 3], -1, 0)).toEqual([1, 2, 3]);
  });
});

describe("validateTemplateDraft", () => {
  const valid = (): TemplateDraft => draftFromTemplate(stored);

  it("accepts a valid template", () => {
    expect(validateTemplateDraft(valid())).toEqual({});
  });

  it("requires names and titles, counted in characters", () => {
    const draft = valid();
    draft.name = " ";
    draft.milestones[0] = { ...emptyMilestone(), name: "" };
    const firstTask = draft.tasks[0];
    if (!firstTask) throw new Error("fixture");
    firstTask.subtasks[0] = {
      ...firstTask.subtasks[0],
      key: "s",
      title: "",
      description: "",
      startDayOffset: "",
      dueDayOffset: "",
      estimateHours: "",
      checklist: [],
    };
    expect(validateTemplateDraft(draft)).toMatchObject({
      name: "templateNameRequired",
      "milestones.0.name": "deliveryMilestoneNameRequired",
      "tasks.0.subtasks.0.title": "taskTitleRequired",
    });
    draft.name = "ø".repeat(200);
    expect(validateTemplateDraft(draft).name).toBeUndefined();
    draft.name = "ø".repeat(201);
    expect(validateTemplateDraft(draft).name).toBe("templateNameTooLong");
  });

  it("refuses negative, fractional and missing offsets, and a due day before the start", () => {
    const draft = valid();
    draft.milestones[0] = { ...emptyMilestone(), name: "M", targetDayOffset: "" };
    draft.milestones[1] = { ...emptyMilestone(), name: "N", targetDayOffset: -1 };
    draft.tasks[1] = { ...emptyTask(), title: "T", startDayOffset: 5, dueDayOffset: 2 };
    draft.tasks[2] = { ...emptyTask(), title: "U", startDayOffset: 1.5 };
    expect(validateTemplateDraft(draft)).toMatchObject({
      "milestones.0.targetDayOffset": "templateDayOffsetRequired",
      "milestones.1.targetDayOffset": "templateDayOffsetNegative",
      "tasks.1.dueDayOffset": "templateDueBeforeStart",
      "tasks.2.startDayOffset": "templateDayOffsetWhole",
    });
  });

  it("refuses a zero or too large estimate", () => {
    const draft = valid();
    draft.tasks[1] = { ...emptyTask(), title: "T", estimateHours: 0 };
    draft.tasks[2] = { ...emptyTask(), title: "U", estimateHours: 1_000_000 };
    expect(validateTemplateDraft(draft)).toMatchObject({
      "tasks.1.estimateHours": "estimateMustBePositive",
      "tasks.2.estimateHours": "templateEstimateTooLarge",
    });
  });

  it("refuses an incomplete, self, duplicate or cyclic dependency", () => {
    const draft = valid();
    const [a, b, c] = draft.tasks.map((task) => task.key);
    draft.dependencies = [
      { ...emptyDependency() },
      { ...emptyDependency(), predecessorKey: a ?? null, successorKey: a ?? null },
      { ...emptyDependency(), predecessorKey: a ?? null, successorKey: b ?? null },
      { ...emptyDependency(), predecessorKey: a ?? null, successorKey: b ?? null },
    ];
    expect(validateTemplateDraft(draft)).toEqual({
      "dependencies.0.predecessorKey": "templateDependencyTaskRequired",
      "dependencies.0.successorKey": "templateDependencyTaskRequired",
      "dependencies.1.successorKey": "templateDependencySelf",
      "dependencies.3.successorKey": "duplicateDependencyError",
    });

    draft.dependencies = [
      { ...emptyDependency(), predecessorKey: a ?? null, successorKey: b ?? null },
      { ...emptyDependency(), predecessorKey: b ?? null, successorKey: c ?? null },
      { ...emptyDependency(), predecessorKey: c ?? null, successorKey: a ?? null },
    ];
    expect(validateTemplateDraft(draft)).toEqual({ dependencies: "templateDependencyCycle" });
  });

  it("refuses a blank or too long checklist item, and more than 100 of them", () => {
    const draft = valid();
    const [first, second] = draft.tasks;
    if (!first || !second) throw new Error("fixture");
    first.checklist = [
      { ...emptyChecklistItem(), text: "Fine" },
      { ...emptyChecklistItem(), text: "  " },
    ];
    const sub = first.subtasks[0];
    if (!sub) throw new Error("fixture");
    sub.checklist = [{ ...emptyChecklistItem(), text: "å".repeat(501) }];
    second.checklist = Array.from({ length: 101 }, (_, i) => ({ ...emptyChecklistItem(), text: `Item ${i}` }));
    expect(validateTemplateDraft(draft)).toEqual({
      "tasks.0.checklist.1.text": "templateChecklistItemRequired",
      "tasks.0.subtasks.0.checklist.0.text": "checklistTextTooLong",
      "tasks.1.checklist": "templateChecklistTooLong",
    });
    sub.checklist = [{ ...emptyChecklistItem(), text: "å".repeat(500) }];
    second.checklist = second.checklist.slice(0, 100);
    first.checklist = first.checklist.slice(0, 1);
    expect(validateTemplateDraft(draft)).toEqual({});
  });

  it("refuses a dependency chain of more than 100 links", () => {
    const tasks = Array.from({ length: 102 }, (_, i) => ({ ...emptyTask(), title: `T${i}` }));
    const chain = (links: number) =>
      tasks.slice(0, links).map((task, i) => ({
        ...emptyDependency(),
        predecessorKey: task.key,
        successorKey: tasks[i + 1]?.key ?? null,
      }));
    const draft: TemplateDraft = { name: "Chain", description: "", milestones: [], tasks, dependencies: chain(100) };
    expect(validateTemplateDraft(draft)).toEqual({});
    draft.dependencies = chain(101);
    expect(validateTemplateDraft(draft)).toEqual({ dependencies: "templateDependencyChainTooLong" });
  });
});

describe("longestDependencyChain", () => {
  it("counts the links of the longest chain", () => {
    expect(longestDependencyChain([])).toBe(0);
    expect(
      longestDependencyChain([
        ["a", "b"],
        ["a", "c"],
        ["b", "d"],
        ["c", "d"],
        ["d", "e"],
      ]),
    ).toBe(3);
  });
});

describe("dependenciesHaveCycle", () => {
  it("finds a cycle and leaves a diamond alone", () => {
    expect(
      dependenciesHaveCycle([
        ["a", "b"],
        ["b", "a"],
      ]),
    ).toBe(true);
    expect(
      dependenciesHaveCycle([
        ["a", "b"],
        ["a", "c"],
        ["b", "d"],
        ["c", "d"],
      ]),
    ).toBe(false);
    expect(dependenciesHaveCycle([])).toBe(false);
  });
});

describe("formPathFromServerField", () => {
  it("turns the server's field names into form paths", () => {
    expect(formPathFromServerField("name")).toBe("name");
    expect(formPathFromServerField("tasks[1].subtasks[0].title")).toBe("tasks.1.subtasks.0.title");
    expect(formPathFromServerField("dependencies[2].successorTaskIndex")).toBe("dependencies.2.successorKey");
    expect(formPathFromServerField("dependencies")).toBe("dependencies");
    expect(formPathFromServerField("tasks[0].subtasks[1].checklist[2]")).toBe("tasks.0.subtasks.1.checklist.2.text");
    expect(formPathFromServerField("tasks[0].checklist")).toBe("tasks.0.checklist");
  });
});

describe("templateSubtaskCount", () => {
  it("counts the subtasks under every task", () => {
    expect(templateSubtaskCount(stored)).toBe(1);
  });
});

describe("templateChecklistCount", () => {
  it("counts the checklist items under every task and subtask", () => {
    expect(templateChecklistCount(stored)).toBe(0);
    expect(
      templateChecklistCount({
        ...stored,
        tasks: [{ title: "A", checklist: ["1", "2"], subtasks: [{ title: "a", checklist: ["3"] }, { title: "b" }] }],
      }),
    ).toBe(3);
  });
});
