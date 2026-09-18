import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Checkbox,
  Divider,
  Drawer,
  Group,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCheck, IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  addChecklistItem,
  addComment,
  type ChecklistItem,
  checklistQueryOptions,
  commentsQueryOptions,
  createTask,
  deleteChecklistItem,
  deleteComment,
  deleteTask,
  type Task,
  type TaskComment,
  taskQueryOptions,
  updateChecklistItem,
  updateComment,
} from "../api/tasks";
import { AssigneePicker } from "../components/assignee-picker";
import { Field } from "../components/field";
import { TaskStatusBadge } from "../components/task-status-badge";
import "../i18n";
import { useProjectDates } from "../lib/dates";
import { isTaskStatus, type TaskStatus, taskStatuses, taskStatusLabelKey } from "../lib/tasks";
import { useTaskSave } from "../lib/use-task-save";

export interface TaskDrawerProps {
  projectId: number;
  /** The task to show; null while the drawer has never been opened on one. */
  taskId: number | null;
  opened: boolean;
  onClose: () => void;
  /** Whether the caller may write the project's work — the project's `capabilities.canContribute`. */
  canContribute: boolean;
  /** A manager may delete anybody's comment, not only their own. */
  canManage: boolean;
  /** Who is looking, so an author may edit and delete their own comments. The host owns the session. */
  currentUserId?: string;
}

/**
 * One task in full (design §8): its fields, its subtasks, its checklist and
 * its comments. Every write is a full replace carrying the revision the task
 * was read at, so a task somebody else changed meanwhile is refused rather
 * than silently overwritten.
 */
export const TaskDrawer = ({ taskId, opened, onClose, ...rest }: TaskDrawerProps) => {
  const { t } = useI18n("projects");
  return (
    <Drawer opened={opened} onClose={onClose} position="right" size="lg" title={t("task")}>
      {taskId !== null && <TaskDrawerBody key={taskId} taskId={taskId} onClose={onClose} {...rest} />}
    </Drawer>
  );
};

type BodyProps = Omit<TaskDrawerProps, "opened" | "taskId"> & { taskId: number };

const TaskDrawerBody = ({ projectId, taskId, onClose, canContribute, canManage, currentUserId }: BodyProps) => {
  const { t } = useI18n("projects");
  const { data: task, isPending, isError, error } = useQuery(taskQueryOptions(taskId));

  if (isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadTask")}>
        {error.message}
      </Alert>
    );
  }
  if (isPending) return <ContentSkeleton rows={5} rowHeight={40} />;

  return (
    <Stack gap="lg">
      <TaskHeader task={task} canContribute={canContribute} onDeleted={onClose} />
      <TaskDetails projectId={projectId} task={task} canContribute={canContribute} />
      <Divider />
      <Subtasks projectId={projectId} task={task} canContribute={canContribute} />
      <Divider />
      <Checklist taskId={task.id} canContribute={canContribute} />
      <Divider />
      <Comments taskId={task.id} canContribute={canContribute} canManage={canManage} currentUserId={currentUserId} />
    </Stack>
  );
};

