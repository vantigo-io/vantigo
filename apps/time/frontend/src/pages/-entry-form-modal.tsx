import { Button, Group, Modal, Select, Stack, Switch, Textarea, TextInput } from "@mantine/core";
import { DateInput, TimeInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import {
  ApiValidationError,
  createTimeEntry,
  type TimeEntry,
  type TimeEntryInput,
  updateTimeEntry,
} from "../api/entries";
import {
  isLoggable,
  myOpenTasksQueryOptions,
  myProjectsQueryOptions,
  NON_BILLABLE,
  projectBillingLinesQueryOptions,
  projectWorkTypesQueryOptions,
} from "../api/projects";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useHoursFormat } from "../lib/hours";
import { rowLabel } from "../lib/rows";
import { hoursBetween, parseHours } from "../lib/week";

/** Logging time on a day, or changing one entry. */
export type EntryModalState = { mode: "create"; date: string } | { mode: "edit"; entry: TimeEntry };

export interface EntryFormModalProps {
  state: EntryModalState | null;
  onClose: () => void;
}

/** What the server allows in a note (and refuses on `note` past it). */
export const NOTE_MAX_LENGTH = 2000;

interface EntryFormValues {
  projectId: string | null;
  billingLineId: string | null;
  taskId: string | null;
  workTypeId: string | null;
  entryDate: string | null;
  hours: string;
  startTime: string;
  endTime: string;
  note: string;
  billable: boolean;
}

/** The request fields a refusal may name that the form has an input for. */
const formFields = new Set([
  "projectId",
  "billingLineId",
  "taskId",
  "workTypeId",
  "entryDate",
  "hours",
  "startTime",
  "endTime",
  "note",
]);

type Option = { value: string; label: string };

/** The options, with the entry's own choice kept when the list no longer offers it (an inactive line, say). */
const withCurrent = (options: Option[], current: Option | null): Option[] =>
  current && !options.some((option) => option.value === current.value) ? [current, ...options] : options;

/**
 * Adds an entry or edits one (design D1): a project with an optional line and
 * task, and a work type when the project has one, the day, and the duration —
 * typed as hours, or given as a start and an end time from which the hours
 * are worked out, exactly as the server will work them out again. The form
 * lives in `EntryForm`, which the modal mounts fresh every time it opens, so
 * no previous entry's values survive.
 */
export const EntryFormModal = ({ state, onClose }: EntryFormModalProps) => {
  const { t } = useI18n("time");
  return (
    <Modal
      opened={state !== null}
      onClose={onClose}
      title={state?.mode === "edit" ? t("editEntryTitle") : t("addEntryTitle")}
      centered
      size="lg"
    >
      {state && <EntryForm state={state} onClose={onClose} />}
    </Modal>
  );
};

