import { Alert, Badge, Button, Group, NumberInput, Select, SimpleGrid, Stack, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconArchive, IconRestore, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { isRevisionConflict, NotFoundError, refusalCode, refusedFields } from "../api/request";
import { manufacturingSitesQueryOptions } from "../api/sites";
import {
  archiveWorkCenter,
  createWorkCenter,
  deleteWorkCenter,
  type ManufacturingWorkCenter,
  reactivateWorkCenter,
  updateWorkCenter,
  workCenterQueryOptions,
} from "../api/work-centers";
import { ClosuresPanel } from "../components/closures-panel";
import { PatternEditor } from "../components/pattern-editor";
import { useRecordActions } from "../lib/actions";
import { canManageBoms, canSeeCosts } from "../lib/capabilities";
import { refusalMessage, statusColor, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import { patternIssues } from "../lib/pattern";
import { MANUFACTURING_HOME_ROUTE_PATH, WORK_CENTERS_ROUTE_PATH, workCenterLinkOptions } from "../lib/routes";
import {
  type WorkCenterValues,
  workCenterInputOf,
  workCenterProblems,
  workCenterUpdateOf,
  workCenterValuesOf,
} from "../lib/work-center-body";

/** The fields a refusal can name that the form shows beside their input. */
const formFields = new Set([
  "code",
  "name",
  "siteId",
  "capacity",
  "timeEfficiency",
  "setupMinutes",
  "cleanupMinutes",
  "machineCostPerHour",
  "labourCostPerHour",
]);

/** Which field a refusal code belongs to, when it is one field's. */
const codeField: Record<string, string> = {
  work_center_code_taken: "code",
  site_unknown: "siteId",
  costs_view_required: "machineCostPerHour",
};

/**
 * A work center's form (M8, M9): its code, name and site, its capacity and
 * efficiency, its setup and cleanup, its cost rates — shown and sent only
 * with `manufacturing:costs-view` — and its weekly pattern. Saving needs
 * `manufacturing:boms-manage`; without it the form is read only.
 */
const WorkCenterForm = ({ center }: { center?: ManufacturingWorkCenter }) => {
  const { t } = useManufacturingFormat();
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const invalidate = useInvalidateManufacturing();
  const meta = useQuery(manufacturingMetaQueryOptions());
  const sites = useQuery(manufacturingSitesQueryOptions());
  const manage = canManageBoms(meta.data);
  const costs = canSeeCosts(meta.data);
  const [values, setValues] = useState<WorkCenterValues>(() => workCenterValuesOf(center));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [problem, setProblem] = useState<string>();
  const [showPatternIssues, setShowPatternIssues] = useState(false);
  const issues = patternIssues(values.pattern);
  const set = <Key extends keyof WorkCenterValues>(key: Key, value: WorkCenterValues[Key]) =>
    setValues((current) => ({ ...current, [key]: value }));

  const save = useMutation({
    mutationFn: () =>
      center
        ? updateWorkCenter(center.id, workCenterUpdateOf(values, costs, center.revision))
        : createWorkCenter(workCenterInputOf(values, costs)),
    onSuccess: async (saved) => {
      notifications.show({
        color: "teal",
        title: t(center ? "workCenterSaved" : "workCenterCreated"),
        message: saved.code,
      });
      await invalidate();
      if (!center) void navigate({ ...workCenterLinkOptions(saved.id), replace: true });
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await invalidate();
        return;
      }
      const fields = refusedFields(error);
      const shown = Object.fromEntries(Object.entries(fields).filter(([field]) => formFields.has(field)));
      const code = refusalCode(error);
      if (code && codeField[code]) shown[codeField[code]] = t(`refusal.${code}`);
      setErrors(shown);
      setProblem(refusalMessage(t, error));
    },
  });

  const submit = () => {
    const found = Object.fromEntries(
      Object.entries(workCenterProblems(values, costs)).map(([field, key]) => [field, t(`problem_${key}`)]),
    );
    setErrors(found);
    setProblem(undefined);
    setShowPatternIssues(true);
    if (Object.keys(found).length === 0 && issues.length === 0) save.mutate();
  };

  const siteOptions = (sites.data ?? []).map((site) => ({
    value: String(site.id),
    label: `${site.code} · ${site.name}`,
  }));
  // An archived site is no longer offered, but the center's own is still named.
  if (center && !siteOptions.some((option) => option.value === String(center.siteId)))
    siteOptions.unshift({ value: String(center.siteId), label: `${center.siteCode} · ${center.siteName}` });

  return (
    <Stack gap="lg">
      <PageSection title={t("workCenterDetails")}>
        <Stack gap="md" data-assist="manufacturing.work-center.form">
          <SimpleGrid cols={{ base: 1, sm: 2, md: 3 }}>
            <TextInput
              label={t("code")}
              description={t("codeHint")}
              withAsterisk
              maxLength={16}
              disabled={!manage}
              value={values.code}
              onChange={(event) => set("code", event.currentTarget.value)}
              error={errors.code}
            />
            <TextInput
              label={t("name")}
              withAsterisk
              maxLength={200}
              disabled={!manage}
              value={values.name}
              onChange={(event) => set("name", event.currentTarget.value)}
              error={errors.name}
            />
            <Select
              label={t("site")}
              withAsterisk
              allowDeselect={false}
              disabled={!manage}
              data={siteOptions}
              value={values.siteId === null ? null : String(values.siteId)}
              onChange={(next) => set("siteId", next === null ? null : Number(next))}
              error={errors.siteId}
            />
            <NumberInput
              label={t("capacity")}
              description={t("capacityHint")}
              min={1}
              allowDecimal={false}
              allowNegative={false}
              disabled={!manage}
              value={values.capacity}
              onChange={(next) => set("capacity", typeof next === "number" ? next : "")}
              error={errors.capacity}
            />
            <NumberInput
              label={t("timeEfficiency")}
              description={t("timeEfficiencyHint")}
              min={1}
              max={500}
              suffix=" %"
              allowDecimal={false}
              allowNegative={false}
              disabled={!manage}
              value={values.timeEfficiency}
              onChange={(next) => set("timeEfficiency", typeof next === "number" ? next : "")}
              error={errors.timeEfficiency}
            />
            <TextInput
              label={t("setupMinutes")}
              inputMode="decimal"
              disabled={!manage}
              value={values.setupMinutes}
              onChange={(event) => set("setupMinutes", event.currentTarget.value)}
              error={errors.setupMinutes}
            />
            <TextInput
              label={t("cleanupMinutes")}
              inputMode="decimal"
              disabled={!manage}
              value={values.cleanupMinutes}
              onChange={(event) => set("cleanupMinutes", event.currentTarget.value)}
              error={errors.cleanupMinutes}
            />
            {costs && (
              <TextInput
                label={t("machineCostPerHour")}
                description={t("costPerHourHint", { currency: meta.data?.currency ?? "NOK" })}
                inputMode="decimal"
                disabled={!manage}
                value={values.machineCostPerHour}
                onChange={(event) => set("machineCostPerHour", event.currentTarget.value)}
                error={errors.machineCostPerHour}
              />
            )}
            {costs && (
              <TextInput
                label={t("labourCostPerHour")}
                description={t("costPerHourHint", { currency: meta.data?.currency ?? "NOK" })}
                inputMode="decimal"
                disabled={!manage}
                value={values.labourCostPerHour}
                onChange={(event) => set("labourCostPerHour", event.currentTarget.value)}
                error={errors.labourCostPerHour}
              />
            )}
          </SimpleGrid>
        </Stack>
      </PageSection>
      <PageSection title={t("weeklyPattern")}>
        <Stack gap="xs">
          <Text size="sm" c="dimmed">
            {t("weeklyPatternHint")}
          </Text>
          <PatternEditor
            value={values.pattern}
            onChange={(pattern) => set("pattern", pattern)}
            issues={showPatternIssues ? issues : []}
            readOnly={!manage}
          />
        </Stack>
      </PageSection>
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
          {problem}
        </Alert>
      )}
      {manage && (
        <Group justify="flex-end">
          <Button variant="default" onClick={() => void navigate({ to: WORK_CENTERS_ROUTE_PATH })}>
            {t("cancel")}
          </Button>
          <Button data-assist="manufacturing.work-center.save" loading={save.isPending} onClick={submit}>
            {t(center ? "save" : "createWorkCenter")}
          </Button>
        </Group>
      )}
    </Stack>
  );
};

