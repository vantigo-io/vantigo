import type {
  ProjectTemplate,
  ProjectTemplateCreateInput,
  ProjectTemplateDependency,
  ProjectTemplateSubtask,
  ProjectTemplateTask,
} from "../api/templates";
import { DELIVERY_MILESTONE_DESCRIPTION_MAX, DELIVERY_MILESTONE_NAME_MAX } from "./delivery-milestones";
import { TASK_DESCRIPTION_MAX, TASK_TITLE_MAX } from "./tasks";
import { nameLength } from "./work-types";

/**
 * The template editor's own shape of a blueprint. The contract links a task to
 * its milestone, and a dependency to its two tasks, by 0-based index; an editor
 * that kept those indices would have to rewrite every one of them each time a
 * row moved or went away. So the draft links rows by a key that never changes,
 * and the indices are worked out once, on the way out (`templateInputFromDraft`),
 * from wherever the rows stand by then.
 *
 * Numbers are `number | string` because that is what a NumberInput holds: an
 * empty field is "".
 */
export interface TemplateDraftSubtask {
  key: string;
  title: string;
  description: string;
  startDayOffset: number | string;
  dueDayOffset: number | string;
  estimateHours: number | string;
}

export interface TemplateDraftTask extends TemplateDraftSubtask {
  /** The key of the milestone the task is linked to, or null for none. */
  milestoneKey: string | null;
  subtasks: TemplateDraftSubtask[];
}

export interface TemplateDraftMilestone {
  key: string;
  name: string;
  description: string;
  targetDayOffset: number | string;
}

export interface TemplateDraftDependency {
  key: string;
  /** Task keys; null until the row's picker has a task in it. */
  predecessorKey: string | null;
  successorKey: string | null;
}

export interface TemplateDraft {
  name: string;
  description: string;
  milestones: TemplateDraftMilestone[];
  tasks: TemplateDraftTask[];
  dependencies: TemplateDraftDependency[];
}

/** What the server accepts, mirrored so the editor says so before a round trip. */
export const TEMPLATE_NAME_MAX = 200;
export const TEMPLATE_DESCRIPTION_MAX = 2000;
/** The widest estimate the task column holds, numeric(8,2). */
export const ESTIMATE_HOURS_MAX = 999999.99;

let nextKey = 0;
/** A key unique within this page's lifetime, which is all a row key needs. */
export const newRowKey = (kind: string): string => {
  nextKey += 1;
  return `${kind}-${nextKey}`;
};

const offsetField = (value: number | null | undefined): number | string => value ?? "";
const textField = (value: string | null | undefined): string => value ?? "";

export const emptyMilestone = (): TemplateDraftMilestone => ({
  key: newRowKey("milestone"),
  name: "",
  description: "",
  targetDayOffset: 0,
});

export const emptySubtask = (): TemplateDraftSubtask => ({
  key: newRowKey("subtask"),
  title: "",
  description: "",
  startDayOffset: "",
  dueDayOffset: "",
  estimateHours: "",
});

export const emptyTask = (): TemplateDraftTask => ({
  ...emptySubtask(),
  key: newRowKey("task"),
  milestoneKey: null,
  subtasks: [],
});

export const emptyDependency = (): TemplateDraftDependency => ({
  key: newRowKey("dependency"),
  predecessorKey: null,
  successorKey: null,
});

const subtaskDraft = (subtask: ProjectTemplateSubtask): TemplateDraftSubtask => ({
  key: newRowKey("subtask"),
  title: subtask.title,
  description: textField(subtask.description),
  startDayOffset: offsetField(subtask.startDayOffset),
  dueDayOffset: offsetField(subtask.dueDayOffset),
  estimateHours: offsetField(subtask.estimateHours),
});

/**
 * A stored template as the editor edits it, or a blank one. Indices that point
 * nowhere (the server refuses them, so only a hand-edited row could hold one)
 * become no link rather than a link to the wrong row.
 */
export const draftFromTemplate = (template?: ProjectTemplate): TemplateDraft => {
  if (!template) return { name: "", description: "", milestones: [], tasks: [], dependencies: [] };
  const milestones = template.milestones.map((m) => ({
    key: newRowKey("milestone"),
    name: m.name,
    description: textField(m.description),
    targetDayOffset: m.targetDayOffset,
  }));
  const tasks = template.tasks.map((task) => ({
    ...subtaskDraft(task),
    key: newRowKey("task"),
    milestoneKey: task.milestoneIndex == null ? null : (milestones[task.milestoneIndex]?.key ?? null),
    subtasks: (task.subtasks ?? []).map(subtaskDraft),
  }));
  const dependencies = template.dependencies.map((d) => ({
    key: newRowKey("dependency"),
    predecessorKey: tasks[d.predecessorTaskIndex]?.key ?? null,
    successorKey: tasks[d.successorTaskIndex]?.key ?? null,
  }));
  return { name: template.name, description: textField(template.description), milestones, tasks, dependencies };
};