/** The title, editable in place, and the delete that takes the whole task with it. */
const TaskHeader = ({
  task,
  canContribute,
  onDeleted,
}: {
  task: Task;
  canContribute: boolean;
  onDeleted: () => void;
}) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<string | null>(null);
  const save = useTaskSave();

  const remove = useMutation({
    mutationFn: () => deleteTask(task.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      onDeleted();
      notifications.show({ color: "teal", title: t("taskDeleted"), message: task.title });
    },
    onError: (error) => {
      notifications.show({ color: "red", title: t("couldNotDeleteTask"), message: error.message });
    },
  });

  const confirmRemove = () =>
    modals.openConfirmModal({
      title: t("deleteTaskTitle", { title: task.title }),
      children: <Text size="sm">{t("deleteTaskWarning")}</Text>,
      labels: { confirm: t("deleteTask"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(),
    });

  const submitTitle = () => {
    const title = (draft ?? "").trim();
    if (!title) return;
    save.mutate({ task, changes: { title } }, { onSuccess: () => setDraft(null) });
  };

  if (draft !== null) {
    return (
      <Group align="end" wrap="nowrap">
        <TextInput
          aria-label={t("taskTitle")}
          flex={1}
          data-autofocus
          value={draft}
          onChange={(event) => setDraft(event.currentTarget.value)}
        />
        <ActionIcon
          variant="filled"
          aria-label={t("saveTaskTitle")}
          loading={save.isPending}
          onClick={submitTitle}
          size="lg"
        >
          <IconCheck size={16} />
        </ActionIcon>
        <Button variant="default" onClick={() => setDraft(null)}>
          {t("cancel")}
        </Button>
      </Group>
    );
  }

  return (
    <Group justify="space-between" align="start" wrap="nowrap">
      <Title order={3} size="h4">
        {task.title}
      </Title>
      {canContribute && (
        <Group gap="xs" wrap="nowrap">
          <ActionIcon variant="subtle" aria-label={t("editTaskTitle")} onClick={() => setDraft(task.title)}>
            <IconPencil size={16} />
          </ActionIcon>
          <ActionIcon variant="subtle" color="red" aria-label={t("deleteTask")} onClick={confirmRemove}>
            <IconTrash size={16} />
          </ActionIcon>
        </Group>
      )}
    </Group>
  );
};

interface DetailsFormValues {
  status: TaskStatus;
  assigneeUserId: string | null;
  startDate: string | null;
  dueDate: string | null;
  estimateHours: number | string;
  description: string;
}

const hours = (value: number | string): number | undefined => {
  if (typeof value === "number") return value;
  const trimmed = value.trim();
  return trimmed === "" ? undefined : Number(trimmed);
};

/** Everything but the title, in one save — a full replace is one call either way. */
const TaskDetails = ({ projectId, task, canContribute }: { projectId: number; task: Task; canContribute: boolean }) => {
  const { t, formatters } = useI18n("projects");
  const dates = useProjectDates();
  const form = useForm<DetailsFormValues>({
    initialValues: {
      status: task.status,
      assigneeUserId: task.assignee?.userId ?? null,
      startDate: task.startDate ?? null,
      dueDate: task.dueDate ?? null,
      estimateHours: task.estimateHours ?? "",
      description: task.description ?? "",
    },
    validate: {
      estimateHours: (value) => {
        const estimate = hours(value);
        return estimate !== undefined && estimate <= 0 ? t("estimateMustBePositive") : null;
      },
      dueDate: (value, values) => (value && values.startDate && value < values.startDate ? t("dueBeforeStart") : null),
    },
  });
  const save = useTaskSave(form.setErrors);

  if (!canContribute) {
    return (
      <Stack gap="md">
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <Field label={t("status")}>
            <TaskStatusBadge status={task.status} />
          </Field>
          <Field label={t("assignee")}>{task.assignee?.displayName ?? t("unassigned")}</Field>
          <Field label={t("dates")}>{dates.range(task.startDate, task.dueDate)}</Field>
          <Field label={t("estimate")}>
            {task.estimateHours === null || task.estimateHours === undefined
              ? t("notAvailable")
              : t("hours", { hours: formatters.formatNumber(task.estimateHours) })}
          </Field>
        </SimpleGrid>
        <Field label={t("description")}>{task.description || t("noDescription")}</Field>
      </Stack>
    );
  }

  return (
    <form
      onSubmit={form.onSubmit((values) =>
        save.mutate({
          task,
          changes: {
            status: values.status,
            assigneeUserId: values.assigneeUserId,
            startDate: values.startDate,
            dueDate: values.dueDate,
            estimateHours: hours(values.estimateHours) ?? null,
            description: values.description.trim() || null,
          },
        }),
      )}
    >
      <Stack gap="md">
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
          <Select
            label={t("status")}
            allowDeselect={false}
            data={taskStatuses.map((status) => ({ value: status, label: t(taskStatusLabelKey(status)) }))}
            value={form.values.status}
            onChange={(value) => value && isTaskStatus(value) && form.setFieldValue("status", value)}
          />
          <AssigneePicker
            projectId={projectId}
            value={form.values.assigneeUserId}
            selected={task.assignee}
            onChange={(value) => form.setFieldValue("assigneeUserId", value)}
          />
          <DateInput
            label={t("startDate")}
            valueFormat={t("dateInputFormat")}
            clearable
            {...form.getInputProps("startDate")}
          />
          <DateInput
            label={t("dueDate")}
            valueFormat={t("dateInputFormat")}
            clearable
            {...form.getInputProps("dueDate")}
          />
        </SimpleGrid>
        <NumberInput label={t("estimateHours")} min={0} decimalScale={2} {...form.getInputProps("estimateHours")} />
        <Textarea label={t("description")} rows={3} {...form.getInputProps("description")} />
        <Group justify="flex-end">
          <Button type="submit" loading={save.isPending}>
            {t("saveChanges")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/** One level down: the task's own subtasks, ticked off or added without leaving the drawer. */
const Subtasks = ({ projectId, task, canContribute }: { projectId: number; task: Task; canContribute: boolean }) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const [title, setTitle] = useState("");
  const save = useTaskSave();

  const add = useMutation({
    mutationFn: () => createTask(projectId, { title: title.trim(), parentTaskId: task.id }),
    onSuccess: (created) => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      setTitle("");
      notifications.show({ color: "teal", title: t("taskCreated"), message: created.title });
    },
    onError: (error) => {
      notifications.show({ color: "red", title: t("couldNotSaveTask"), message: error.message });
    },
  });

  const subtasks = task.subtasks ?? [];

  return (
    <Stack gap="xs">
      <Text fw={600} component="h4">
        {t("subtasks")}
      </Text>
      {subtasks.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noSubtasks")}
        </Text>
      )}
      {subtasks.map((subtask) => (
        <Group key={subtask.id} gap="sm" wrap="nowrap">
          <Checkbox
            label={subtask.title}
            disabled={!canContribute || save.isPending}
            checked={subtask.status === "done"}
            onChange={(event) =>
              save.mutate({ task: subtask, changes: { status: event.currentTarget.checked ? "done" : "todo" } })
            }
          />
          <TaskStatusBadge status={subtask.status} size="xs" />
        </Group>
      ))}
      {canContribute && (
        <Group gap="xs" wrap="nowrap" mt="xs">
          <TextInput
            aria-label={t("subtaskTitle")}
            placeholder={t("subtaskTitle")}
            flex={1}
            value={title}
            onChange={(event) => setTitle(event.currentTarget.value)}
          />
          <Button
            variant="light"
            leftSection={<IconPlus size={14} />}
            loading={add.isPending}
            disabled={!title.trim()}
            onClick={() => add.mutate()}
          >
            {t("addSubtask")}
          </Button>
        </Group>
      )}
    </Stack>
  );
};

const Checklist = ({ taskId, canContribute }: { taskId: number; canContribute: boolean }) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const { data: items, isPending, isError, error } = useQuery(checklistQueryOptions(taskId));
  const [text, setText] = useState("");

  const write = useMutation({
    mutationFn: (action: () => Promise<unknown>) => action(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      setText("");
    },
    onError: (failure) => {
      notifications.show({ color: "red", title: t("couldNotSaveChecklistItem"), message: failure.message });
    },
  });

  return (
    <Stack gap="xs">
      <Text fw={600} component="h4">
        {t("checklist")}
      </Text>
      {isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadChecklist")}>
          {error.message}
        </Alert>
      )}
      {isPending && <ContentSkeleton rows={2} rowHeight={28} />}
      {items && items.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noChecklistItems")}
        </Text>
      )}
      {(items ?? []).map((item: ChecklistItem) => (
        <Group key={item.id} gap="sm" wrap="nowrap" justify="space-between">
          <Checkbox
            label={item.text}
            disabled={!canContribute}
            checked={item.done}
            onChange={(event) => {
              // Read off the event now: the mutation runs the closure after
              // React has let go of it, when `currentTarget` is already null.
              const done = event.currentTarget.checked;
              write.mutate(() => updateChecklistItem(taskId, item.id, { done }));
            }}
          />
          {canContribute && (
            <ActionIcon
              variant="subtle"
              color="red"
              aria-label={t("deleteChecklistItem")}
              onClick={() => write.mutate(() => deleteChecklistItem(taskId, item.id))}
            >
              <IconTrash size={16} />
            </ActionIcon>
          )}
        </Group>
      ))}
      {canContribute && (
        <Group gap="xs" wrap="nowrap" mt="xs">
          <TextInput
            aria-label={t("checklistItem")}
            placeholder={t("checklistItem")}
            flex={1}
            value={text}
            onChange={(event) => setText(event.currentTarget.value)}
          />
          <Button
            variant="light"
            leftSection={<IconPlus size={14} />}
            loading={write.isPending}
            disabled={!text.trim()}
            onClick={() => write.mutate(() => addChecklistItem(taskId, { text: text.trim() }))}
          >
            {t("addChecklistItem")}
          </Button>
        </Group>
      )}
    </Stack>
  );
};

