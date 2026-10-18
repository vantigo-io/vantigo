import { Alert, Button, Card, Group, Progress, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { IconAlertCircle, IconClock } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type EnergyImport, importQueryKey, stopImport } from "../api/imports";
import "../i18n";

/** How long a queued import may wait before the page says the worker has not taken it up. */
const WAITING_FOR_WORKER_MS = 60_000;

/**
 * The bar while the worker checks, imports or attaches (energy import design
 * D6, D9): the rows done of the rows there are — for an attach, the pending
 * points attached of those there were — the counts so far, **Stop** (with a
 * confirmation on a real run, since what is committed stays), a whole-run
 * failure the worker will retry, and an import nobody has taken up in a
 * minute, which is a worker that is not running.
 */
export const ImportProgress = ({ imp, now = () => Date.now() }: { imp: EnergyImport; now?: () => number }) => {
  const { t, formatters } = useI18n("energy");
  const queryClient = useQueryClient();
  const importing = imp.state === "importing";
  const attaching = imp.state === "attaching";
  const stop = useMutation({
    mutationFn: () => stopImport(imp.id),
    onSuccess: (result) => queryClient.setQueryData(importQueryKey(imp.id), result),
  });
  const confirmStop = () => {
    if (!importing) {
      stop.mutate();
      return;
    }
    modals.openConfirmModal({
      title: t("importStopTitle"),
      children: <Text size="sm">{t("importStopWarning")}</Text>,
      labels: { confirm: t("importStopConfirm"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => stop.mutate(),
    });
  };
  const [done, total] = attaching ? [imp.attached, imp.attached + imp.pending] : [imp.processed, imp.rows];
  const percent = total === 0 ? 100 : Math.round((done / total) * 100);
  const waiting =
    imp.startedAt === null &&
    imp.requestedAt !== null &&
    now() - new Date(imp.requestedAt).getTime() > WAITING_FOR_WORKER_MS;
  const title = attaching ? "importAttachingTitle" : importing ? "importImportingTitle" : "importCheckingTitle";
  const help = attaching ? "importAttachingHelp" : importing ? "importImportingHelp" : "importCheckingHelp";

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <div>
          <Text fw={600}>{t(title)}</Text>
          <Text size="sm" c="dimmed">
            {t(help)}
          </Text>
        </div>
        <Progress value={percent} size="lg" animated={imp.startedAt !== null} aria-label={t("importProgress")} />
        <Group justify="space-between">
          <Text size="sm" role="status" aria-live="polite">
            {imp.startedAt === null
              ? t("importQueued")
              : attaching
                ? t("importAttachProgress", {
                    attached: formatters.formatNumber(imp.attached),
                    pending: formatters.formatNumber(imp.pending),
                  })
                : t("importProgressRows", {
                    done: formatters.formatNumber(imp.processed),
                    total: formatters.formatNumber(imp.rows),
                  })}
          </Text>
          {!attaching && (
            <Text size="sm" c="dimmed">
              {t("importProgressCounts", {
                created: formatters.formatNumber(imp.created),
                updated: formatters.formatNumber(imp.updated),
                skipped: formatters.formatNumber(imp.skipped),
                failed: formatters.formatNumber(imp.failed),
              })}
            </Text>
          )}
        </Group>
        {imp.stopRequested && <Text size="sm">{t("importStopping")}</Text>}
        {imp.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importRunFailedTitle")}>
            <Text size="sm">{t("importRunFailed", { error: imp.error })}</Text>
          </Alert>
        )}
        {waiting && !imp.error && (
          <Alert color="yellow" icon={<IconClock size={16} />} title={t("importWaitingTitle")}>
            <Text size="sm">{t("importWaiting")}</Text>
          </Alert>
        )}
        <Group justify="flex-end">
          <Button
            variant="default"
            color="red"
            disabled={imp.stopRequested}
            loading={stop.isPending}
            onClick={confirmStop}
          >
            {t("importStop")}
          </Button>
        </Group>
      </Stack>
    </Card>
  );
};