const optionalText = (value: string): string | undefined => value.trim() || undefined;
const optionalNumber = (value: number | string): number | undefined => {
  if (typeof value === "number") return value;
  const trimmed = value.trim();
  return trimmed === "" ? undefined : Number(trimmed);
};

const subtaskInput = (subtask: TemplateDraftSubtask): ProjectTemplateSubtask => ({
  title: subtask.title.trim(),
  description: optionalText(subtask.description),
  startDayOffset: optionalNumber(subtask.startDayOffset),
  dueDayOffset: optionalNumber(subtask.dueDayOffset),
  estimateHours: optionalNumber(subtask.estimateHours),
});

/**
 * The draft as the contract carries it: every key link turned back into the
 * index of the row it names, as the rows stand now. A dependency whose picker
 * is still empty is not sent (validation refuses to submit one anyway).
 */
export const templateInputFromDraft = (draft: TemplateDraft): ProjectTemplateCreateInput => {
  const milestoneIndex = new Map(draft.milestones.map((m, i) => [m.key, i]));
  const taskIndex = new Map(draft.tasks.map((task, i) => [task.key, i]));
  const tasks: ProjectTemplateTask[] = draft.tasks.map((task) => ({
    ...subtaskInput(task),
    milestoneIndex: task.milestoneKey === null ? undefined : milestoneIndex.get(task.milestoneKey),
    subtasks: task.subtasks.map(subtaskInput),
  }));
  const dependencies: ProjectTemplateDependency[] = draft.dependencies.flatMap((d) => {
    const predecessorTaskIndex = d.predecessorKey === null ? undefined : taskIndex.get(d.predecessorKey);
    const successorTaskIndex = d.successorKey === null ? undefined : taskIndex.get(d.successorKey);
    return predecessorTaskIndex === undefined || successorTaskIndex === undefined
      ? []
      : [{ predecessorTaskIndex, successorTaskIndex }];
  });
  return {
    name: draft.name.trim(),
    description: optionalText(draft.description),
    milestones: draft.milestones.map((m) => ({
      name: m.name.trim(),
      description: optionalText(m.description),
      targetDayOffset: optionalNumber(m.targetDayOffset) ?? 0,
    })),
    tasks,
    dependencies,
  };
};

/** `list` with the row at `from` moved to `to`; out-of-range moves change nothing. */
export function moveRow<T>(list: readonly T[], from: number, to: number): T[] {
  if (from < 0 || from >= list.length || to < 0 || to >= list.length || from === to) return [...list];
  const next = [...list];
  const [row] = next.splice(from, 1);
  next.splice(to, 0, row as T);
  return next;
}

/** The draft without one milestone; the tasks linked to it keep their place and lose the link. */
export const removeMilestone = (draft: TemplateDraft, key: string): TemplateDraft => ({
  ...draft,
  milestones: draft.milestones.filter((m) => m.key !== key),
  tasks: draft.tasks.map((task) => (task.milestoneKey === key ? { ...task, milestoneKey: null } : task)),
});

/** The draft without one task, its subtasks, and every dependency it was one end of. */
export const removeTask = (draft: TemplateDraft, key: string): TemplateDraft => ({
  ...draft,
  tasks: draft.tasks.filter((task) => task.key !== key),
  dependencies: draft.dependencies.filter((d) => d.predecessorKey !== key && d.successorKey !== key),
});

/** How many subtasks a stored template holds, under all its tasks. */
export const templateSubtaskCount = (template: ProjectTemplate): number =>
  template.tasks.reduce((sum, task) => sum + (task.subtasks?.length ?? 0), 0);

/**
 * Whether the dependencies, as pairs of task keys, form a cycle. Kahn's
 * algorithm, as the server runs it: the graph is acyclic exactly when
 * repeatedly taking away the tasks nothing waits on takes away every task.
 */
export const dependenciesHaveCycle = (pairs: readonly (readonly [string, string])[]): boolean => {
  const incoming = new Map<string, number>();
  const successors = new Map<string, string[]>();
  for (const [from, to] of pairs) {
    if (!incoming.has(from)) incoming.set(from, 0);
    incoming.set(to, (incoming.get(to) ?? 0) + 1);
    successors.set(from, [...(successors.get(from) ?? []), to]);
  }
  const ready = [...incoming].filter(([, n]) => n === 0).map(([node]) => node);
  let removed = 0;
  while (ready.length > 0) {
    const node = ready.pop() as string;
    removed += 1;
    for (const next of successors.get(node) ?? []) {
      const left = (incoming.get(next) ?? 0) - 1;
      incoming.set(next, left);
      if (left === 0) ready.push(next);
    }
  }
  return removed < incoming.size;
};

/** The `projects` catalog key of each rule a draft breaks, by the form path of the field it is about. */
export type TemplateDraftErrors = Record<string, string>;

const isWholeDays = (value: number) => Number.isInteger(value);