const breadcrumbs = (t: ReturnType<typeof useManufacturingFormat>["t"], label: string) => [
  { label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH },
  { label: t("navWorkCenters"), to: WORK_CENTERS_ROUTE_PATH },
  { label },
];

/** A new work center's form, at the default pattern. */
export const NewWorkCenterPage = () => {
  const { t } = useManufacturingFormat();
  const meta = useQuery(manufacturingMetaQueryOptions());
  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={breadcrumbs(t, t("newWorkCenter"))}
        title={t("newWorkCenter")}
        description={t("newWorkCenterDescription")}
      />
      {meta.data && !canManageBoms(meta.data) ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("notAllowed_manageBoms")}
        </Alert>
      ) : meta.isPending ? (
        <ContentSkeleton rows={3} />
      ) : (
        <WorkCenterForm />
      )}
    </Stack>
  );
};

/** One work center: its form, its closures, and archive, reactivate and delete for a manager. */
export const WorkCenterPage = () => {
  const { t } = useManufacturingFormat();
  const { workCenterId } = useParams({ strict: false }) as { workCenterId: string };
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const meta = useQuery(manufacturingMetaQueryOptions());
  const center = useQuery(workCenterQueryOptions(Number(workCenterId)));
  const { run, confirm } = useRecordActions();
  const manage = canManageBoms(meta.data);
  const data = center.data;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={breadcrumbs(t, data?.code ?? t("workCenter"))}
        title={
          data ? (
            <Group gap="sm">
              {`${data.code} · ${data.name}`}
              <Badge variant="light" color={statusColor[data.status]}>
                {t(`status_${data.status}`)}
              </Badge>
              {data.workingState === "blocked" && (
                <Badge variant="light" color="red">
                  {t("workingState_blocked")}
                </Badge>
              )}
            </Group>
          ) : (
            t("workCenter")
          )
        }
        description={data ? `${data.siteCode} · ${data.siteName}` : undefined}
        actions={
          data &&
          manage && (
            <Group gap="xs" data-assist="manufacturing.work-center.actions">
              {data.status === "active" ? (
                <Button
                  variant="default"
                  leftSection={<IconArchive size={16} />}
                  onClick={() =>
                    confirm(
                      t("archiveWorkCenterTitle", { code: data.code }),
                      t("archiveWorkCenterBody"),
                      t("archive"),
                      () =>
                        void run(
                          () => archiveWorkCenter(data.id, data.revision),
                          t("workCenterArchived"),
                          t("couldNotArchive"),
                        ),
                    )
                  }
                >
                  {t("archive")}
                </Button>
              ) : (
                <Button
                  variant="default"
                  leftSection={<IconRestore size={16} />}
                  onClick={() =>
                    void run(
                      () => reactivateWorkCenter(data.id, data.revision),
                      t("workCenterReactivated"),
                      t("couldNotReactivate"),
                    )
                  }
                >
                  {t("reactivate")}
                </Button>
              )}
              <Button
                variant="default"
                color="red"
                leftSection={<IconTrash size={16} />}
                onClick={() =>
                  confirm(
                    t("deleteWorkCenterTitle", { code: data.code }),
                    t("deleteWorkCenterBody"),
                    t("delete"),
                    () =>
                      void run(
                        () => deleteWorkCenter(data.id, data.revision),
                        t("workCenterDeleted"),
                        t("couldNotDelete"),
                        () => void navigate({ to: WORK_CENTERS_ROUTE_PATH, replace: true }),
                      ),
                  )
                }
              >
                {t("delete")}
              </Button>
            </Group>
          )
        }
      />
      {center.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {center.error instanceof NotFoundError ? t("workCenterNotFound") : center.error.message}
        </Alert>
      ) : !data || meta.isPending ? (
        <ContentSkeleton rows={4} />
      ) : (
        <>
          {/* A fresh form per revision: a save, an archive or someone else's change reloads what it shows. */}
          <WorkCenterForm key={data.revision} center={data} />
          <ClosuresPanel workCenterId={data.id} canManage={manage} />
        </>
      )}
    </Stack>
  );
};
