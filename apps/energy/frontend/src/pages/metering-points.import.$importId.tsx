import { Alert, Button, Card, Checkbox, Group, Modal, Stack, Stepper, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle, IconDownload } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  attachImport,
  downloadFailedRows,
  type EnergyImport,
  importQueryKey,
  importQueryOptions,
  importStep,
  runImport,
  SWITCH_NOT_CONFIRMED,
  saveFile,
  saveImportMapping,
} from "../api/imports";
import { ApiConflictError } from "../api/request";
import { type ImportCapabilities, ImportMappingStep } from "./-import-mapping-step";
import { ImportProgress } from "./-import-progress";
import { ImportRowsTable } from "./-import-rows-table";
import "../i18n";

export type { ImportCapabilities } from "./-import-mapping-step";

/** The figures a result shows, worded for a check or for a run; the link counts only when there are any. */
const Figures = ({ imp, done }: { imp: EnergyImport; done: boolean }) => {
  const { t, formatters } = useI18n("energy");
  const figures = [
    { label: t("importCountRows"), value: imp.rows },
    { label: t(done ? "importCountCreated" : "importCountWouldCreate"), value: imp.created },
    { label: t(done ? "importCountUpdated" : "importCountWouldUpdate"), value: imp.updated },
    { label: t(done ? "importCountSkipped" : "importCountWouldSkip"), value: imp.skipped },
    { label: t(done ? "importCountFailed" : "importCountHaveErrors"), value: imp.failed },
  ];
  if (imp.switched > 0) {
    figures.push({ label: t(done ? "importCountSwitched" : "importCountWouldSwitch"), value: imp.switched });
  }
  if (imp.pending > 0) {
    figures.push({ label: t(done ? "importCountPending" : "importCountWouldPend"), value: imp.pending });
  }
  if (done && imp.pendingSwitches > 0 && imp.pending > 0) {
    figures.push({ label: t("importCountPendingSwitches"), value: imp.pendingSwitches });
  }
  if (done && imp.attached > 0) figures.push({ label: t("importCountAttached"), value: imp.attached });
  if (done && imp.processed < imp.rows) {
    figures.push({ label: t("importCountNotAttempted"), value: imp.rows - imp.processed });
  }
  return (
    <Group gap="xl" role="status" aria-live="polite">
      {figures.map(({ label, value }) => (
        <div key={label}>
          <Text size="xs" c="dimmed">
            {label}
          </Text>
          <Text fw={600}>{formatters.formatNumber(value)}</Text>
        </div>
      ))}
    </Group>
  );
};

const DownloadFailedRows = ({ imp }: { imp: EnergyImport }) => {
  const { t } = useI18n("energy");
  const download = useMutation({
    mutationFn: () => downloadFailedRows(imp.id),
    onSuccess: saveFile,
    onError: (error: Error) =>
      notifications.show({ color: "red", title: t("importFailedRowsCouldNotDownload"), message: error.message }),
  });
  if (imp.failed === 0) return null;
  return (
    <Stack gap={4} align="flex-start">
      <Button
        variant="light"
        leftSection={<IconDownload size={16} />}
        loading={download.isPending}
        onClick={() => download.mutate()}
      >
        {t("importDownloadFailedRows")}
      </Button>
      <Text size="xs" c="dimmed">
        {t("importFailedRowsHint")}
      </Text>
    </Stack>
  );
};

