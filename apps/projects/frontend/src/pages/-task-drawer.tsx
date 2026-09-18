import {
  ActionIcon,
  Alert,
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
import { createTask, deleteTask, type Task, taskQueryOptions } from "../api/tasks";
import { AssigneePicker } from "../components/assignee-picker";
import { Field } from "../components/field";
import { TaskStatusBadge } from "../components/task-status-badge";
import "../i18n";
import { useProjectDates } from "../lib/dates";
import {
  isTaskStatus,
  TASK_DESCRIPTION_MAX,
  TASK_TITLE_MAX,
  type TaskStatus,
  taskStatuses,
  taskStatusLabelKey,
} from "../lib/tasks";
import { useTaskSave } from "../lib/use-task-save";
import { TaskChecklist } from "./-task-checklist";
import { TaskComments } from "./-task-comments";

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

  // Nesting is one level deep, so a subtask can neither be given subtasks nor
  // ever have any: the section is left out of its drawer entirely rather than
  // standing empty above an add the API would refuse.
  const showSubtasks = task.parentTaskId == null || (task.subtasks?.length ?? 0) > 0;

  return (
    <Stack gap="lg">
      <TaskHeader task={task} canContribute={canContribute} onDeleted={onClose} />
      <TaskDetails projectId={projectId} task={task} canContribute={canContribute} />
      {showSubtasks && (
        <>
          <Divider />
          <Subtasks projectId={projectId} task={task} canContribute={canContribute} />
        </>
      )}
      <Divider />
      <TaskChecklist taskId={task.id} canContribute={canContribute} />
      <Divider />
      <TaskComments
        taskId={task.id}
        canContribute={canContribute}
        canManage={canManage}
        currentUserId={currentUserId}
      />
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
  // The title being typed, and the revision the task stood at when the caller
  // started typing it — the same reason the details form holds one.
  const [draft, setDraft] = useState<{ title: string; revision: number } | null>(null);
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
    if (draft === null) return;
    const title = draft.title.trim();
    if (!title || title.length > TASK_TITLE_MAX) return;
    save.mutate({ task, changes: { title, revision: draft.revision } }, { onSuccess: () => setDraft(null) });
  };

  if (draft !== null) {
    const tooLong = draft.title.trim().length > TASK_TITLE_MAX;
    return (
      <Group align="start" wrap="nowrap">
        <TextInput
          aria-label={t("taskTitle")}
          flex={1}
          data-autofocus
          value={draft.title}
          error={tooLong ? t("taskTitleTooLong") : undefined}
          onChange={(event) => setDraft({ ...draft, title: event.currentTarget.value })}
        />
        <ActionIcon
          variant="filled"
          aria-label={t("saveTaskTitle")}
          loading={save.isPending}
          disabled={!draft.title.trim() || tooLong}
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
          <ActionIcon
            variant="subtle"
            aria-label={t("editTaskTitle")}
            onClick={() => setDraft({ title: task.title, revision: task.revision })}
          >
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
      description: (value) => (value.trim().length > TASK_DESCRIPTION_MAX ? t("descriptionTooLong") : null),
    },
  });
  // The revision the fields on screen were read at. The form seeds once, but
  // the task behind it is refetched by every invalidation the drawer causes —
  // a ticked checklist item, a posted comment — and sending that newer
  // revision with these older fields would quietly overwrite whatever somebody
  // else changed meanwhile instead of being refused with a 409.
  const [seededRevision, setSeededRevision] = useState(task.revision);
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
        save.mutate(
          {
            task,
            changes: {
              status: values.status,
              assigneeUserId: values.assigneeUserId,
              startDate: values.startDate,
              dueDate: values.dueDate,
              estimateHours: hours(values.estimateHours) ?? null,
              description: values.description.trim() || null,
              revision: seededRevision,
            },
          },
          // What was just saved is what the form now stands at, so the next
          // save is guarded by the revision this one produced.
          {
            onSuccess: (saved) => {
              setSeededRevision(saved.revision);
              form.setInitialValues(values);
            },
          },
        ),
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

/**
 * One level down: the task's own subtasks, ticked off or added without leaving
 * the drawer. A task that is itself a subtask reads its own — there are never
 * any — but is offered no add: the API refuses a third level, and the drawer
 * opens on a subtask from a list row, a board card, a "my tasks" row and the
 * `?task=` deep link alike.
 */
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
  const canAdd = canContribute && task.parentTaskId == null;

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
      {canAdd && (
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
