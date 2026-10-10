import { Badge, Button, Card, Group, List, Stack, Table, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconDownload } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { InvoiceDocument } from "../api/invoices";
import {
  type Reminder,
  type ReminderStatus,
  reminderPdfUrl,
  retryReminder,
  WITH_PDF,
  WITHDRAWABLE,
  withdrawReminder,
} from "../api/reminders";
import { INVOICES_QUERY_KEY } from "../api/request";
import { invoicesCatalog } from "../i18n";
import { useWho } from "../lib/bank";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { letterName, useLetterLevel } from "../lib/reminders";
import { PdfButton } from "./pdf-button";
import { ReasonModal } from "./reason-modal";

type NextAction = NonNullable<InvoiceDocument["nextAction"]>;

const statusColour: Record<ReminderStatus, string> = {
  queued: "blue",
  awaiting_print: "blue",
  printed: "cyan",
  sent: "green",
  failed: "red",
  withdrawn: "gray",
};

/** A key in words when this catalog has it, the code itself otherwise — a newer server's code never throws. */
const useWords = () => {
  const { t } = useInvoiceFormat();
  return (key: string, code: string, values?: Record<string, unknown>) =>
    key in invoicesCatalog.en ? t(key, values) : code;
};

export interface WithdrawReminderModalProps {
  letter: Pick<Reminder, "id" | "sequence">;
  onClose: () => void;
}

/**
 * Withdraws a letter with the person's reason (D10): a queued, awaiting-print,
 * printed or failed one. A printed letter may be in the post already, so the
 * dialog says to pull it; one being sent right now is refused, in words.
 */
export const WithdrawReminderModal = ({ letter, onClose }: WithdrawReminderModalProps) => {
  const { t } = useInvoiceFormat();
  return (
    <ReasonModal
      title={t("reminder.withdrawTitle", { letter: letterName(t, letter.sequence) })}
      summary={letterName(t, letter.sequence)}
      hint={t("reminder.withdrawHint")}
      submitLabel={t("reminder.withdraw")}
      doneMessage={t("reminder.withdrawn")}
      errorTitle={t("reminder.couldNotWithdraw")}
      onSubmit={(reason) => withdrawReminder(letter.id, reason)}
      onClose={onClose}
    />
  );
};

/**
 * What the reminder engine says to do next with the invoice today (D8): a
 * reminder or a debt collection notice from a day, with the letter as it would
 * go today; a suggested hand-off to a collection agency; or why it waits or is
 * blocked, and why a letter claims less than it might — every reason in words.
 */
export const NextActionPanel = ({ next, currency }: { next: NextAction; currency: string }) => {
  const { t, money, date } = useInvoiceFormat();
  const words = useWords();
  const level = useLetterLevel();
  const letter = next.letter;
  return (
    <Stack gap={4} data-testid="next-action">
      <Text size="sm" fw={600}>
        {words(`reminder.next.${next.action}`, next.action)}
        {next.earliestOn && ` ${t("reminder.next.from", { date: date(next.earliestOn) })}`}
      </Text>
      {next.reasons.length > 0 && (
        <List size="sm" spacing={2}>
          {next.reasons.map((reason) => (
            <List.Item key={reason}>
              {words(`reminder.reason.${reason}`, reason, {
                kind: next.outdated ? words(`rates.kind.${next.outdated.kind}`, next.outdated.kind) : "",
                halfYear: next.outdated?.halfYear ?? "",
              })}
            </List.Item>
          ))}
        </List>
      )}
      {letter && (
        <Text size="sm" data-testid="next-letter">
          {t("reminder.next.letter", {
            level: level(letter),
            total: money(letter.total, currency),
            deadline: date(letter.deadline),
          })}
          {letter.fee > 0 && ` ${t("reminder.next.fee", { amount: money(letter.fee, currency) })}`}
          {letter.compensation > 0 &&
            ` ${t("reminder.next.compensation", { amount: money(letter.compensation, currency) })}`}
          {letter.interest > 0 && ` ${t("reminder.next.interest", { amount: money(letter.interest, currency) })}`}
        </Text>
      )}
      {next.chargeNotes.map((note) => (
        <Text key={note} size="xs" c="dimmed" data-charge-note={note}>
          {words(`reminder.chargeNote.${note}`, note)}
        </Text>
      ))}
    </Stack>
  );
};

export interface RemindersCardProps {
  invoice: InvoiceDocument;
  /** meta's `canRegisterPayments` — `invoices:payments`, which withdrawing and retrying a letter need. */
  canAct: boolean;
  /** The signed-in user, so a withdrawal of theirs says "you". */
  currentUserId?: string;
}