interface CommentsProps {
  taskId: number;
  canContribute: boolean;
  canManage: boolean;
  currentUserId?: string;
}

/**
 * The task's own history. Comments come oldest first, so "load more" reaches
 * forward in time and the pages simply stack: page 1 stays where it is.
 */
const Comments = ({ taskId, canContribute, canManage, currentUserId }: CommentsProps) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const [pageCount, setPageCount] = useState(1);
  const [body, setBody] = useState("");
  // The last page is what says whether there is another; every earlier page is
  // rendered by its own child, which reads the very same query key.
  const { data: lastPage, isPending, isError, error } = useQuery(commentsQueryOptions(taskId, pageCount));

  const post = useMutation({
    mutationFn: () => addComment(taskId, { body: body.trim() }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      setBody("");
    },
    onError: (failure) => {
      notifications.show({ color: "red", title: t("couldNotSaveComment"), message: failure.message });
    },
  });

  return (
    <Stack gap="xs">
      <Text fw={600} component="h4">
        {t("comments")}
      </Text>
      {isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadComments")}>
          {error.message}
        </Alert>
      )}
      {isPending && <ContentSkeleton rows={2} rowHeight={40} />}
      {lastPage && lastPage.pagination.totalCount === 0 && (
        <Text size="sm" c="dimmed">
          {t("noComments")}
        </Text>
      )}
      {Array.from({ length: pageCount }, (_, index) => index + 1).map((page) => (
        <CommentPage
          key={page}
          taskId={taskId}
          page={page}
          canContribute={canContribute}
          canManage={canManage}
          currentUserId={currentUserId}
        />
      ))}
      {lastPage?.pagination.hasNextPage && (
        <Group justify="center">
          <Button variant="subtle" size="xs" onClick={() => setPageCount((count) => count + 1)}>
            {t("loadMoreComments")}
          </Button>
        </Group>
      )}
      {canContribute && (
        <Stack gap="xs" mt="xs">
          <Textarea
            aria-label={t("writeComment")}
            placeholder={t("writeComment")}
            rows={3}
            value={body}
            onChange={(event) => setBody(event.currentTarget.value)}
          />
          <Group justify="flex-end">
            <Button loading={post.isPending} disabled={!body.trim()} onClick={() => post.mutate()}>
              {t("postComment")}
            </Button>
          </Group>
        </Stack>
      )}
    </Stack>
  );
};

