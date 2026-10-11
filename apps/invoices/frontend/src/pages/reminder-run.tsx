import { Alert, Badge, Button, Card, Group, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconArrowLeft, IconDownload } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { invoicesMetaQueryOptions } from "../api/meta";
import { reminderRunQueryOptions } from "../api/overdue";
import {
  REMINDER_STATUSES,
  type Reminder,
  reminderPdfUrl,
  retryReminder,
  WITH_PDF,
  WITHDRAWABLE,
} from "../api/reminders";
import { INVOICES_QUERY_KEY } from "../api/request";
import { RouteLink } from "../components/document-link";
import { PdfButton } from "../components/pdf-button";
import { WithdrawReminderModal } from "../components/reminders-card";
import { RemindersGate } from "../components/reminders-gate";
import "../i18n";
import { useWho } from "../lib/bank";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { letterName, statusColour, useLetterLevel, useLetterStatusLine, useWords } from "../lib/reminders";
import { invoiceHref, invoiceLinkOptions, OVERDUE_ROUTE_PATH, REMINDER_PRINT_ROUTE_PATH } from "../lib/routes";

export interface ReminderRunPageProps {
  runId: number;
  /** The signed-in user's id, so a run or a withdrawal of theirs says "you". */
  currentUserId?: string;
}

/**
 * One reminder run (D10, plan reading 26): when and by whom, on what bank
 * data, what it made and skipped, and its letters with their current status
 * — counted by status, each with its invoice, how it goes, why it waits,
 * failed or was withdrawn, its PDF once printed or sent, and withdraw and
 * retry. The route is the nav's `/invoices` entry (`invoices:access`); the
 * page needs meta's `canRunReminders`, and says so without it.
 */
export const ReminderRunPage = ({ runId, currentUserId }: ReminderRunPageProps) => {
  const { t } = useInvoiceFormat();
  return (
    <Stack gap="lg">
      <RouteLink href={OVERDUE_ROUTE_PATH} to={{ to: OVERDUE_ROUTE_PATH }}>
        <Group gap={4} component="span">
          <IconArrowLeft size={14} />
          {t("overdue.backToOverdue")}
        </Group>
      </RouteLink>
      <PageHeader title={t("runPage.title", { id: runId })} />
      <RemindersGate>
        <RunDetail runId={runId} currentUserId={currentUserId} />
      </RemindersGate>
    </Stack>
  );
};

