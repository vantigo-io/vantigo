import {
  Alert,
  Button,
  Drawer,
  em,
  Group,
  NumberInput,
  SegmentedControl,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { DatePickerInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import {
  allocationStatuses,
  confirmAllocation,
  createAllocation,
  deleteAllocation,
  type PlanningAllocation,
  type PlanningAllocationInput,
  type PlanningAllocationStatus,
  type PlanningAllocationWritten,
  updateAllocation,
} from "../api/allocations";
import { type PlanningLoadUnit, planningMetaQueryOptions } from "../api/meta";
import type { PlanningProjectStatus } from "../api/projects";
import { ApiValidationError, isRevisionConflict, PLANNING_QUERY_KEY, refusalCode, ruleRefusals } from "../api/request";
import { usePlanningFormat } from "../lib/format";
import { PersonPicker, type PickedPerson } from "./person-picker";
import { type PickedProject, ProjectPicker } from "./project-picker";
import { WarningList } from "./request-sheet";
import { TermsSection } from "./terms-section";

/** What the sheet opens with when it creates: the board's cell, the project's plan, a person's timeline. */
export interface AllocationPreset {
  person?: PickedPerson;
  project?: PickedProject;
  fromDate?: string;
  toDate?: string;
}

export interface AllocationSheetProps {
  opened: boolean;
  onClose: () => void;
  /** The row to change; left out, a new one. */
  allocation?: PlanningAllocation;
  preset?: AllocationPreset;
}

/** The loads S2 allows, by unit. */
const MAX_LOAD: Record<PlanningLoadUnit, number> = { percent: 100, hours_per_week: 80 };

/** A row on a planned project starts tentative, on any other confirmed (S3). */
const statusFor = (project?: PlanningProjectStatus): PlanningAllocationStatus =>
  project === "planned" ? "tentative" : "confirmed";

/**
 * The allocation sheet (design §7): who, on what, when, how much, how sure,
 * the role and the planners' note. A drawer at the side, a bottom sheet on a
 * phone; its form opens fresh each time it is shown. A row being changed
 * shows its terms under the form (S14, delivery C). It is `planning:manage`'s
 * — nothing offers it to anyone else.
 */
export const AllocationSheet = ({ opened, onClose, allocation, preset }: AllocationSheetProps) => {
  const { t } = usePlanningFormat();
  const phone = useMediaQuery(`(max-width: ${em(767.98)})`);
  return (
    <Drawer
      opened={opened}
      onClose={onClose}
      title={allocation ? t("editAllocation") : t("newAllocation")}
      position={phone ? "bottom" : "right"}
      size={phone ? "90%" : "md"}
      closeButtonProps={{ "aria-label": t("close") }}
    >
      {opened && (
        <AllocationForm key={allocation?.id ?? "new"} allocation={allocation} preset={preset} onDone={onClose} />
      )}
    </Drawer>
  );
};

interface FormValues {
  personId: number | null;
  projectId: number | null;
  period: [string | null, string | null];
  loadUnit: PlanningLoadUnit;
  load: number | string;
  status: PlanningAllocationStatus;
  title: string;
  note: string;
}

const AllocationForm = ({
  allocation,
  preset,
  onDone,
}: {
  allocation?: PlanningAllocation;
  preset?: AllocationPreset;
  onDone: () => void;
}) => {
  const { t, personName, projectName, week } = usePlanningFormat();
  const queryClient = useQueryClient();
  const meta = useQuery(planningMetaQueryOptions());
  const [statusTouched, setStatusTouched] = useState(allocation !== undefined);
  const [written, setWritten] = useState<PlanningAllocationWritten>();
  const [refusal, setRefusal] = useState<string>();

  const person: PickedPerson | undefined = allocation
    ? {
        id: allocation.person.id,
        label: allocation.person.employeeNumber
          ? `${personName(allocation.person)} (${allocation.person.employeeNumber})`
          : personName(allocation.person),
      }
    : preset?.person;
  const project: PickedProject | undefined = allocation
    ? { id: allocation.project.id, label: projectName(allocation.project), status: allocation.project.status }
    : preset?.project;

  const form = useForm<FormValues>({
    initialValues: {
      personId: person?.id ?? null,
      projectId: project?.id ?? null,
      period: [allocation?.fromDate ?? preset?.fromDate ?? null, allocation?.toDate ?? preset?.toDate ?? null],
      loadUnit: allocation?.loadUnit ?? meta.data?.settings?.defaultLoadUnit ?? "percent",
      load: allocation?.load ?? "",
      status: allocation?.status ?? statusFor(project?.status),
      title: allocation?.title ?? "",
      note: allocation?.note ?? "",
    },
    validate: {
      personId: (value) => (value === null ? t("personRequired") : null),
      projectId: (value) => (value === null ? t("projectRequired") : null),
      period: ([from, to]) => (!from || !to ? t("periodRequired") : null),
      load: (value, values) =>
        value === "" || Number(value) <= 0 || Number(value) > MAX_LOAD[values.loadUnit]
          ? t(values.loadUnit === "percent" ? "loadPercentRange" : "loadHoursRange")
          : null,
      title: (value) => (value.length > 100 ? t("textTooLong", { max: 100 }) : null),
      note: (value) => (value.length > 500 ? t("textTooLong", { max: 500 }) : null),
    },
  });

  const inputOf = (values: FormValues): PlanningAllocationInput => ({
    personId: values.personId as number,
    projectId: values.projectId as number,
    fromDate: values.period[0] as string,
    toDate: values.period[1] as string,
    load: Number(values.load),
    loadUnit: values.loadUnit,
    status: values.status,
    ...(values.title.trim() ? { title: values.title.trim() } : {}),
    ...(values.note.trim() ? { note: values.note.trim() } : {}),
  });

  const afterWrite = async (answer: PlanningAllocationWritten, saved: string) => {
    await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
    if (answer.warnings.length > 0) {
      // The consequence is said where the planner is looking (rule 6); the row is written.
      setWritten(answer);
      return;
    }
    notifications.show({ color: "teal", title: saved, message: "" });
    onDone();
  };

  const refused = async (error: Error) => {
    if (isRevisionConflict(error)) {
      notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
      onDone();
      return;
    }
    const code = refusalCode(error);
    if (error instanceof ApiValidationError) {
      const fields = error.fieldErrors;
      const period = fields.fromDate ?? fields.toDate;
      form.setErrors({
        personId: fields.personId,
        projectId: fields.projectId,
        period: code === "period_too_long" ? t("refusal.period_too_long") : period,
        load: fields.load ?? fields.loadUnit,
        status: fields.status,
        title: fields.title,
        note: fields.note,
      });
      return;
    }
    setRefusal(code && ruleRefusals.includes(code) ? t(`refusal.${code}`) : error.message);
  };

  const save = useMutation({
    mutationFn: (values: FormValues) =>
      allocation
        ? updateAllocation(allocation.id, inputOf(values), allocation.revision)
        : createAllocation(inputOf(values)),
    onMutate: () => setRefusal(undefined),
    onSuccess: (answer) => afterWrite(answer, t("allocationSaved")),
    onError: refused,
  });
  const confirm = useMutation({
    mutationFn: () => confirmAllocation((allocation as PlanningAllocation).id),
    onMutate: () => setRefusal(undefined),
    onSuccess: (answer) => afterWrite(answer, t("allocationConfirmed")),
    onError: refused,
  });
  const remove = useMutation({
    mutationFn: () => deleteAllocation((allocation as PlanningAllocation).id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("allocationDeleted"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
      onDone();
    },
    onError: refused,
  });

  if (written)
    return (
      <Stack>
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("savedWithWarnings")}>
          <WarningList warnings={written.warnings} week={week} />
        </Alert>
        <Group justify="flex-end">
          <Button onClick={onDone}>{t("close")}</Button>
        </Group>
      </Stack>
    );

  const unit = form.values.loadUnit;
  return (
    <Stack gap="xl">
      <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
        <Stack>
          <PersonPicker
            label={t("person")}
            value={form.values.personId}
            selected={person}
            onChange={(id) => form.setFieldValue("personId", id)}
            error={form.errors.personId as string | undefined}
          />
          <ProjectPicker
            label={t("project")}
            value={form.values.projectId}
            selected={project}
            onChange={(id, picked) => {
              form.setFieldValue("projectId", id);
              if (!statusTouched && picked) form.setFieldValue("status", statusFor(picked.status));
            }}
            error={form.errors.projectId as string | undefined}
          />
          <DatePickerInput
            type="range"
            label={t("period")}
            description={t("periodDescription")}
            withAsterisk
            allowSingleDateInRange
            valueFormat={t("dateInputFormat")}
            {...form.getInputProps("period")}
          />
          <Stack gap={4}>
            <Text size="sm" fw={500}>
              {t("load")}
            </Text>
            <SegmentedControl
              aria-label={t("loadUnit")}
              value={unit}
              onChange={(value) => form.setFieldValue("loadUnit", value as PlanningLoadUnit)}
              data={(meta.data?.loadUnits ?? ["percent", "hours_per_week"]).map((value) => ({
                value,
                label: t(`loadUnit_${value}`),
              }))}
            />
            <NumberInput
              aria-label={unit === "percent" ? t("loadPercentField") : t("loadHoursField")}
              min={0}
              max={MAX_LOAD[unit]}
              decimalScale={2}
              allowNegative={false}
              suffix={unit === "percent" ? " %" : undefined}
              {...form.getInputProps("load")}
            />
          </Stack>
          <Stack gap={4}>
            <Text size="sm" fw={500}>
              {t("status")}
            </Text>
            <SegmentedControl
              aria-label={t("status")}
              value={form.values.status}
              onChange={(value) => {
                setStatusTouched(true);
                form.setFieldValue("status", value as PlanningAllocationStatus);
              }}
              data={allocationStatuses.map((value) => ({ value, label: t(`status_${value}`) }))}
            />
            <Text size="xs" c="dimmed">
              {t(`statusDescription_${form.values.status}`)}
            </Text>
          </Stack>
          <TextInput
            label={t("allocationTitle")}
            description={t("allocationTitleDescription")}
            {...form.getInputProps("title")}
          />
          <Textarea label={t("note")} description={t("noteDescription")} rows={3} {...form.getInputProps("note")} />
          {refusal && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
              {refusal}
            </Alert>
          )}
          <Group justify="space-between">
            <Group gap="xs">
              {allocation && (
                <Button
                  variant="subtle"
                  color="red"
                  loading={remove.isPending}
                  onClick={() =>
                    modals.openConfirmModal({
                      title: t("deleteAllocationTitle"),
                      children: <Text size="sm">{t("deleteAllocationExplained")}</Text>,
                      labels: { confirm: t("delete"), cancel: t("cancel") },
                      confirmProps: { color: "red" },
                      onConfirm: () => remove.mutate(),
                    })
                  }
                >
                  {t("delete")}
                </Button>
              )}
              {allocation?.status === "tentative" && (
                <Button variant="light" loading={confirm.isPending} onClick={() => confirm.mutate()}>
                  {t("confirm")}
                </Button>
              )}
            </Group>
            <Button type="submit" loading={save.isPending}>
              {t("save")}
            </Button>
          </Group>
        </Stack>
      </form>
      {/* The terms are their own resource, with their own revision (S14): a form of their own, beside this one. */}
      {allocation && <TermsSection allocation={allocation} onDone={onDone} />}
    </Stack>
  );
};
