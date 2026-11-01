import {
  ActionIcon,
  Alert,
  Button,
  Divider,
  Group,
  Modal,
  NumberInput,
  Paper,
  Select,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconArrowDown, IconArrowUp, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { type ApiError, ApiValidationError } from "../api/request";
import { createProjectTemplate, type ProjectTemplate, updateProjectTemplate } from "../api/templates";
import "../i18n";
import {
  draftFromTemplate,
  emptyDependency,
  emptyMilestone,
  emptySubtask,
  emptyTask,
  formPathFromServerField,
  moveRow,
  removeMilestone,
  removeTask,
  type TemplateDraft,
  type TemplateDraftSubtask,
  templateInputFromDraft,
  validateTemplateDraft,
} from "../lib/templates";

/** Starting a template, or editing the one the caller just read off the catalog. */
export type TemplateModalState = { mode: "create" } | { mode: "edit"; template: ProjectTemplate };

export interface TemplateFormModalProps {
  state: TemplateModalState | null;
  onClose: () => void;
}

/**
 * Creates or edits one project template: its name and description, then its
 * delivery milestones, tasks (each with its subtasks) and the dependencies
 * between tasks. Every date is a day offset from the kickoff the template is
 * later applied with, so "T+14" is two weeks in. The form is mounted fresh
 * each time the modal opens, so no previous template's rows survive a close.
 */
export const TemplateFormModal = ({ state, onClose }: TemplateFormModalProps) => {
  const { t } = useI18n("projects");
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={state?.mode === "edit" ? t("editTemplateTitle") : t("createTemplateTitle")}
      size="xl"
      centered
    >
      {state && (
        <TemplateForm key={state.mode === "edit" ? state.template.id : "create"} state={state} onClose={onClose} />
      )}
    </Modal>
  );
};

/** Up, down and remove for one row of a list, labelled with what the row is. */
const RowActions = ({
  label,
  index,
  count,
  onMove,
  onRemove,
}: {
  label: string;
  index: number;
  count: number;
  onMove: (to: number) => void;
  onRemove: () => void;
}) => {
  const { t } = useI18n("projects");
  return (
    <Group gap={4} wrap="nowrap">
      <ActionIcon
        variant="subtle"
        aria-label={t("moveRowUp", { label })}
        disabled={index === 0}
        onClick={() => onMove(index - 1)}
      >
        <IconArrowUp size={14} />
      </ActionIcon>
      <ActionIcon
        variant="subtle"
        aria-label={t("moveRowDown", { label })}
        disabled={index === count - 1}
        onClick={() => onMove(index + 1)}
      >
        <IconArrowDown size={14} />
      </ActionIcon>
      <ActionIcon variant="subtle" color="red" aria-label={t("removeRow", { label })} onClick={onRemove}>
        <IconTrash size={14} />
      </ActionIcon>
    </Group>
  );
};

const Section = ({ title, action, children }: { title: string; action: ReactNode; children: ReactNode }) => (
  <Stack gap="xs">
    <Group justify="space-between">
      <Title order={5}>{title}</Title>
      {action}
    </Group>
    {children}
  </Stack>
);

