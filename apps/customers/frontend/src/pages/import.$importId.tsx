import { Alert, Button, Card, Group, Stack, Stepper, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDownload } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { isSessionExpired, saveCsv } from "../api/import-export";
import {
  type CustomerImport,
  downloadFailedRows,
  importQueryKey,
  importQueryOptions,
  importStep,
  runImport,
  saveImportMapping,
} from "../api/imports";
import { type ImportCapabilities, ImportMappingStep } from "./-import-mapping-step";
import { ImportProgress } from "./-import-progress";
import { ImportRowsTable } from "./-import-rows-table";
import "../i18n";

export type { ImportCapabilities } from "./-import-mapping-step";

/** The four figures a result shows, worded for a check or for a run. */
const Figures = ({ imp, done }: { imp: CustomerImport; done: boolean }) => {
  const { t, formatters } = useI18n("customers");
  const figures = [
    { label: t("importCountRows"), value: imp.rows },
    { label: t(done ? "importCountCreated" : "importCountWouldCreate"), value: imp.created },
    { label: t(done ? "importCountUpdated" : "importCountWouldUpdate"), value: imp.updated },
    { label: t(done ? "importCountFailed" : "importCountHaveErrors"), value: imp.failed },
  ];
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

const DownloadFailedRows = ({ imp }: { imp: CustomerImport }) => {
  const { t } = useI18n("customers");
  const download = useMutation({
    mutationFn: () => downloadFailedRows(imp.id),
    onSuccess: saveCsv,
    onError: (error: Error) => {
      if (!isSessionExpired(error)) {
        notifications.show({ color: "red", title: t("importFailedRowsCouldNotDownload"), message: error.message });
      }
    },
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

/** Step 2 once the check has finished: the counts, the errors, and Import. */
const CheckResult = ({ imp }: { imp: CustomerImport }) => {
  const { t, formatters } = useI18n("customers");
  const queryClient = useQueryClient();
  const setImport = (result: CustomerImport) => queryClient.setQueryData(importQueryKey(imp.id), result);
  const run = useMutation({
    mutationFn: () => runImport(imp.id),
    onSuccess: setImport,
    onError: (error: Error) =>
      notifications.show({ color: "red", title: t("importCouldNotStart"), message: error.message }),
  });
  // Back to step 1 is the server's own move: saving the mapping as it is
  // returns the import to draft and clears the check (design D5).
  const backToMapping = useMutation({
    mutationFn: () => saveImportMapping(imp.id, imp.mapping ?? imp.suggested),
    onSuccess: setImport,
    onError: (error: Error) =>
      notifications.show({ color: "red", title: t("importMappingRefused"), message: error.message }),
  });
  const importable = imp.created + imp.updated > 0;
  const confirmRun = () =>
    modals.openConfirmModal({
      title: t("importRunTitle"),
      children: (
        <Text size="sm">
          {t("importRunConfirm", {
            created: formatters.formatNumber(imp.created),
            updated: formatters.formatNumber(imp.updated),
            failed: formatters.formatNumber(imp.failed),
          })}
        </Text>
      ),
      labels: { confirm: t("importRun"), cancel: t("cancel") },
      onConfirm: () => run.mutate(),
    });
  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <div>
          <Text fw={600}>{t("importCheckedTitle")}</Text>
          <Text size="sm" c="dimmed">
            {importable ? t("importCheckIntro") : t("importNothingToImport")}
          </Text>
        </div>
        <Figures imp={imp} done={false} />
        {imp.failed > 0 && <ImportRowsTable imp={imp} outcome="failed" />}
        <DownloadFailedRows imp={imp} />
        <Group justify="flex-end">
          <Button variant="default" loading={backToMapping.isPending} onClick={() => backToMapping.mutate()}>
            {t("importChangeMapping")}
          </Button>
          <Button disabled={!importable} loading={run.isPending} onClick={confirmRun}>
            {t("importRun")}
          </Button>
        </Group>
      </Stack>
    </Card>
  );
};

/** Step 3 once the run has finished: what it did, and where to go next. */
const DoneResult = ({ imp }: { imp: CustomerImport }) => {
  const { t } = useI18n("customers");
  const stopped = imp.processed < imp.rows;
  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <div>
          <Text fw={600}>{t(stopped ? "importStoppedTitle" : "importDoneTitle")}</Text>
          <Text size="sm" c="dimmed">
            {t(stopped ? "importStoppedHelp" : "importDoneHelp")}
          </Text>
        </div>
        <Figures imp={imp} done />
        {imp.failed > 0 && <ImportRowsTable imp={imp} outcome="failed" />}
        <DownloadFailedRows imp={imp} />
        {imp.created > 0 && <ImportRowsTable imp={imp} outcome="created" />}
        {imp.updated > 0 && <ImportRowsTable imp={imp} outcome="updated" />}
        <Group justify="flex-end">
          <Button component={Link} to="/customers/import" variant="default">
            {t("importStartAnother")}
          </Button>
          <Button component={Link} to="/customers">
            {t("importGoToCustomers")}
          </Button>
        </Group>
      </Stack>
    </Card>
  );
};

/**
 * One import (customers import wizard design D8): the stepper across the top —
 * Map columns, Check, Import — whose active step is derived from the state,
 * so a refresh lands where the person was and a link to a running import
 * shows its bar. The page polls while the worker works (importQueryOptions)
 * and otherwise not at all. The capabilities come from the host, which reads
 * the caller's permissions; this package never fetches them itself.
 */
export const ImportPage = ({ importId, capabilities }: { importId: number; capabilities: ImportCapabilities }) => {
  const { t, formatters } = useI18n("customers");
  const { data: imp, isPending, isError, error, refetch } = useQuery(importQueryOptions(importId));

  const breadcrumbs = [
    { label: t("customers"), to: "/customers" },
    { label: t("importTitle"), to: "/customers/import" },
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
          loading={imp.state === "importing"}
        />
      </Stepper>
      {imp.state === "draft" && <ImportMappingStep key={imp.id} imp={imp} capabilities={capabilities} />}
      {(imp.state === "checking" || imp.state === "importing") && <ImportProgress imp={imp} />}
      {imp.state === "checked" && <CheckResult imp={imp} />}
      {imp.state === "done" && <DoneResult imp={imp} />}
    </Stack>
  );
};