/**
 * The server's template rules (validateTemplateData in
 * apps/server/internal/projects/templates.go), mirrored so the editor refuses
 * what the API would and points at the row it is about. Errors are keyed by
 * Mantine form path (`tasks.1.subtasks.0.title`), valued by catalog key.
 */
export const validateTemplateDraft = (draft: TemplateDraft): TemplateDraftErrors => {
  const errors: TemplateDraftErrors = {};
  const set = (path: string, key: string | null) => {
    if (key && !errors[path]) errors[path] = key;
  };

  const name = draft.name.trim();
  set("name", !name ? "templateNameRequired" : nameLength(name) > TEMPLATE_NAME_MAX ? "templateNameTooLong" : null);
  set(
    "description",
    nameLength(draft.description.trim()) > TEMPLATE_DESCRIPTION_MAX ? "deliveryMilestoneDescriptionTooLong" : null,
  );

  const offset = (path: string, value: number | string, required: boolean): number | undefined => {
    const parsed = optionalNumber(value);
    if (parsed === undefined || Number.isNaN(parsed)) {
      set(path, required || Number.isNaN(parsed) ? "templateDayOffsetRequired" : null);
      return undefined;
    }
    set(path, parsed < 0 ? "templateDayOffsetNegative" : !isWholeDays(parsed) ? "templateDayOffsetWhole" : null);
    return parsed;
  };

  draft.milestones.forEach((m, i) => {
    const milestoneName = m.name.trim();
    set(
      `milestones.${i}.name`,
      !milestoneName
        ? "deliveryMilestoneNameRequired"
        : nameLength(milestoneName) > DELIVERY_MILESTONE_NAME_MAX
          ? "deliveryMilestoneNameTooLong"
          : null,
    );
    set(
      `milestones.${i}.description`,
      nameLength(m.description.trim()) > DELIVERY_MILESTONE_DESCRIPTION_MAX
        ? "deliveryMilestoneDescriptionTooLong"
        : null,
    );
    offset(`milestones.${i}.targetDayOffset`, m.targetDayOffset, true);
  });

  const taskRules = (path: string, task: TemplateDraftSubtask) => {
    const title = task.title.trim();
    set(`${path}.title`, !title ? "taskTitleRequired" : nameLength(title) > TASK_TITLE_MAX ? "taskTitleTooLong" : null);
    set(
      `${path}.description`,
      nameLength(task.description.trim()) > TASK_DESCRIPTION_MAX ? "descriptionTooLong" : null,
    );
    const start = offset(`${path}.startDayOffset`, task.startDayOffset, false);
    const due = offset(`${path}.dueDayOffset`, task.dueDayOffset, false);
    if (start !== undefined && due !== undefined && due < start) set(`${path}.dueDayOffset`, "templateDueBeforeStart");
    const estimate = optionalNumber(task.estimateHours);
    if (estimate !== undefined) {
      set(
        `${path}.estimateHours`,
        Number.isNaN(estimate) || estimate <= 0
          ? "estimateMustBePositive"
          : Math.round(estimate * 100) / 100 > ESTIMATE_HOURS_MAX
            ? "templateEstimateTooLarge"
            : null,
      );
    }
  };

  draft.tasks.forEach((task, i) => {
    taskRules(`tasks.${i}`, task);
    task.subtasks.forEach((subtask, j) => {
      taskRules(`tasks.${i}.subtasks.${j}`, subtask);
    });
  });

  const seen = new Set<string>();
  const pairs: [string, string][] = [];
  draft.dependencies.forEach((d, i) => {
    if (d.predecessorKey === null) set(`dependencies.${i}.predecessorKey`, "templateDependencyTaskRequired");
    if (d.successorKey === null) set(`dependencies.${i}.successorKey`, "templateDependencyTaskRequired");
    if (d.predecessorKey === null || d.successorKey === null) return;
    if (d.predecessorKey === d.successorKey) {
      set(`dependencies.${i}.successorKey`, "templateDependencySelf");
      return;
    }
    const pair = `${d.predecessorKey}→${d.successorKey}`;
    if (seen.has(pair)) {
      set(`dependencies.${i}.successorKey`, "duplicateDependencyError");
      return;
    }
    seen.add(pair);
    pairs.push([d.predecessorKey, d.successorKey]);
  });
  if (dependenciesHaveCycle(pairs)) set("dependencies", "templateDependencyCycle");

  return errors;
};

/**
 * A server field name (`tasks[1].subtasks[0].title`) as the editor's form path
 * (`tasks.1.subtasks.0.title`). The editor sends its rows in the order it shows
 * them, so the server's indices are the form's; the one exception is a
 * dependency's two ends, which the server names by index and the form by key.
 */
export const formPathFromServerField = (field: string): string =>
  field
    .replace(/\[(\d+)\]/g, ".$1")
    .replace(/\.predecessorTaskIndex$/, ".predecessorKey")
    .replace(/\.successorTaskIndex$/, ".successorKey");
