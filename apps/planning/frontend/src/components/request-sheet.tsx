import {
  ActionIcon,
  Alert,
  Button,
  Drawer,
  em,
  Fieldset,
  Group,
  List,
  NumberInput,
  Select,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { DatePickerInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { randomId, useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle, IconInfoCircle, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { PlanningWarning } from "../api/allocations";
import { competenceLevels, competencesQueryOptions } from "../api/competences";
import { ApiValidationError, isRevisionConflict, PLANNING_QUERY_KEY, refusalCode, ruleRefusals } from "../api/request";
import {
  createRequest,
  MAX_REQUIREMENTS,
  type PlanningRequest,
  type PlanningRequestInput,
  type PlanningRequestWritten,
  requestQueryOptions,
  updateRequest,
} from "../api/requests";
import { usePlanningFormat } from "../lib/format";
import { CompetencePicker } from "./competence-picker";
import { type PickedProject, ProjectPicker } from "./project-picker";

/** What the sheet opens with when it creates: the project's plan names the project and the window's first day. */
export interface RequestPreset {
  project?: PickedProject;
  fromDate?: string;
  toDate?: string;
}

export interface RequestSheetProps {
  opened: boolean;
  onClose: () => void;
  /**
   * The request to change, by id; left out, a new one. The sheet reads it
   * afresh, so a save refused as request_not_open, which reloads it, leaves
   * the sheet open and read-only.
   */
  requestId?: number;
  preset?: RequestPreset;
}

/** The most hours a week a request may ask for (S16). */
const MAX_HOURS = 80;

/**
 * The request sheet (design S16, §7): the project, the title, the period, the
 * hours a week — a request has no person whose position a percentage could be
 * read against — the competences the person must hold, and the planners'
 * note. A drawer at the side, a bottom sheet on a phone. A filled or withdrawn
 * request is shown, not changed: its allocation is changed instead.
 * `planning:manage`'s — nothing offers it to anyone else.
 */
export const RequestSheet = ({ opened, onClose, requestId, preset }: RequestSheetProps) => {
  const { t } = usePlanningFormat();
  const phone = useMediaQuery(`(max-width: ${em(767.98)})`);
  return (
    <Drawer
      opened={opened}
      onClose={onClose}
      title={requestId !== undefined ? t("editRequest") : t("newRequest")}
      position={phone ? "bottom" : "right"}
      size={phone ? "90%" : "md"}
      closeButtonProps={{ "aria-label": t("close") }}
    >
      {opened &&
        (requestId === undefined ? (
          <RequestForm key="new" preset={preset} onDone={onClose} />
        ) : (
          <StoredRequestForm key={requestId} requestId={requestId} onDone={onClose} />
        ))}
    </Drawer>
  );
};

/** A stored request's form, once the request is read; it follows the request as it is reloaded. */
const StoredRequestForm = ({ requestId, onDone }: { requestId: number; onDone: () => void }) => {
  const { t } = usePlanningFormat();
  const stored = useQuery(requestQueryOptions(requestId));
  if (stored.data) return <RequestForm request={stored.data} onDone={onDone} />;
  if (stored.isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadRequest")}>
        {stored.error.message}
      </Alert>
    );
  return <ContentSkeleton rows={5} rowHeight={44} />;
};

interface RequirementValues {
  key: string;
  competenceId: number | null;
  minLevel: string | null;
}

interface FormValues {
  projectId: number | null;
  title: string;
  period: [string | null, string | null];
  hoursPerWeek: number | string;
  note: string;
  requirements: RequirementValues[];
}

/** A server field (`requirements[0].competenceId`) as the form's path (`requirements.0.competenceId`). */
const formPath = (field: string): string => {
  if (field === "fromDate" || field === "toDate") return "period";
  return field.replace(/\[(\d+)\]/g, ".$1");
};

const RequestForm = ({
  request,
  preset,
  onDone,
}: {
  request?: PlanningRequest;
  preset?: RequestPreset;
  onDone: () => void;
}) => {
  const format = usePlanningFormat();
  const { t, projectName, week } = format;
  const queryClient = useQueryClient();
  const catalogue = useQuery(competencesQueryOptions());
  const [written, setWritten] = useState<PlanningRequestWritten>();
  const [refusal, setRefusal] = useState<string>();
  // The revision the sheet was opened at: a reload meanwhile must not make a
  // stale form overwrite a newer request.
  const [revision] = useState(request?.revision);
  const editable = request === undefined || request.status === "open";

  const project: PickedProject | undefined = request
    ? { id: request.project.id, label: projectName(request.project), status: request.project.status }
    : preset?.project;

  const form = useForm<FormValues>({
    initialValues: {
      projectId: project?.id ?? null,
      title: request?.title ?? "",
      period: [request?.fromDate ?? preset?.fromDate ?? null, request?.toDate ?? preset?.toDate ?? null],
      hoursPerWeek: request?.hoursPerWeek ?? "",
      note: request?.note ?? "",
      requirements: (request?.requirements ?? []).map((requirement) => ({
        key: randomId(),
        competenceId: requirement.competenceId,
        minLevel: requirement.minLevel === undefined ? null : String(requirement.minLevel),
      })),
    },
    validate: {
      projectId: (value) => (value === null ? t("projectRequired") : null),
      title: (value) =>
        value.trim() === "" ? t("titleRequired") : value.trim().length > 100 ? t("textTooLong", { max: 100 }) : null,
      period: ([from, to]) => (!from || !to ? t("periodRequired") : null),
      hoursPerWeek: (value) =>
        value === "" || Number(value) <= 0 || Number(value) > MAX_HOURS ? t("loadHoursRange") : null,
      note: (value) => (value.length > 500 ? t("textTooLong", { max: 500 }) : null),
      requirements: { competenceId: (value) => (value === null ? t("competenceRequired") : null) },
    },
  });

  const hasLevel = (competenceId: number | null) =>
    competenceId !== null &&
    (catalogue.data?.find((competence) => competence.id === competenceId)?.hasLevel ??
      request?.requirements.find((requirement) => requirement.competenceId === competenceId)?.hasLevel ??
      false);

  const inputOf = (values: FormValues): PlanningRequestInput => ({
    projectId: values.projectId as number,
    title: values.title.trim(),
    fromDate: values.period[0] as string,
    toDate: values.period[1] as string,
    hoursPerWeek: Number(values.hoursPerWeek),
    ...(values.note.trim() ? { note: values.note.trim() } : {}),
    requirements: values.requirements.map((requirement) => ({
      competenceId: requirement.competenceId as number,
      ...(requirement.minLevel !== null && hasLevel(requirement.competenceId)
        ? { minLevel: Number(requirement.minLevel) }
        : {}),
    })),
  });

  /** Whether a form path is a field the sheet shows, so its error is seen there. */
  const rendered = (path: string) => {
    if (["projectId", "title", "period", "hoursPerWeek", "note", "requirements"].includes(path)) return true;
    const row = /^requirements\.(\d+)\.(competenceId|minLevel)$/.exec(path);
    const requirement = row ? form.values.requirements[Number(row[1])] : undefined;
    if (!row || !requirement) return false;
    return row[2] === "competenceId" || hasLevel(requirement.competenceId);
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
      const errors: Record<string, string> = {};
      for (const [field, message] of Object.entries(error.fieldErrors)) errors[formPath(field)] = message;
      if (code === "period_too_long" || code === "window_out_of_bounds") errors.period = t(`refusal.${code}`);
      form.setErrors(errors);
      // An error on a field the sheet does not show is said above the buttons, not dropped.
      if (Object.keys(errors).some((path) => !rendered(path))) setRefusal(error.message);
      return;
    }
    setRefusal(code && ruleRefusals.includes(code) ? t(`refusal.${code}`) : error.message);
    if (code === "request_not_open") await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
  };

  const save = useMutation({
    mutationFn: (values: FormValues) =>
      request ? updateRequest(request.id, inputOf(values), revision as number) : createRequest(inputOf(values)),
    onMutate: () => setRefusal(undefined),
    onSuccess: async (answer) => {
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
      if (answer.warnings.length > 0) {
        setWritten(answer);
        return;
      }
      notifications.show({ color: "teal", title: t("requestSaved"), message: "" });
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

  const chosen = form.values.requirements.flatMap((requirement) =>
    requirement.competenceId === null ? [] : [requirement.competenceId],
  );
  const listError = form.errors.requirements as string | undefined;

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        {!editable && (
          <Alert color="blue" icon={<IconInfoCircle size={16} />}>
            {t("requestNotEditable")}
          </Alert>
        )}
        <Fieldset variant="unstyled" disabled={!editable}>
          <Stack>
            <ProjectPicker
              label={t("project")}
              value={form.values.projectId}
              selected={project}
              onChange={(id) => form.setFieldValue("projectId", id)}
              error={form.errors.projectId as string | undefined}
            />
            <TextInput
              label={t("requestTitle")}
              description={t("requestTitleDescription")}
              withAsterisk
              {...form.getInputProps("title")}
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
            <NumberInput
              label={t("loadHoursField")}
              withAsterisk
              min={0}
              max={MAX_HOURS}
              decimalScale={2}
              allowNegative={false}
              {...form.getInputProps("hoursPerWeek")}
            />
            <Stack gap={6}>
              <div>
                <Text size="sm" fw={500}>
                  {t("requirements")}
                </Text>
                <Text size="xs" c="dimmed">
                  {t("requirementsDescription")}
                </Text>
              </div>
              {form.values.requirements.map((requirement, index) => (
                <Group key={requirement.key} align="flex-start" gap="xs" wrap="nowrap">
                  <div style={{ flex: 2, minWidth: 0 }}>
                    <CompetencePicker
                      label={t("requirementLabel", { index: index + 1 })}
                      value={requirement.competenceId}
                      exclude={chosen.filter((id) => id !== requirement.competenceId)}
                      onChange={(id) => {
                        form.setFieldValue(`requirements.${index}.competenceId`, id);
                        if (!hasLevel(id)) form.setFieldValue(`requirements.${index}.minLevel`, null);
                      }}
                      error={form.errors[`requirements.${index}.competenceId`] as string | undefined}
                    />
                  </div>
                  {hasLevel(requirement.competenceId) && (
                    <Select
                      style={{ flex: 1, minWidth: 0 }}
                      label={t("minLevel")}
                      placeholder={t("anyLevel")}
                      clearable
                      data={competenceLevels.map((level) => ({ value: String(level), label: t(`level_${level}`) }))}
                      {...form.getInputProps(`requirements.${index}.minLevel`)}
                    />
                  )}
                  <ActionIcon
                    variant="subtle"
                    color="red"
                    mt={26}
                    aria-label={t("removeRequirement", { index: index + 1 })}
                    onClick={() => form.removeListItem("requirements", index)}
                  >
                    <IconTrash size={16} />
                  </ActionIcon>
                </Group>
              ))}
              {listError && (
                <Text size="xs" c="red">
                  {listError}
                </Text>
              )}
              {form.values.requirements.length < MAX_REQUIREMENTS && (
                <Group>
                  <Button
                    variant="subtle"
                    size="xs"
                    leftSection={<IconPlus size={14} />}
                    onClick={() =>
                      form.insertListItem("requirements", { key: randomId(), competenceId: null, minLevel: null })
                    }
                  >
                    {t("addRequirement")}
                  </Button>
                </Group>
              )}
            </Stack>
            <Textarea
              label={t("note")}
              description={t("requestNoteDescription")}
              rows={3}
              {...form.getInputProps("note")}
            />
          </Stack>
        </Fieldset>
        {refusal && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {refusal}
          </Alert>
        )}
        <Group justify="flex-end">
          {editable ? (
            <Button type="submit" loading={save.isPending}>
              {t("save")}
            </Button>
          ) : (
            <Button variant="default" onClick={onDone}>
              {t("close")}
            </Button>
          )}
        </Group>
      </Stack>
    </form>
  );
};

/** A write's warnings in the words the allocation sheet uses (rule 6). */
export const WarningList = ({
  warnings,
  week,
}: {
  warnings: readonly PlanningWarning[];
  week: (monday: string) => string;
}) => {
  const { t, date } = usePlanningFormat();
  return (
    <List size="sm" spacing={4}>
      {warnings.map((warning) => (
        <List.Item key={`${warning.code}/${warning.weeks?.join(",") ?? ""}/${warning.allocationId ?? ""}`}>
          {t(`warning_${warning.code}`, {
            weeks: (warning.weeks ?? []).map(week).join(", "),
            date: warning.date ? date(warning.date) : "",
            allocationId: warning.allocationId ?? "",
          })}
        </List.Item>
      ))}
    </List>
  );
};