const RunDetail = ({ runId, currentUserId }: ReminderRunPageProps) => {
  const { t, money, date } = useInvoiceFormat();
  const words = useWords();
  const level = useLetterLevel();
  const who = useWho(currentUserId);
  const statusLine = useLetterStatusLine(currentUserId);
  const queryClient = useQueryClient();
  const meta = useQuery(invoicesMetaQueryOptions());
  const currency = meta.data?.currency ?? "NOK";
  const detail = useQuery(reminderRunQueryOptions(runId));
  const [withdrawing, setWithdrawing] = useState<Reminder | null>(null);
  const retry = useMutation({
    mutationFn: (letter: Reminder) => retryReminder(letter.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("reminder.retried") });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("reminder.couldNotRetry"), message: refusalMessage(error, t, date) }),
  });
  if (detail.isPending) return <ContentSkeleton rows={4} rowHeight={40} />;
  if (detail.isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("runPage.failedToLoad")}>
        {refusalMessage(detail.error, t, date)}
      </Alert>
    );
  const { run, letters } = detail.data;
  const counts = REMINDER_STATUSES.map(
    (status) => [status, letters.filter((l) => l.status === status).length] as const,
  ).filter(([, count]) => count > 0);
  return (
    <Stack gap="lg">
      <Card withBorder>
        <Stack gap={4}>
          <Text size="sm">{t("runPage.made", { date: date(run.runOn), who: who(run.createdBy) })}</Text>
          <Text size="sm">
            {run.letters !== undefined && run.skipped !== undefined
              ? t("runPage.counts", { count: run.letters, skipped: run.skipped })
              : t("runPage.unfinished")}
          </Text>
          <Text size="sm">
            {run.lastBookedOn ? t("overdue.fresh.current", { date: date(run.lastBookedOn) }) : t("runPage.noBankData")}
          </Text>
          {run.staleImportAcknowledged && <Text size="sm">{t("runPage.staleAcknowledged")}</Text>}
          <Group gap="xs" data-testid="run-counts">
            {counts.map(([status, count]) => (
              <Badge key={status} variant="light" color={statusColour[status]}>
                {t("runPage.statusCount", { status: words(`reminder.status.${status}`, status), count })}
              </Badge>
            ))}
          </Group>
          {letters.some((l) => l.status === "awaiting_print") && (
            <RouteLink href={REMINDER_PRINT_ROUTE_PATH} to={{ to: REMINDER_PRINT_ROUTE_PATH }}>
              {t("runPage.printLink")}
            </RouteLink>
          )}
        </Stack>
      </Card>
      <Table.ScrollContainer minWidth={800}>
        <Table data-testid="run-letters">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("reminder.col.letter")}</Table.Th>
              <Table.Th>{t("reminder.col.channel")}</Table.Th>
              <Table.Th>{t("reminder.col.status")}</Table.Th>
              <Table.Th>{t("reminder.col.sentOn")}</Table.Th>
              <Table.Th ta="right">{t("reminder.col.total")}</Table.Th>
              <Table.Th />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {letters.map((letter) => {
              const name = letterName(t, letter.sequence);
              const line = statusLine(letter);
              return (
                <Table.Tr key={letter.id} data-testid={`run-letter-${letter.id}`}>
                  <Table.Td>
                    <Text size="sm" fw={600}>
                      {name}
                    </Text>
                    <Text size="xs">{level(letter)}</Text>
                    <RouteLink
                      href={invoiceHref(letter.invoiceId)}
                      to={invoiceLinkOptions(letter.invoiceId)}
                      aria-label={t("runPage.openInvoiceOf", { letter: name })}
                    >
                      {t("runPage.openInvoice")}
                    </RouteLink>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{t(`reminder.channel.${letter.channel}`)}</Text>
                    {letter.recipient && (
                      <Text size="xs" c="dimmed">
                        {letter.recipient}
                      </Text>
                    )}
                  </Table.Td>
                  <Table.Td>
                    <Badge variant="light" color={statusColour[letter.status]}>
                      {words(`reminder.status.${letter.status}`, letter.status)}
                    </Badge>
                    {line && (
                      <Text size="xs" c="dimmed">
                        {line}
                      </Text>
                    )}
                  </Table.Td>
                  <Table.Td>{letter.sentOn ? date(letter.sentOn) : ""}</Table.Td>
                  <Table.Td ta="right">{letter.total !== undefined ? money(letter.total, currency) : ""}</Table.Td>
                  <Table.Td>
                    <Group gap={4} justify="flex-end" wrap="nowrap">
                      {WITH_PDF.has(letter.status) && (
                        <PdfButton
                          url={reminderPdfUrl(letter.id)}
                          mode="download"
                          size="xs"
                          variant="subtle"
                          aria-label={t("reminder.pdfOf", { letter: name })}
                          leftSection={<IconDownload size={14} />}
                        >
                          {t("reminder.pdf")}
                        </PdfButton>
                      )}
                      {letter.status === "failed" && (
                        <Button
                          size="xs"
                          variant="subtle"
                          aria-label={t("reminder.retryOf", { letter: name })}
                          loading={retry.isPending && retry.variables?.id === letter.id}
                          disabled={retry.isPending}
                          onClick={() => retry.mutate(letter)}
                        >
                          {t("reminder.retry")}
                        </Button>
                      )}
                      {WITHDRAWABLE.has(letter.status) && (
                        <Button
                          size="xs"
                          variant="subtle"
                          color="red"
                          aria-label={t("reminder.withdrawOf", { letter: name })}
                          onClick={() => setWithdrawing(letter)}
                        >
                          {t("reminder.withdraw")}
                        </Button>
                      )}
                    </Group>
                  </Table.Td>
                </Table.Tr>
              );
            })}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {withdrawing && <WithdrawReminderModal letter={withdrawing} onClose={() => setWithdrawing(null)} />}
    </Stack>
  );
};