const CommentPage = ({ taskId, page, canContribute, canManage, currentUserId }: CommentsProps & { page: number }) => {
  const { data } = useQuery(commentsQueryOptions(taskId, page));
  return (
    <>
      {(data?.data ?? []).map((comment) => (
        <CommentRow
          key={comment.id}
          taskId={taskId}
          comment={comment}
          canContribute={canContribute}
          canManage={canManage}
          currentUserId={currentUserId}
        />
      ))}
    </>
  );
};

const CommentRow = ({
  taskId,
  comment,
  canContribute,
  canManage,
  currentUserId,
}: CommentsProps & { comment: TaskComment }) => {
  const { t, formatters } = useI18n("projects");
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<string | null>(null);
  const isAuthor = currentUserId !== undefined && comment.author.userId === currentUserId;
  const canEdit = canContribute && isAuthor;
  const canDelete = canManage || canEdit;

  const write = useMutation({
    mutationFn: (action: () => Promise<unknown>) => action(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      setDraft(null);
    },
    onError: (failure) => {
      notifications.show({ color: "red", title: t("couldNotSaveComment"), message: failure.message });
    },
  });

  const confirmRemove = () =>
    modals.openConfirmModal({
      title: t("deleteCommentTitle"),
      children: <Text size="sm">{t("deleteCommentWarning")}</Text>,
      labels: { confirm: t("deleteTheComment"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => write.mutate(() => deleteComment(taskId, comment.id)),
    });

  return (
    <Stack gap={4}>
      <Group gap="xs" wrap="nowrap" justify="space-between">
        <Group gap="xs" wrap="nowrap">
          <Text size="sm" fw={600}>
            {comment.author.displayName}
          </Text>
          {!comment.author.active && (
            <Badge size="xs" variant="light" color="gray">
              {t("inactiveUser")}
            </Badge>
          )}
          <Text size="xs" c="dimmed">
            {formatters.formatDate(comment.createdAt, { dateStyle: "medium", timeStyle: "short" })}
          </Text>
          {comment.editedAt && (
            <Text size="xs" c="dimmed">
              {t("commentEdited")}
            </Text>
          )}
        </Group>
        <Group gap={4} wrap="nowrap">
          {canEdit && draft === null && (
            <ActionIcon variant="subtle" aria-label={t("editTheComment")} onClick={() => setDraft(comment.body)}>
              <IconPencil size={14} />
            </ActionIcon>
          )}
          {canDelete && (
            <ActionIcon variant="subtle" color="red" aria-label={t("deleteTheComment")} onClick={confirmRemove}>
              <IconTrash size={14} />
            </ActionIcon>
          )}
        </Group>
      </Group>
      {draft === null ? (
        <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
          {comment.body}
        </Text>
      ) : (
        <Stack gap="xs">
          <Textarea
            aria-label={t("editTheComment")}
            rows={3}
            value={draft}
            onChange={(event) => setDraft(event.currentTarget.value)}
          />
          <Group justify="flex-end" gap="xs">
            <Button variant="default" size="xs" onClick={() => setDraft(null)}>
              {t("cancel")}
            </Button>
            <Button
              size="xs"
              loading={write.isPending}
              disabled={!draft.trim()}
              onClick={() => write.mutate(() => updateComment(taskId, comment.id, { body: draft.trim() }))}
            >
              {t("saveChanges")}
            </Button>
          </Group>
        </Stack>
      )}
    </Stack>
  );
};
