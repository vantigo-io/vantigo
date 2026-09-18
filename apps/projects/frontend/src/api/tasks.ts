import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { TaskStatus } from "../lib/tasks";
import type { PaginatedResponse } from "./projects";
import { request } from "./request";

export type { TaskStatus } from "../lib/tasks";

type Schemas = components["schemas"];

/**
 * The contract declares the task status as a plain string (OpenAPI 3.0
 * without enum members), so the generated rows are narrowed here to the exact
 * strings the backend accepts. Everything else is the generated shape.
 */
export type Task = Omit<Schemas["TaskResponse"], "status" | "subtasks"> & {
  status: TaskStatus;
  /** Present on the endpoints that answer a tree, and never on a subtask itself (one level of nesting). */
  subtasks?: Task[];
};

/** One open task of the caller's, named with the project it belongs to. Never nested. */
export type MyTask = Omit<Schemas["MyTaskResponse"], "status"> & { status: TaskStatus };

export type TaskAssignee = Schemas["TaskAssignee"];
export type TaskChecklistProgress = Schemas["TaskChecklistProgress"];

export type TaskInput = Omit<Schemas["TaskRequest"], "status"> & { status?: TaskStatus };

/** An update carries every field plus the revision the task was read at. */
export type TaskUpdateInput = Omit<Schemas["TaskUpdateRequest"], "status"> & { status: TaskStatus };

export type TaskPositionInput = Schemas["TaskPositionRequest"];

export type ChecklistItem = Schemas["ChecklistItemResponse"];
export type ChecklistItemInput = Schemas["ChecklistItemRequest"];
export type ChecklistItemUpdateInput = Schemas["ChecklistItemUpdateRequest"];

export type TaskComment = Schemas["CommentResponse"];
export type TaskCommentAuthor = Schemas["CommentAuthor"];
export type TaskCommentInput = Schemas["CommentRequest"];

/** How many comments one "load more" adds; the API's own default page is larger than a drawer wants. */
export const TASK_COMMENTS_PAGE_SIZE = 20;

/** What the tasks tab may narrow the tree by. An absent filter is no filter at all. */
export interface TaskFilters {
  status?: TaskStatus;
  assigneeUserId?: string;
}

const treeQuery = (filters: TaskFilters): string => {
  const query = new URLSearchParams();
  if (filters.status) query.set("status", filters.status);
  if (filters.assigneeUserId) query.set("assigneeUserId", filters.assigneeUserId);
  const search = query.toString();
  return search ? `?${search}` : "";
};

/**
 * The project's top-level tasks by position, each carrying its own subtasks.
 * Every task query key starts `["projects", "tasks"]`, so the blanket
 * `["projects"]` invalidation every write in this package does still reaches
 * them.
 */
export const projectTasksQueryOptions = (projectId: number, filters: TaskFilters = {}) =>
  queryOptions({
    queryKey: ["projects", "tasks", "project", projectId, filters],
    queryFn: ({ signal }) => request<Task[]>(`/api/v1/projects/${projectId}/tasks${treeQuery(filters)}`, { signal }),
  });

/** One task with its own subtasks — what the drawer reads. */
export const taskQueryOptions = (taskId: number) =>
  queryOptions({
    queryKey: ["projects", "tasks", "detail", taskId],
    queryFn: ({ signal }) => request<Task>(`/api/v1/projects/tasks/${taskId}`, { signal }),
  });

/** The caller's open tasks across every project they can see, by due date. */
export const myTasksQueryOptions = () =>
  queryOptions({
    queryKey: ["projects", "tasks", "mine"],
    queryFn: ({ signal }) => request<MyTask[]>("/api/v1/projects/my-tasks", { signal }),
  });

export const checklistQueryOptions = (taskId: number) =>
  queryOptions({
    queryKey: ["projects", "tasks", "detail", taskId, "checklist"],
    queryFn: ({ signal }) => request<ChecklistItem[]>(`/api/v1/projects/tasks/${taskId}/checklist`, { signal }),
  });

/** One page of a task's comments, oldest first — the way a conversation is read. */
export const commentsQueryOptions = (taskId: number, page: number) =>
  queryOptions({
    queryKey: ["projects", "tasks", "detail", taskId, "comments", page],
    queryFn: ({ signal }) => {
      const query = new URLSearchParams({ page: String(page), pageSize: String(TASK_COMMENTS_PAGE_SIZE) });
      return request<PaginatedResponse<TaskComment>>(`/api/v1/projects/tasks/${taskId}/comments?${query}`, { signal });
    },
    placeholderData: keepPreviousData,
  });

export const createTask = (projectId: number, input: TaskInput): Promise<Task> =>
  request<Task>(`/api/v1/projects/${projectId}/tasks`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/** A full replace: a revision that has moved on answers 409 and the caller is asked to reload. */
export const updateTask = (taskId: number, input: TaskUpdateInput): Promise<Task> =>
  request<Task>(`/api/v1/projects/tasks/${taskId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/** The task, its subtasks, its checklist and its comments all go. */
export const deleteTask = (taskId: number): Promise<void> =>
  request<void>(`/api/v1/projects/tasks/${taskId}`, { method: "DELETE" });

/** Where the task should sit: among which siblings, and how far down. */
export const moveTask = (taskId: number, input: TaskPositionInput): Promise<Task> =>
  request<Task>(`/api/v1/projects/tasks/${taskId}/position`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const addChecklistItem = (taskId: number, input: ChecklistItemInput): Promise<ChecklistItem> =>
  request<ChecklistItem>(`/api/v1/projects/tasks/${taskId}/checklist`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/** Every field is optional: ticking an item off says nothing about its text or its place. */
export const updateChecklistItem = (
  taskId: number,
  itemId: number,
  input: ChecklistItemUpdateInput,
): Promise<ChecklistItem> =>
  request<ChecklistItem>(`/api/v1/projects/tasks/${taskId}/checklist/${itemId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const deleteChecklistItem = (taskId: number, itemId: number): Promise<void> =>
  request<void>(`/api/v1/projects/tasks/${taskId}/checklist/${itemId}`, { method: "DELETE" });

export const addComment = (taskId: number, input: TaskCommentInput): Promise<TaskComment> =>
  request<TaskComment>(`/api/v1/projects/tasks/${taskId}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const updateComment = (taskId: number, commentId: number, input: TaskCommentInput): Promise<TaskComment> =>
  request<TaskComment>(`/api/v1/projects/tasks/${taskId}/comments/${commentId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const deleteComment = (taskId: number, commentId: number): Promise<void> =>
  request<void>(`/api/v1/projects/tasks/${taskId}/comments/${commentId}`, { method: "DELETE" });

/** The fields a full replace needs, which a tree task and a my-tasks row both carry. */
export type TaskUpdateSource = Pick<
  Task,
  "title" | "description" | "status" | "assignee" | "startDate" | "dueDate" | "estimateHours" | "revision"
>;

/**
 * The update body for a task as it stands, with the one thing the caller is
 * changing applied. An update is a full replace, so anything left out would be
 * cleared: a status change from a board card has to carry the title, the
 * assignee and the dates along with it.
 */
export const taskUpdateFrom = (task: TaskUpdateSource, changes: Partial<TaskUpdateInput> = {}): TaskUpdateInput => ({
  title: task.title,
  description: task.description ?? null,
  status: task.status,
  assigneeUserId: task.assignee?.userId ?? null,
  startDate: task.startDate ?? null,
  dueDate: task.dueDate ?? null,
  estimateHours: task.estimateHours ?? null,
  revision: task.revision,
  ...changes,
});