/**
 * An issued invoice's reminder letters (D10, D22) under what comes next: each
 * letter by its number, its kind, how it goes, its status — why a queued one
 * waits, why a failed one failed, why and by whom one was withdrawn — its date,
 * deadline and total, and its PDF once printed or sent. Withdraw and Retry are
 * offered with `invoices:payments`: withdraw on a letter not yet sent, retry on
 * a failed one.
 */
export const RemindersCard = ({ invoice, canAct, currentUserId }: RemindersCardProps) => {
  const { t, money, date } = useInvoiceFormat();
  const words = useWords();
  const level = useLetterLevel();
  const who = useWho(currentUserId);
  const queryClient = useQueryClient();
  const [withdrawing, setWithdrawing] = useState<Reminder | null>(null);
  const letters = invoice.reminders ?? [];
  const retry = useMutation({
    mutationFn: (letter: Reminder) => retryReminder(letter.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("reminder.retried") });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("reminder.couldNotRetry"), message: refusalMessage(error, t, date) }),
  });
  const statusLine = (letter: Reminder): string | undefined => {
    if (letter.status === "queued" && letter.heldReason)
      return words(`reminder.held.${letter.heldReason}`, letter.heldReason);
    if (letter.status === "failed")
      return t("reminder.failedAfter", { attempts: letter.attempts, error: letter.lastError ?? "" });
    if (letter.status === "withdrawn") {
      const reason = letter.withdrawnBy
        ? (letter.withdrawalReason ?? "")
        : words(`reminder.withdrawnReason.${letter.withdrawalReason}`, letter.withdrawalReason ?? "");
      return letter.withdrawnBy
        ? t("reminder.withdrawnBy", { who: who(letter.withdrawnBy), reason })
        : t("reminder.withdrawnByVantigo", { reason });
    }
    if (letter.status === "printed" && letter.printBatchId !== undefined) {
      return t("reminder.inBatch", { batch: letter.printBatchId });
    }
    return undefined;
  };
  return (
    <Card withBorder data-testid="reminders-card">
      <Stack gap="xs">
        <Title order={4}>{t("reminder.title")}</Title>
        {invoice.nextAction && <NextActionPanel next={invoice.nextAction} currency={invoice.currency} />}
        {letters.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("reminder.none")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={720}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("reminder.col.letter")}</Table.Th>
                  <Table.Th>{t("reminder.col.channel")}</Table.Th>
                  <Table.Th>{t("reminder.col.status")}</Table.Th>
                  <Table.Th>{t("reminder.col.sentOn")}</Table.Th>
                  <Table.Th>{t("reminder.col.deadline")}</Table.Th>
                  <Table.Th ta="right">{t("reminder.col.total")}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {letters.map((letter) => {
                  const line = statusLine(letter);
                  const name = letterName(t, letter.sequence);
                  return (
                    <Table.Tr key={letter.id} data-letter={letter.sequence}>
                      <Table.Td>
                        <Text size="sm" fw={600}>
                          {name}
                        </Text>
                        <Text size="xs">{level(letter)}</Text>
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
                          <Text size="xs" c="dimmed" data-testid={`letter-status-${letter.sequence}`}>
                            {line}
                          </Text>
                        )}
                      </Table.Td>
                      <Table.Td>{letter.sentOn ? date(letter.sentOn) : ""}</Table.Td>
                      <Table.Td>{letter.deadline ? date(letter.deadline) : ""}</Table.Td>
                      <Table.Td ta="right">
                        {letter.total !== undefined ? money(letter.total, invoice.currency) : ""}
                        {(letter.fee ?? 0) > 0 && (
                          <Text size="xs" c="dimmed">
                            {t("reminder.next.fee", { amount: money(letter.fee ?? 0, invoice.currency) })}
                          </Text>
                        )}
                        {(letter.compensation ?? 0) > 0 && (
                          <Text size="xs" c="dimmed">
                            {t("reminder.next.compensation", {
                              amount: money(letter.compensation ?? 0, invoice.currency),
                            })}
                          </Text>
                        )}
                        {(letter.interest ?? 0) > 0 && (
                          <Text size="xs" c="dimmed">
                            {t("reminder.next.interest", { amount: money(letter.interest ?? 0, invoice.currency) })}
                          </Text>
                        )}
                      </Table.Td>
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
                          {canAct && letter.status === "failed" && (
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
                          {canAct && WITHDRAWABLE.has(letter.status) && (
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
        )}
      </Stack>
      {withdrawing && <WithdrawReminderModal letter={withdrawing} onClose={() => setWithdrawing(null)} />}
    </Card>
  );
};
