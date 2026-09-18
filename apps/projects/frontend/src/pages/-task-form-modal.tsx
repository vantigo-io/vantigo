import { Button, Group, Modal, NumberInput, Select, Stack, Textarea, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { ApiValidationError } from "../api/projects";
import { createTask, projectTasksQueryOptions, type TaskInput } from "../api/tasks";
import { AssigneePicker } from "../components/assignee-picker";
import "../i18n";
import {
  isTaskStatus,
  TASK_DESCRIPTION_MAX,
  TASK_TITLE_MAX,
  type TaskStatus,
  taskStatuses,
  taskStatusLabelKey,
} from "../lib/tasks";

/** Adding a task, optionally under a parent the caller already picked. */
export type TaskModalState = { mode: "create"; parentTaskId?: number };

interface TaskFormValues {
  title: string;
  description: string;
  status: TaskStatus;
  assigneeUserId: string | null;
  startDate: string | null;
  dueDate: string | null;
  estimateHours: number | string;
  parentTaskId: string | null;
}

const hours = (value: number | string): number | undefined => {
  if (typeof value === "number") return value;
  const trimmed = value.trim();
  return trimmed === "" ? undefined : Number(trimmed);
};

export interface TaskFormModalProps {
  projectId: number;
  state: TaskModalState | null;
  onClose: () => void;
}

/**
 * Adds one task to the project (design §8). The form lives in `TaskForm`,
 * which the modal mounts fresh every time it opens, so no previous task's
 * values survive a close.
 */
export const TaskFormModal = ({ projectId, state, onClose }: TaskFormModalProps) => {
  const { t } = useI18n("projects");
  return (
    <Modal opened={state !== null} onClose={onClose} title={t("createTaskTitle")} centered size="lg">
      {state && <TaskForm projectId={projectId} state={state} onClose={onClose} />}
    </Modal>
  );
};

const TaskForm = ({ projectId, state, onClose }: TaskFormModalProps & { state: TaskModalState }) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  // Only a top-level task may be a parent, which is exactly what the tree's
  // own rows are; their subtasks are one level down and never offered.
  const { data: tree } = useQuery(projectTasksQueryOptions(projectId));

  const form = useForm<TaskFormValues>({
    initialValues: {
      title: "",
      description: "",
      status: "todo",
      assigneeUserId: null,
      startDate: null,
      dueDate: null,
      estimateHours: "",
      parentTaskId: state.parentTaskId === undefined ? null : String(state.parentTaskId),
    },
    validate: {
      title: (value) => {
        const title = value.trim();
        if (!title) return t("taskTitleRequired");
        return title.length > TASK_TITLE_MAX ? t("taskTitleTooLong") : null;
      },
      description: (value) => (value.trim().length > TASK_DESCRIPTION_MAX ? t("descriptionTooLong") : null),
      estimateHours: (value) => {
        const estimate = hours(value);
        return estimate !== undefined && estimate <= 0 ? t("estimateMustBePositive") : null;
      },
      dueDate: (value, values) => (value && values.startDate && value < values.startDate ? t("dueBeforeStart") : null),
    },
  });

  const mutation = useMutation({
    mutationFn: (values: TaskFormValues) => {
      const input: TaskInput = {
        title: values.title.trim(),
        description: values.description.trim() || null,
        status: values.status,
        assigneeUserId: values.assigneeUserId,
        startDate: values.startDate,
        dueDate: values.dueDate,
        estimateHours: hours(values.estimateHours) ?? null,
        parentTaskId: values.parentTaskId === null ? null : Number(values.parentTaskId),
      };
      return createTask(projectId, input);
    },
    onSuccess: (task) => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      onClose();
      notifications.show({ color: "teal", title: t("taskCreated"), message: task.title });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSaveTask"), message: error.message });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
      <Stack>
        <TextInput label={t("taskTitle")} withAsterisk data-autofocus {...form.getInputProps("title")} />
        <Textarea label={t("description")} rows={3} {...form.getInputProps("description")} />
        <Group grow align="start">
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
            onChange={(value) => form.setFieldValue("assigneeUserId", value)}
          />
        </Group>
        <Group grow align="start">
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
        </Group>
        <Group grow align="start">
          <NumberInput label={t("estimateHours")} min={0} decimalScale={2} {...form.getInputProps("estimateHours")} />
          <Select
            label={t("parentTask")}
            placeholder={t("noParentTask")}
            clearable
            data={(tree ?? []).map((task) => ({ value: String(task.id), label: task.title }))}
            value={form.values.parentTaskId}
            onChange={(value) => form.setFieldValue("parentTaskId", value)}
          />
        </Group>
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={mutation.isPending}>
            {t("create")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