/** The confirmation a switch needs (design D4): the box to tick, saying how many. */
const SwitchConfirmation = ({
  count,
  checked,
  onChange,
}: {
  count: number;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) => {
  const { t } = useI18n("energy");
  return (
    <Checkbox
      label={t("importSwitchConfirm", { count })}
      checked={checked}
      onChange={(event) => onChange(event.currentTarget.checked)}
    />
  );
};

/** The number of switches a 409 `import_switch_not_confirmed` says the server expects, if it says. */
const switchesOf = (error: unknown): number | undefined => {
  if (!(error instanceof ApiConflictError) || error.code !== SWITCH_NOT_CONFIRMED) return undefined;
  const switches = error.problem.switches;
  return typeof switches === "number" ? switches : 0;
};

/**
 * Step 2 once the check has finished: the switch warning above everything
 * else (design D4), the counts, the errors, and Import — whose confirmation
 * repeats the counts and, when points will switch customer, is only enabled
 * once the person ticks that they understand how many.
 */
const CheckResult = ({ imp }: { imp: EnergyImport }) => {
  const { t, formatters } = useI18n("energy");
  const queryClient = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const [understood, setUnderstood] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);
  const setImport = (result: EnergyImport) => queryClient.setQueryData(importQueryKey(imp.id), result);
  const run = useMutation({
    mutationFn: () => runImport(imp.id, imp.switched),
    onSuccess: (result) => {
      setConfirming(false);
      setImport(result);
    },
    onError: (error: Error) => {
      setConfirming(false);
      setUnderstood(false);
      if (switchesOf(error) !== undefined) {
        // The server counts the switches differently from the page: say so,
        // and read the import and its rows again.
        setRefusal(error.message);
        void queryClient.invalidateQueries({ queryKey: importQueryKey(imp.id) });
        return;
      }
      notifications.show({ color: "red", title: t("importCouldNotStart"), message: error.message });
    },
  });
  // Back to step 1 is the server's own move: saving the mapping as it is
  // returns the import to draft and clears the check.
  const backToMapping = useMutation({
    mutationFn: () => saveImportMapping(imp.id, imp.mapping ?? imp.suggested),
    onSuccess: setImport,
    onError: (error: Error) =>
      notifications.show({ color: "red", title: t("importMappingRefused"), message: error.message }),
  });
  const importable = imp.created + imp.updated > 0;
  const switches = imp.switched;
  return (
    <Stack gap="lg">
      {switches > 0 && (
        <Alert
          color="red"
          variant="filled"
          icon={<IconAlertTriangle size={20} />}
          title={t("importSwitchWarningTitle")}
        >
          <Text size="sm">{t("importSwitchWarning", { count: switches })}</Text>
        </Alert>
      )}
      {switches > 0 && (
        <Card withBorder padding="lg" radius="md">
          <ImportRowsTable imp={imp} kind="switched" />
        </Card>
      )}
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <div>
            <Text fw={600}>{t("importCheckedTitle")}</Text>
            <Text size="sm" c="dimmed">
              {importable ? t("importCheckIntro") : t("importNothingToImport")}
            </Text>
          </div>
          <Figures imp={imp} done={false} />
          {refusal && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importCouldNotStart")}>
              <Text size="sm">{refusal}</Text>
            </Alert>
          )}
          {imp.failed > 0 && <ImportRowsTable imp={imp} kind="failed" />}
          <DownloadFailedRows imp={imp} />
          {imp.skipped > 0 && <ImportRowsTable imp={imp} kind="skipped" />}
          <Group justify="flex-end">
            <Button variant="default" loading={backToMapping.isPending} onClick={() => backToMapping.mutate()}>
              {t("importChangeMapping")}
            </Button>
            <Button
              disabled={!importable}
              loading={run.isPending}
              onClick={() => {
                setRefusal(null);
                setUnderstood(false);
                setConfirming(true);
              }}
            >
              {t("importRun")}
            </Button>
          </Group>
        </Stack>
      </Card>
      <Modal opened={confirming} onClose={() => setConfirming(false)} title={t("importRunTitle")} centered>
        <Stack gap="md">
          <Text size="sm">
            {t("importRunConfirm", {
              created: formatters.formatNumber(imp.created),
              updated: formatters.formatNumber(imp.updated),
              failed: formatters.formatNumber(imp.failed),
            })}
          </Text>
          {(switches > 0 || imp.pending > 0) && (
            <Text size="sm">
              {t("importRunConfirmLinks", {
                switched: formatters.formatNumber(switches),
                pending: formatters.formatNumber(imp.pending),
              })}
            </Text>
          )}
          {switches > 0 && (
            <Alert color="red" icon={<IconAlertTriangle size={16} />}>
              <Stack gap="sm">
                <Text size="sm">{t("importSwitchWarning", { count: switches })}</Text>
                <SwitchConfirmation count={switches} checked={understood} onChange={setUnderstood} />
              </Stack>
            </Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setConfirming(false)}>
              {t("cancel")}
            </Button>
            <Button
              color={switches > 0 ? "red" : undefined}
              disabled={switches > 0 && !understood}
              loading={run.isPending}
              onClick={() => run.mutate()}
            >
              {t("importRun")}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  );
};