const TemplateForm = ({ state, onClose }: TemplateFormModalProps & { state: TemplateModalState }) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();
  const template = state.mode === "edit" ? state.template : undefined;

  const form = useForm<TemplateDraft>({
    initialValues: draftFromTemplate(template),
    // The server's rules, by the form path of the row each is about, so the
    // message lands next to the field rather than in a toast.
    validate: (values) =>
      Object.fromEntries(Object.entries(validateTemplateDraft(values)).map(([path, key]) => [path, t(key)])),
  });

  const mutation = useMutation({
    mutationFn: (values: TemplateDraft) => {
      // billingType is not edited here; an update keeps whatever the template carries.
      const input = { ...templateInputFromDraft(values), billingType: template?.billingType ?? undefined };
      return template
        ? updateProjectTemplate(template.id, { ...input, revision: template.revision })
        : createProjectTemplate(input);
    },
    onSuccess: (saved) => {
      queryClient.invalidateQueries({ queryKey: ["projects", "templates"] });
      onClose();
      notifications.show({ color: "teal", title: t("templateSaved"), message: saved.name });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(
          Object.fromEntries(
            Object.entries(error.fieldErrors).map(([field, message]) => [formPathFromServerField(field), message]),
          ),
        );
        return;
      }
      const conflict = (error as ApiError).status === 409;
      notifications.show({
        color: "red",
        title: t("couldNotSaveTemplate"),
        message: conflict ? t("templateChangedElsewhere") : error.message,
      });
    },
  });

  const { milestones, tasks, dependencies } = form.values;
  // A structural change re-numbers rows, so errors keyed by the old numbers
  // would point at the wrong ones; they come back on the next submit.
  const restructure = (next: TemplateDraft) => {
    form.clearErrors();
    form.setValues(next);
  };

  const milestoneOptions = milestones.map((m, i) => ({
    value: m.key,
    label: m.name.trim() || t("templateMilestoneNumber", { number: i + 1 }),
  }));
  const taskOptions = tasks.map((task, i) => ({
    value: task.key,
    label: task.title.trim() || t("templateTaskNumber", { number: i + 1 }),
  }));

  const scheduleFields = (path: string) => (
    <Group grow align="start">
      <NumberInput
        label={t("templateStartDay")}
        min={0}
        allowDecimal={false}
        prefix="T+"
        {...form.getInputProps(`${path}.startDayOffset`)}
      />
      <NumberInput
        label={t("templateDueDay")}
        min={0}
        allowDecimal={false}
        prefix="T+"
        {...form.getInputProps(`${path}.dueDayOffset`)}
      />
      <NumberInput
        label={t("estimateHours")}
        min={0}
        decimalScale={2}
        {...form.getInputProps(`${path}.estimateHours`)}
      />
    </Group>
  );

  const subtaskRows = (taskIndex: number, subtasks: TemplateDraftSubtask[]) =>
    subtasks.map((subtask, j) => {
      const path = `tasks.${taskIndex}.subtasks.${j}`;
      const label = subtask.title.trim() || t("templateSubtaskNumber", { number: j + 1 });
      return (
        <Paper key={subtask.key} withBorder p="xs" radius="sm" ml="lg">
          <Stack gap="xs">
            <Group align="start" wrap="nowrap">
              <TextInput flex={1} label={t("subtaskTitle")} withAsterisk {...form.getInputProps(`${path}.title`)} />
              <RowActions
                label={label}
                index={j}
                count={subtasks.length}
                onMove={(to) =>
                  restructure({
                    ...form.values,
                    tasks: tasks.map((task, i) =>
                      i === taskIndex ? { ...task, subtasks: moveRow(task.subtasks, j, to) } : task,
                    ),
                  })
                }
                onRemove={() =>
                  restructure({
                    ...form.values,
                    tasks: tasks.map((task, i) =>
                      i === taskIndex
                        ? { ...task, subtasks: task.subtasks.filter((s) => s.key !== subtask.key) }
                        : task,
                    ),
                  })
                }
              />
            </Group>
            <Textarea label={t("description")} rows={1} {...form.getInputProps(`${path}.description`)} />
            {scheduleFields(path)}
          </Stack>
        </Paper>
      );
    });

  return (
    <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
      <Stack gap="md">
        <TextInput data-autofocus label={t("name")} withAsterisk {...form.getInputProps("name")} />
        <Textarea label={t("description")} rows={2} {...form.getInputProps("description")} />
        <Text size="xs" c="dimmed">
          {t("templateOffsetHelp")}
        </Text>

        <Divider />
        <Section
          title={t("deliveryMilestones")}
          action={
            <Button
              size="xs"
              variant="default"
              leftSection={<IconPlus size={14} />}
              onClick={() => form.insertListItem("milestones", emptyMilestone())}
            >
              {t("addTemplateMilestone")}
            </Button>
          }
        >
          {milestones.length === 0 && (
            <Text size="sm" c="dimmed">
              {t("noTemplateMilestones")}
            </Text>
          )}
          {milestones.map((m, i) => (
            <Paper key={m.key} withBorder p="sm" radius="sm">
              <Stack gap="xs">
                <Group align="start" wrap="nowrap">
                  <TextInput flex={1} label={t("name")} withAsterisk {...form.getInputProps(`milestones.${i}.name`)} />
                  <NumberInput
                    w={120}
                    label={t("templateTargetDay")}
                    withAsterisk
                    min={0}
                    allowDecimal={false}
                    prefix="T+"
                    {...form.getInputProps(`milestones.${i}.targetDayOffset`)}
                  />
                  <RowActions
                    label={milestoneOptions[i]?.label ?? ""}
                    index={i}
                    count={milestones.length}
                    onMove={(to) => restructure({ ...form.values, milestones: moveRow(milestones, i, to) })}
                    onRemove={() => restructure(removeMilestone(form.values, m.key))}
                  />
                </Group>
                <Textarea label={t("description")} rows={1} {...form.getInputProps(`milestones.${i}.description`)} />
              </Stack>
            </Paper>
          ))}
        </Section>

        <Divider />
        <Section
          title={t("tasks")}
          action={
            <Button
              size="xs"
              variant="default"
              leftSection={<IconPlus size={14} />}
              onClick={() => form.insertListItem("tasks", emptyTask())}
            >
              {t("addTemplateTask")}
            </Button>
          }
        >
          {tasks.length === 0 && (
            <Text size="sm" c="dimmed">
              {t("noTemplateTasks")}
            </Text>
          )}
          {tasks.map((task, i) => (
            <Paper key={task.key} withBorder p="sm" radius="sm">
              <Stack gap="xs">
                <Group align="start" wrap="nowrap">
                  <TextInput flex={1} label={t("taskTitle")} withAsterisk {...form.getInputProps(`tasks.${i}.title`)} />
                  <Select
                    w={200}
                    label={t("deliveryMilestone")}
                    placeholder={t("noDeliveryMilestone")}
                    data={milestoneOptions}
                    clearable
                    {...form.getInputProps(`tasks.${i}.milestoneKey`)}
                  />
                  <RowActions
                    label={taskOptions[i]?.label ?? ""}
                    index={i}
                    count={tasks.length}
                    onMove={(to) => restructure({ ...form.values, tasks: moveRow(tasks, i, to) })}
                    onRemove={() => restructure(removeTask(form.values, task.key))}
                  />
                </Group>
                <Textarea label={t("description")} rows={1} {...form.getInputProps(`tasks.${i}.description`)} />
                {scheduleFields(`tasks.${i}`)}
                {subtaskRows(i, task.subtasks)}
                <Group>
                  <Button
                    size="xs"
                    variant="subtle"
                    leftSection={<IconPlus size={14} />}
                    onClick={() => form.insertListItem(`tasks.${i}.subtasks`, emptySubtask())}
                  >
                    {t("addSubtask")}
                  </Button>
                </Group>
              </Stack>
            </Paper>
          ))}
        </Section>

        <Divider />
        <Section
          title={t("dependencies")}
          action={
            <Button
              size="xs"
              variant="default"
              leftSection={<IconPlus size={14} />}
              disabled={tasks.length < 2}
              onClick={() => form.insertListItem("dependencies", emptyDependency())}
            >
              {t("addTemplateDependency")}
            </Button>
          }
        >
          <Text size="xs" c="dimmed">
            {t("templateDependenciesHelp")}
          </Text>
          {form.errors.dependencies && (
            <Alert color="red" icon={<IconAlertCircle size={16} />}>
              {form.errors.dependencies}
            </Alert>
          )}
          {dependencies.map((d, i) => (
            <Group key={d.key} align="start" wrap="nowrap">
              <Select
                flex={1}
                label={t("templatePredecessor")}
                placeholder={t("selectTask")}
                data={taskOptions}
                withAsterisk
                {...form.getInputProps(`dependencies.${i}.predecessorKey`)}
              />
              <Select
                flex={1}
                label={t("templateSuccessor")}
                placeholder={t("selectTask")}
                data={taskOptions}
                withAsterisk
                {...form.getInputProps(`dependencies.${i}.successorKey`)}
              />
              <ActionIcon
                mt={28}
                variant="subtle"
                color="red"
                aria-label={t("removeRow", { label: t("templateDependencyNumber", { number: i + 1 }) })}
                onClick={() =>
                  restructure({ ...form.values, dependencies: dependencies.filter((other) => other.key !== d.key) })
                }
              >
                <IconTrash size={14} />
              </ActionIcon>
            </Group>
          ))}
        </Section>

        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={mutation.isPending}>
            {template ? t("saveChanges") : t("create")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
