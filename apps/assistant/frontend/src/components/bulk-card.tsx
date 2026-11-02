import { Alert, Anchor, Badge, Button, Card, Group, Loader, Progress, Stack, Table, Text } from "@mantine/core";
import { IconAlertTriangle, IconCheck, IconClockOff, IconDownload, IconX } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect, useRef } from "react";
import { bulkFailuresUrl, bulkJobQueryOptions, bulkRunningStates } from "../api/assistant";
import type { AssistantBulkItem, AssistantBulkJob, AssistantBulkPlan, AssistantPendingAction } from "../api/types";
import "../i18n";

export interface BulkCardProps {
  card: AssistantPendingAction;
  /** A decision is being made: the buttons wait. */
  busy?: boolean;
  /** The card has just arrived: focus lands on the card, never on Start. */
  autoFocus?: boolean;
  /** A decision the server refused, in words. */
  error?: string;
  onStart: (card: AssistantPendingAction) => void;
  onCancel: (card: AssistantPendingAction) => void;
  onContinue: (card: AssistantPendingAction) => void;
  onStop: (card: AssistantPendingAction) => void;
  onNavigate?: (route: string) => void;
}

const when = (iso: string) => {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
};

const whyKey: Record<string, string> = {
  stopped_by_person: "bulkWhyStopped_by_person",
  narrowed: "bulkWhyNarrowed",
  session_ended: "bulkWhySession_ended",
  cancelled: "bulkWhyCancelled",
  internal: "bulkWhyInternal",
};

const excludedKey: Record<string, string> = {
  missing_id: "bulkExcludedMissing_id",
  duplicate: "bulkExcludedDuplicate",
};

const itemKey: Record<string, string> = {
  succeeded: "bulkItemChanged",
  failed: "bulkItemFailed",
  skipped: "bulkItemSkipped",
  pending: "bulkItemPending",
};

/** The job as the card shows it: the polled answer, unless the plan it came with is further on. */
const fresher = (
  plan: AssistantBulkPlan,
  job: AssistantBulkJob | undefined,
): AssistantBulkPlan & { failures?: AssistantBulkItem[] } => (job && job.processed >= plan.processed ? job : plan);

/**
 * A bulk change (design S13): the same change to many items. It is one card in
 * five parts, by where the job stands:
 *
 * - the preview — how many were found, a sample with each item's current and new
 *   value, how many will be skipped, Start and Cancel; nothing has been changed;
 * - the canary — the first five items, run for real, each with how it went, and
 *   Continue or Stop;
 * - the progress — a bar, the counters and Stop, polled every two seconds;
 * - a pause — when the assistant's permission, which belongs to the person's
 *   session, was about to run out: Continue issues a fresh one;
 * - the end — what happened, the items that failed with each refusal, and the
 *   list as a CSV.
 *
 * It is a preview of the change, not a rehearsal of it: Start changes the first
 * items for real, and the card says so. Enter does not start it by accident: the
 * card takes the focus when it arrives, not its Start button.
 */