/**
 * The pending points attached afterwards (design D6): the count, the date
 * their periods start, the switch warning and its box when some of them are
 * another customer's now, and **Attach**. The pending switches the page knows
 * are those the run wrote; when the server counts otherwise now (a point was
 * switched by hand since), its 409 says how many, and the warning takes that
 * number and asks again.
 */
const AttachPanel = ({ imp, canAttach }: { imp: EnergyImport; canAttach: boolean }) => {
  const { t } = useI18n("energy");
  const queryClient = useQueryClient();
  const [start, setStart] = useState("");
  const [understood, setUnderstood] = useState(false);
  const [serverSwitches, setServerSwitches] = useState<number | null>(null);
  const [refusal, setRefusal] = useState<string | null>(null);
  const switches = serverSwitches ?? imp.pendingSwitches;
  const attach = useMutation({
    // The date input yields "YYYY-MM-DD"; the server wants a UTC timestamp, so UTC midnight of that day.
    mutationFn: () => attachImport(imp.id, { start: new Date(start).toISOString(), confirmSwitches: switches }),
    onSuccess: (result) => {
      setRefusal(null);
      queryClient.setQueryData(importQueryKey(imp.id), result);
    },
    onError: (error: Error) => {
      const expected = switchesOf(error);
      if (expected !== undefined) {
        setServerSwitches(expected);
        setUnderstood(false);
        void queryClient.invalidateQueries({ queryKey: importQueryKey(imp.id) });
      }
      setRefusal(error.message);
    },
  });
  const ready = canAttach && start !== "" && (switches === 0 || understood);
  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <div>
          <Text fw={600}>{t("importAttachTitle")}</Text>
          <Text size="sm" c="dimmed">
            {t("importAttachIntro", { count: imp.pending })}
          </Text>
        </div>
        {switches > 0 && (
          <Alert color="red" variant="filled" icon={<IconAlertTriangle size={20} />}>
            <Text size="sm">{t("importAttachSwitchWarning", { count: switches })}</Text>
          </Alert>
        )}
        <ImportRowsTable imp={imp} kind="pending" />
        {canAttach ? (
          <>
            <TextInput
              label={t("importAttachStart")}
              type="date"
              withAsterisk
              value={start}
              onChange={(event) => setStart(event.currentTarget.value)}
              maw={240}
            />
            {switches > 0 && <SwitchConfirmation count={switches} checked={understood} onChange={setUnderstood} />}
          </>
        ) : (
          <Text size="sm" c="dimmed">
            {t("importAttachNeedsPermission")}
          </Text>
        )}
        {refusal && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importCouldNotAttach")}>
            <Text size="sm">{refusal}</Text>
          </Alert>
        )}
        <Group justify="flex-end">
          <Button
            color={switches > 0 ? "red" : undefined}
            disabled={!ready}
            loading={attach.isPending}
            onClick={() => attach.mutate()}
          >
            {t("importAttach")}
          </Button>
        </Group>
      </Stack>
    </Card>
  );
};