const EntryForm = ({ state, onClose }: { state: EntryModalState; onClose: () => void }) => {
  const { t } = useI18n("time");
  const queryClient = useQueryClient();
  const format = useHoursFormat();
  const editing = state.mode === "edit" ? state.entry : undefined;

  const form = useForm<EntryFormValues>({
    initialValues: editing
      ? {
          projectId: String(editing.projectId),
          billingLineId: editing.billingLineId == null ? null : String(editing.billingLineId),
          taskId: editing.taskId == null ? null : String(editing.taskId),
          workTypeId: editing.workType ? String(editing.workType.id) : null,
          entryDate: editing.entryDate,
          hours: format.input(editing.hours),
          startTime: editing.startTime ?? "",
          endTime: editing.endTime ?? "",
          note: editing.note ?? "",
          billable: editing.billable,
        }
      : {
          projectId: null,
          billingLineId: null,
          taskId: null,
          workTypeId: null,
          entryDate: state.mode === "create" ? state.date : null,
          hours: "",
          startTime: "",
          endTime: "",
          note: "",
          billable: true,
        },
    validate: {
      projectId: (value) => (value ? null : t("projectRequired")),
      entryDate: (value) => (value ? null : t("dateRequired")),
      startTime: (value, values) => (!value && values.endTime ? t("bothTimesOrNeither") : null),
      endTime: (value, values) => {
        if (!value && values.startTime) return t("bothTimesOrNeither");
        if (value && values.startTime && hoursBetween(values.startTime, value) === undefined) return t("endAfterStart");
        return null;
      },
      hours: (value, values) => {
        if (values.startTime || values.endTime) return null;
        if (!value.trim()) return t("hoursRequired");
        return parseHours(value) === undefined ? t("invalidHours") : null;
      },
      note: (value) => (value.trim().length > NOTE_MAX_LENGTH ? t("noteTooLong") : null),
    },
  });

  const projectId = form.values.projectId === null ? null : Number(form.values.projectId);
  const { data: projects } = useQuery(myProjectsQueryOptions());
  const { data: lines } = useQuery({ ...projectBillingLinesQueryOptions(projectId ?? 0), enabled: projectId !== null });
  const { data: tasks } = useQuery(myOpenTasksQueryOptions());
  // The project's active work types (work types design D5). Only a project
  // that has one shows the select; ordinary hours are the empty choice.
  const { data: workTypes } = useQuery({
    ...projectWorkTypesQueryOptions(projectId ?? 0),
    enabled: projectId !== null,
  });

  const project = projects?.find((candidate) => candidate.id === projectId);
  // The billable switch is offered only when the project's billing type is
  // known and bills; otherwise the server's default stands.
  const billingKnown = project !== undefined;
  const showBillable = billingKnown && project.billingType !== NON_BILLABLE;
  const derived = hoursBetween(form.values.startTime, form.values.endTime);

  const sameProject = editing && editing.projectId === projectId;
  const projectOptions = withCurrent(
    (projects ?? []).filter(isLoggable).map((p) => ({ value: String(p.id), label: `${p.code} · ${p.name}` })),
    editing ? { value: String(editing.projectId), label: `${editing.projectCode} · ${editing.projectName}` } : null,
  );
  const lineOptions = withCurrent(
    (lines ?? []).map((line) => ({
      value: String(line.id),
      label: line.productName ? `${line.code} · ${line.productName}` : line.code,
    })),
    sameProject && editing.billingLineId != null
      ? { value: String(editing.billingLineId), label: editing.billingLineCode ?? String(editing.billingLineId) }
      : null,
  );
  const taskOptions = withCurrent(
    (tasks ?? [])
      .filter((task) => task.projectId === projectId)
      .map((task) => ({ value: String(task.id), label: task.title })),
    sameProject && editing.taskId != null
      ? { value: String(editing.taskId), label: editing.taskTitle ?? String(editing.taskId) }
      : null,
  );
  // An entry keeps its own type even once it is retired, so the form shows
  // what it was logged as; the server refuses it on save until another is
  // picked, and that refusal lands on this field.
  const workTypeOptions = withCurrent(
    (workTypes ?? []).map((type) => ({ value: String(type.id), label: type.name })),
    sameProject && editing.workType ? { value: String(editing.workType.id), label: editing.workType.name } : null,
  );

  const pickProject = (value: string | null) => {
    form.setValues({ projectId: value, billingLineId: null, taskId: null, workTypeId: null });
  };

  const billableToSend = (): boolean | undefined => {
    if (billingKnown) return showBillable ? form.values.billable : false;
    return editing?.billable;
  };

  const mutation = useMutation({
    mutationFn: (values: EntryFormValues) => {
      const fromTimes = hoursBetween(values.startTime, values.endTime);
      const times = fromTimes !== undefined;
      const input: TimeEntryInput = {
        projectId: Number(values.projectId),
        billingLineId: values.billingLineId === null ? null : Number(values.billingLineId),
        taskId: values.taskId === null ? null : Number(values.taskId),
        entryDate: values.entryDate ?? "",
        hours: fromTimes ?? parseHours(values.hours) ?? 0,
        startTime: times ? values.startTime : null,
        endTime: times ? values.endTime : null,
        note: values.note.trim() || null,
        ...(values.workTypeId === null ? {} : { workTypeId: Number(values.workTypeId) }),
      };
      const billable = billableToSend();
      if (billable !== undefined) input.billable = billable;
      return editing ? updateTimeEntry(editing.id, { ...input, revision: editing.revision }) : createTimeEntry(input);
    },
    onSuccess: async (saved) => {
      notifications.show({
        color: "teal",
        title: t("entrySaved"),
        message: `${rowLabel(saved)} · ${t("hoursShort", { hours: format.display(saved.hours) })}`,
      });
      onClose();
      await queryClient.invalidateQueries({ queryKey: ["time"] });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const fields = Object.fromEntries(Object.entries(error.fieldErrors).filter(([field]) => formFields.has(field)));
        if (Object.keys(fields).length > 0) {
          form.setErrors(fields);
          return;
        }
      }
      notifications.show({ color: "red", title: t("couldNotSaveEntry"), message: refusalMessage(error) });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
      <Stack>
        <Select
          label={t("project")}
          placeholder={t("chooseProject")}
          withAsterisk
          searchable
          data-autofocus
          data={projectOptions}
          value={form.values.projectId}
          onChange={pickProject}
          error={form.errors.projectId}
        />
        <Group grow align="start">
          <Select
            label={t("billingLine")}
            placeholder={t("noBillingLine")}
            clearable
            disabled={lineOptions.length === 0}
            data={lineOptions}
            {...form.getInputProps("billingLineId")}
          />
          <Select
            label={t("task")}
            placeholder={t("noTask")}
            clearable
            disabled={taskOptions.length === 0}
            data={taskOptions}
            {...form.getInputProps("taskId")}
          />
        </Group>
        {workTypeOptions.length > 0 && (
          <Select
            label={t("workType")}
            placeholder={t("ordinaryHours")}
            clearable
            data={workTypeOptions}
            {...form.getInputProps("workTypeId")}
          />
        )}
        <DateInput
          label={t("date")}
          valueFormat={t("dateInputFormat")}
          withAsterisk
          {...form.getInputProps("entryDate")}
        />
        <Group grow align="start">
          <TimeInput label={t("startTime")} {...form.getInputProps("startTime")} />
          <TimeInput label={t("endTime")} {...form.getInputProps("endTime")} />
          <TextInput
            label={t("hours")}
            inputMode="decimal"
            autoComplete="off"
            {...form.getInputProps("hours")}
            value={derived === undefined ? form.values.hours : format.input(derived)}
            disabled={derived !== undefined}
            description={derived === undefined ? undefined : t("hoursDerived")}
          />
        </Group>
        <Textarea label={t("note")} rows={3} {...form.getInputProps("note")} />
        {showBillable && <Switch label={t("billable")} {...form.getInputProps("billable", { type: "checkbox" })} />}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={mutation.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