export const BulkCard = ({
  card,
  busy = false,
  autoFocus = false,
  error,
  onStart,
  onCancel,
  onContinue,
  onStop,
  onNavigate,
}: BulkCardProps) => {
  const { t } = useI18n("assistant");
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (autoFocus && card.status === "pending") ref.current?.focus();
  }, [autoFocus, card.status]);

  const plan = card.bulk;
  const started = plan !== undefined && plan.state !== "previewed";
  // The job is polled while it runs, and read once more when it ends for its failed list.
  const polled = useQuery({
    ...bulkJobQueryOptions(plan?.jobId ?? "", plan !== undefined && bulkRunningStates.includes(plan.state)),
    enabled: started && plan !== undefined,
  });
  if (!plan) return null;
  const view = fresher(plan, polled.data);
  const failures = polled.data?.failures ?? [];

  const state = view.state;
  const running = bulkRunningStates.includes(state);
  const ended = state === "done" || state === "stopped" || state === "failed" || state === "expired";
  const cancelled = card.status === "cancelled";
  const expired = card.status === "expired" || state === "expired";
  const previewing = card.status === "pending";
  const paused = state === "awaiting_continue";

  const tone = ended
    ? state === "done"
      ? "teal"
      : state === "failed"
        ? "red"
        : "gray"
    : cancelled || expired
      ? "gray"
      : "blue";
  const why = view.errorCode ? whyKey[view.errorCode] : undefined;

  return (
    <Card
      ref={ref}
      withBorder
      padding="sm"
      radius="md"
      tabIndex={-1}
      role="group"
      aria-label={t("bulkLabel")}
      data-testid="bulk-card"
      data-status={card.status}
      data-state={state}
      style={{ outline: "none" }}
    >
      <Stack gap="xs">
        <Group justify="space-between" wrap="nowrap" align="flex-start" gap="xs">
          <Stack gap={2} flex={1}>
            <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
              {t("bulkLabel")}
            </Text>
            <Text size="sm" fw={600}>
              {card.explain || card.summary}
            </Text>
            {card.explain && card.explain !== card.summary && (
              <Text size="xs" c="dimmed">
                {card.summary}
              </Text>
            )}
          </Stack>
          {(ended || cancelled || expired) && (
            <Badge
              color={tone}
              variant="light"
              leftSection={
                state === "done" ? (
                  <IconCheck size={12} />
                ) : expired ? (
                  <IconClockOff size={12} />
                ) : state === "failed" ? (
                  <IconAlertTriangle size={12} />
                ) : (
                  <IconX size={12} />
                )
              }
            >
              {state === "done"
                ? t("outcomeChanged")
                : expired
                  ? t("outcomeExpired")
                  : state === "failed"
                    ? t("outcomeFailed")
                    : cancelled
                      ? t("outcomeCancelled")
                      : t("bulkItemSkipped")}
            </Badge>
          )}
          {running && (
            <Group gap={4} c="dimmed" wrap="nowrap" role="status">
              <Loader size={12} />
            </Group>
          )}
        </Group>

        {previewing && (
          <>
            <Text size="sm" data-testid="bulk-found">
              {t("bulkFound", { n: view.total })}
            </Text>
            {view.sample.length > 0 && (
              <Stack gap={4}>
                <Text size="xs" c="dimmed">
                  {t("bulkSampleTitle", { n: view.sample.length })}
                </Text>
                <Table withTableBorder fz="xs" verticalSpacing={4} data-testid="bulk-sample">
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("bulkItemColumn")}</Table.Th>
                      <Table.Th>{t("cardFieldColumn")}</Table.Th>
                      <Table.Th>{t("cardNowColumn")}</Table.Th>
                      <Table.Th>{t("cardNewColumn")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {view.sample.flatMap((row, index) =>
                      row.fields.map((field, fieldIndex) => (
                        <Table.Tr key={`${index}-${field.name}`}>
                          <Table.Td>
                            {fieldIndex === 0 &&
                              (row.route ? (
                                <Anchor
                                  href={row.route}
                                  size="xs"
                                  onClick={(event) => {
                                    if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
                                    event.preventDefault();
                                    onNavigate?.(row.route ?? "");
                                  }}
                                >
                                  {row.label || row.route}
                                </Anchor>
                              ) : (
                                row.label
                              ))}
                          </Table.Td>
                          <Table.Td>{field.name}</Table.Td>
                          <Table.Td c="dimmed">{field.current === undefined ? "—" : field.current || "—"}</Table.Td>
                          <Table.Td fw={500}>{field.next || "—"}</Table.Td>
                        </Table.Tr>
                      )),
                    )}
                  </Table.Tbody>
                </Table>
              </Stack>
            )}
            {view.excluded.count > 0 && (
              <Stack gap={0} data-testid="bulk-excluded">
                <Text size="xs" fw={600}>
                  {t("bulkExcluded", { n: view.excluded.count })}
                </Text>
                {view.excluded.reasons.map((reason) => (
                  <Text size="xs" c="dimmed" key={reason.code}>
                    {excludedKey[reason.code] ? t(excludedKey[reason.code], { n: reason.count }) : reason.code}
                  </Text>
                ))}
              </Stack>
            )}
            <Text size="xs" c="dimmed">
              {t("bulkRowNote")}
            </Text>
            <Text size="xs" c="dimmed">
              {t("bulkStartNote")}
            </Text>
            <Text size="xs" c="dimmed">
              {t("cardExpires", { time: when(card.expiresAt) })}
            </Text>
            <Group gap="xs">
              <Button size="xs" loading={busy} onClick={() => onStart(card)}>
                {t("bulkStart")}
              </Button>
              <Button size="xs" variant="default" disabled={busy} onClick={() => onCancel(card)}>
                {t("cancel")}
              </Button>
            </Group>
          </>
        )}

        {started && (
          <Stack gap={6}>
            <Progress
              value={view.total > 0 ? (view.processed / view.total) * 100 : 0}
              color={tone}
              size="sm"
              aria-label={t("bulkProgressLabel")}
              data-testid="bulk-progress"
            />
            <Group gap="md" wrap="wrap">
              <Text size="xs" fw={600}>
                {t("bulkProgress", { done: view.processed, total: view.total })}
              </Text>
              <Text size="xs" c="dimmed">
                {t("bulkSucceeded", { n: view.succeeded })}
              </Text>
              <Text size="xs" c={view.failed > 0 ? "red" : "dimmed"}>
                {t("bulkFailedCount", { n: view.failed })}
              </Text>
              <Text size="xs" c="dimmed">
                {t("bulkSkippedCount", { n: view.skipped })}
              </Text>
            </Group>
          </Stack>
        )}

        {(state === "canary" || (paused && view.canary.length > 0 && view.pauseReason !== "grant_expiring")) &&
          view.canary.length > 0 && (
            <Table withTableBorder fz="xs" verticalSpacing={4} data-testid="bulk-canary">
              <Table.Tbody>
                {view.canary.map((one) => (
                  <Table.Tr key={one.seq}>
                    <Table.Td>{one.label || `#${one.seq + 1}`}</Table.Td>
                    <Table.Td>
                      <Badge size="xs" variant="light" color={one.status === "failed" ? "red" : "teal"}>
                        {t(itemKey[one.status] ?? "bulkItemPending")}
                      </Badge>
                    </Table.Td>
                    <Table.Td c="dimmed">{one.status === "failed" ? one.title : undefined}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          )}

        {state === "canary" && view.canary.length === 0 && (
          <Group gap={6} c="dimmed" role="status">
            <Loader size={12} />
            <Text size="xs" c="dimmed">
              {t("making")}
            </Text>
          </Group>
        )}

        {paused && (
          <>
            <Text size="sm" fw={600}>
              {view.pauseReason === "grant_expiring"
                ? t("bulkPausedExpiring")
                : t("bulkCanaryTitle", { n: view.canary.length })}
            </Text>
            {view.pauseReason !== "grant_expiring" && (
              <Text size="xs" c="dimmed">
                {t("bulkCanaryHint", { n: view.canary.length, rest: Math.max(0, view.total - view.processed) })}
              </Text>
            )}
            <Group gap="xs">
              <Button size="xs" loading={busy} onClick={() => onContinue(card)}>
                {t("bulkContinue")}
              </Button>
              <Button size="xs" variant="default" disabled={busy} onClick={() => onStop(card)}>
                {t("bulkStop")}
              </Button>
            </Group>
          </>
        )}

        {(state === "running" || state === "stopping") && (
          <>
            <Text size="xs" c="dimmed">
              {state === "stopping" ? t("bulkStopping") : t("bulkRunning")}
            </Text>
            <Group gap="xs">
              <Button size="xs" variant="default" disabled={busy || state === "stopping"} onClick={() => onStop(card)}>
                {t("bulkStop")}
              </Button>
            </Group>
          </>
        )}

        {ended && (
          <Stack gap="xs">
            <Text size="sm" fw={600} data-testid="bulk-summary">
              {state === "done"
                ? t("bulkDone", { succeeded: view.succeeded, failed: view.failed, skipped: view.skipped })
                : state === "stopped"
                  ? t("bulkStoppedAt", {
                      done: view.processed,
                      total: view.total,
                      succeeded: view.succeeded,
                      failed: view.failed,
                    })
                  : state === "expired"
                    ? t("bulkWhyExpired")
                    : t("outcomeFailed")}
            </Text>
            {why && state !== "done" && (
              <Text size="xs" c="dimmed">
                {t(why)}
              </Text>
            )}
            {view.failed > 0 && (
              <Stack gap={4} data-testid="bulk-failures">
                <Text size="xs" fw={600}>
                  {t("bulkFailedTitle")}
                </Text>
                {failures.length > 0 && (
                  <Table withTableBorder fz="xs" verticalSpacing={4}>
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>{t("bulkItemColumn")}</Table.Th>
                        <Table.Th>{t("bulkStatusColumn")}</Table.Th>
                        <Table.Th>{t("bulkReasonColumn")}</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {failures.map((one) => (
                        <Table.Tr key={one.seq}>
                          <Table.Td>{one.label || `#${one.seq + 1}`}</Table.Td>
                          <Table.Td>{one.httpStatus ?? "—"}</Table.Td>
                          <Table.Td>{one.title || one.code || "—"}</Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                )}
                {view.failed > failures.length && failures.length > 0 && (
                  <Text size="xs" c="dimmed">
                    {t("bulkFailedShown", { shown: failures.length, n: view.failed })}
                  </Text>
                )}
                <Anchor
                  href={bulkFailuresUrl(view.jobId)}
                  download
                  size="xs"
                  data-testid="bulk-csv"
                  style={{ display: "inline-flex", alignItems: "center", gap: 4 }}
                >
                  <IconDownload size={14} />
                  {t("bulkDownload")}
                </Anchor>
              </Stack>
            )}
          </Stack>
        )}

        {(cancelled || expired) && !ended && why === undefined && (
          <Text size="xs" c="dimmed">
            {cancelled ? t("bulkWhyCancelled") : t("reasonExpired")}
          </Text>
        )}
        {error && (
          <Alert color="red" p="xs" role="alert">
            {error}
          </Alert>
        )}
      </Stack>
    </Card>
  );
};