/** Step 3 once the run (or an attach) has finished: what it did, and where to go next. */
const DoneResult = ({ imp, capabilities }: { imp: EnergyImport; capabilities: ImportCapabilities }) => {
  const { t, formatters } = useI18n("energy");
  const stopped = imp.processed < imp.rows;
  return (
    <Stack gap="lg">
      {imp.pending > 0 && <AttachPanel imp={imp} canAttach={capabilities.canLinkCustomers} />}
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <div>
            <Text fw={600}>{t(stopped ? "importStoppedTitle" : "importDoneTitle")}</Text>
            <Text size="sm" c="dimmed">
              {t(stopped ? "importStoppedHelp" : "importDoneHelp")}
            </Text>
            {imp.attachStart && imp.attached > 0 && (
              <Text size="sm" c="dimmed">
                {t("importAttachedFrom", { date: formatters.formatDate(imp.attachStart) })}
              </Text>
            )}
          </div>
          <Figures imp={imp} done />
          {imp.failed > 0 && <ImportRowsTable imp={imp} kind="failed" />}
          <DownloadFailedRows imp={imp} />
          {imp.switched > 0 && <ImportRowsTable imp={imp} kind="switched" />}
          {imp.created > 0 && <ImportRowsTable imp={imp} kind="created" />}
          {imp.updated > 0 && <ImportRowsTable imp={imp} kind="updated" />}
          {imp.skipped > 0 && <ImportRowsTable imp={imp} kind="skipped" />}
          <Group justify="flex-end">
            <Button component={Link} to="/energy/metering-points/import" variant="default">
              {t("importStartAnother")}
            </Button>
            <Button component={Link} to="/energy/metering-points">
              {t("importGoToMeteringPoints")}
            </Button>
          </Group>
        </Stack>
      </Card>
    </Stack>
  );
};

/**
 * One import (energy import design D9): the stepper across the top — Map
 * columns, Check, Import — whose active step is derived from the state, so a
 * refresh lands where the person was; attaching belongs to the last step. The
 * page polls while the worker works (importQueryOptions) and otherwise not at
 * all. The capabilities come from the host, which reads the caller's
 * permissions; this package never fetches them itself.
 */
export const ImportPage = ({ importId, capabilities }: { importId: number; capabilities: ImportCapabilities }) => {
  const { t, formatters } = useI18n("energy");
  const { data: imp, isPending, isError, error, refetch } = useQuery(importQueryOptions(importId));

  const breadcrumbs = [
    { label: t("meteringPoints"), to: "/energy/metering-points" },
    { label: t("importTitle"), to: "/energy/metering-points/import" },
  ];
  if (isPending) {
    return (
      <Stack gap="lg">
        <PageHeader breadcrumbs={breadcrumbs} title={t("importTitle")} />
        <ContentSkeleton rows={6} />
      </Stack>
    );
  }
  if (isError) {
    return (
      <Stack gap="lg">
        <PageHeader breadcrumbs={breadcrumbs} title={t("importTitle")} />
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importCouldNotLoad")}>
          <Stack align="flex-start" gap="xs">
            <Text size="sm">{error.message}</Text>
            <Button variant="light" size="compact-sm" onClick={() => refetch()}>
              {t("tryAgain")}
            </Button>
          </Stack>
        </Alert>
      </Stack>
    );
  }

  const step = importStep(imp.state);
  const facts = [
    t("importFactRows", { count: formatters.formatNumber(imp.rows) }),
    t(`importEncoding_${imp.encoding === "utf-8" ? "utf8" : "windows1252"}`),
    t(`importSeparator_${imp.separator === ";" ? "semicolon" : imp.separator === "," ? "comma" : "tab"}`),
    imp.createdByName
      ? t("importFactUploadedBy", { name: imp.createdByName, when: formatters.formatDate(imp.createdAt) })
      : t("importFactUploaded", { when: formatters.formatDate(imp.createdAt) }),
  ];
  return (
    <Stack gap="lg">
      <PageHeader breadcrumbs={breadcrumbs} title={imp.fileName} description={facts.join(" · ")} />
      <Stepper active={step} allowNextStepsSelect={false}>
        <Stepper.Step label={t("importStepMap")} description={t("importStepMapHelp")} />
        <Stepper.Step
          label={t("importStepCheck")}
          description={t("importStepCheckHelp")}
          loading={imp.state === "checking"}
        />
        <Stepper.Step
          label={t("importStepImport")}
          description={t("importStepImportHelp")}
          loading={imp.state === "importing" || imp.state === "attaching"}
        />
      </Stepper>
      {imp.state === "draft" && <ImportMappingStep key={imp.id} imp={imp} capabilities={capabilities} />}
      {(imp.state === "checking" || imp.state === "importing" || imp.state === "attaching") && (
        <ImportProgress imp={imp} />
      )}
      {imp.state === "checked" && <CheckResult imp={imp} />}
      {imp.state === "done" && <DoneResult imp={imp} capabilities={capabilities} />}
    </Stack>
  );
};
