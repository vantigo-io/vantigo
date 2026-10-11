import {
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  List,
  Modal,
  Pagination,
  Radio,
  Select,
  Stack,
  Table,
  Text,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconArrowLeft, IconDownload } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import dayjs from "dayjs";
import { useState } from "react";
import { invoicesMetaQueryOptions } from "../api/meta";
import {
  confirmPosted,
  createPrintBatch,
  MAX_POST_ON_DAYS,
  MAX_PRINT_LETTERS,
  type PrintBatch,
  type PrintBatchLeftOut,
  type PrintBatchPosted,
  type PrintBatchResult,
  printBatchesQueryOptions,
  printBatchPdfUrl,
  printBatchState,
  reprint,
} from "../api/overdue";
import { type Reminder, remindersQueryOptions } from "../api/reminders";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import { RouteLink } from "../components/document-link";
import { PdfButton } from "../components/pdf-button";
import { RemindersGate } from "../components/reminders-gate";
import "../i18n";
import { useWho } from "../lib/bank";
import { fieldRefusals, refusalCode, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { letterName, useLetterLevel, useWords } from "../lib/reminders";
import {
  invoiceHref,
  invoiceLinkOptions,
  OVERDUE_ROUTE_PATH,
  reminderRunHref,
  reminderRunLinkOptions,
} from "../lib/routes";

export interface ReminderPrintPageProps {
  /** The signed-in user's id, so a batch of theirs says "you". */
  currentUserId?: string;
}

/** A day `n` days after `day`, both as YYYY-MM-DD. */
const addDays = (day: string, n: number): string => dayjs(day).add(n, "day").format("YYYY-MM-DD");

/** The calendar day in Oslo an instant fell on, as YYYY-MM-DD. */
const osloDay = (instant: string): string =>
  new Intl.DateTimeFormat("en-CA", {
    timeZone: "Europe/Oslo",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date(instant));

/**
 * Paper letters (D10, D22): a paper letter goes when it is posted. Choose the
 * letters awaiting print and the day they will be posted, print them —
 * each judged for that day, the ones that cannot go left out and named —
 * and download the batch; once it is in the post, confirm it posted on its
 * day, or reprint it for another. The route is the nav's `/invoices` entry
 * (`invoices:access`); the page needs meta's `canRunReminders`.
 */
export const ReminderPrintPage = ({ currentUserId }: ReminderPrintPageProps) => {
  const { t } = useInvoiceFormat();
  return (
    <Stack gap="lg">
      <RouteLink href={OVERDUE_ROUTE_PATH} to={{ to: OVERDUE_ROUTE_PATH }}>
        <Group gap={4} component="span">
          <IconArrowLeft size={14} />
          {t("overdue.backToOverdue")}
        </Group>
      </RouteLink>
      <PageHeader title={t("print.title")} description={t("print.description")} />
      <RemindersGate>
        <AwaitingCard />
        <BatchesCard currentUserId={currentUserId} />
      </RemindersGate>
    </Stack>
  );
};

/** The words for why a letter was left out of a batch, with the rate and half-year it lacks. */
const useLeftOutWords = () => {
  const words = useWords();
  return (left: PrintBatchLeftOut) =>
    words(`print.leftOut.${left.reason}`, left.reason, {
      kind: left.outdated ? words(`rates.inSentence.${left.outdated.kind}`, left.outdated.kind) : "",
      halfYear: left.outdated?.halfYear ?? "",
    });
};

/** The letters awaiting print, chosen for a batch and printed for a posting day. */
const AwaitingCard = () => {
  const { t, date, dateTime } = useInvoiceFormat();
  const level = useLetterLevel();
  const leftOutWords = useLeftOutWords();
  const queryClient = useQueryClient();
  const meta = useQuery(invoicesMetaQueryOptions());
  const today = meta.data?.today ?? dayjs().format("YYYY-MM-DD");
  const [page, setPage] = useState(1);
  const letters = useQuery(remindersQueryOptions({ status: "awaiting_print", page, pageSize: 100 }));
  const [deselected, setDeselected] = useState<ReadonlySet<number>>(new Set());
  const [postOn, setPostOn] = useState<string>(today);
  const [refusal, setRefusal] = useState<string | null>(null);
  const [result, setResult] = useState<{ answer: PrintBatchResult; letters: Reminder[] } | null>(null);
  const shown = letters.data?.data ?? [];
  const chosen = shown.filter((l) => !deselected.has(l.id));
  const print = useMutation({
    mutationFn: (batch: Reminder[]) =>
      createPrintBatch(
        batch.map((l) => l.id),
        postOn,
      ),
    onSuccess: async (answer, batch) => {
      setResult({ answer, letters: batch });
      setRefusal(null);
      setDeselected(new Set());
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
    },
    onError: (error) =>
      setRefusal(
        error instanceof ApiValidationError
          ? fieldRefusals(error, t, () => false, "printBatch").elsewhere.join(" ")
          : refusalMessage(error, t, date),
      ),
  });
  const days = Array.from({ length: MAX_POST_ON_DAYS + 1 }, (_, n) => addDays(today, n));
  const nameOf = (reminderId: number, of: Reminder[]) => {
    const letter = of.find((l) => l.id === reminderId);
    return letter ? letterName(t, letter.sequence) : t("print.letterId", { id: reminderId });
  };
  return (
    <Card withBorder data-testid="awaiting-print">
      <Stack gap="sm">
        <Title order={4}>{t("print.awaiting")}</Title>
        <Text size="sm" c="dimmed">
          {t("print.awaitingDescription")}
        </Text>
        {letters.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("print.failedToLoad")}>
            {refusalMessage(letters.error, t, date)}
          </Alert>
        )}
        {letters.isPending && <ContentSkeleton rows={3} rowHeight={32} />}
        {letters.data && shown.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("print.noneAwaiting")}
          </Text>
        )}
        {shown.length > 0 && (
          <>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th />
                  <Table.Th>{t("reminder.col.letter")}</Table.Th>
                  <Table.Th>{t("print.col.invoice")}</Table.Th>
                  <Table.Th>{t("print.col.made")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {shown.map((l) => {
                  const name = letterName(t, l.sequence);
                  return (
                    <Table.Tr key={l.id} data-testid={`awaiting-${l.id}`}>
                      <Table.Td>
                        <Checkbox
                          aria-label={t("print.chooseLetter", { letter: name, id: l.invoiceId })}
                          checked={!deselected.has(l.id)}
                          onChange={(event) => {
                            const on = event.currentTarget.checked;
                            setDeselected((current) => {
                              const next = new Set(current);
                              if (on) next.delete(l.id);
                              else next.add(l.id);
                              return next;
                            });
                            setRefusal(null);
                          }}
                        />
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm" fw={600}>
                          {name}
                        </Text>
                        <Text size="xs">{level(l)}</Text>
                      </Table.Td>
                      <Table.Td>
                        <RouteLink
                          href={invoiceHref(l.invoiceId)}
                          to={invoiceLinkOptions(l.invoiceId)}
                          aria-label={t("runPage.openInvoiceOf", { letter: name })}
                        >
                          {t("runPage.openInvoice")}
                        </RouteLink>
                      </Table.Td>
                      <Table.Td>
                        <RouteLink href={reminderRunHref(l.runId)} to={reminderRunLinkOptions(l.runId)}>
                          {t("overdue.runName", { id: l.runId })}
                        </RouteLink>
                        <Text size="xs" c="dimmed">
                          {dateTime(l.createdAt)}
                        </Text>
                      </Table.Td>
                    </Table.Tr>
                  );
                })}
              </Table.Tbody>
            </Table>
            {letters.data && letters.data.pagination.totalPages > 1 && (
              <Pagination total={letters.data.pagination.totalPages} value={page} onChange={setPage} />
            )}
            <Group align="flex-end">
              <Select
                label={t("print.postOn")}
                description={t("print.postOnHint")}
                data={days.map((d) => ({ value: d, label: date(d) }))}
                value={postOn}
                allowDeselect={false}
                onChange={(value) => {
                  if (value) setPostOn(value);
                  setRefusal(null);
                }}
              />
              <Button
                disabled={chosen.length === 0 || chosen.length > MAX_PRINT_LETTERS || print.isPending}
                loading={print.isPending}
                onClick={() => print.mutate(chosen)}
              >
                {t("print.print", { count: chosen.length })}
              </Button>
            </Group>
            {chosen.length > MAX_PRINT_LETTERS && (
              <Text size="sm" c="red">
                {t("print.tooMany", { max: MAX_PRINT_LETTERS, count: chosen.length })}
              </Text>
            )}
          </>
        )}
        {refusal && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("print.couldNotPrint")}>
            {refusal}
          </Alert>
        )}
        {result && (
          <Alert color="green" data-testid="print-result">
            <Stack gap="xs">
              <Text size="sm">
                {t("print.printed", {
                  id: result.answer.batch.id,
                  count: result.answer.batch.letters.filter((l) => l.status === "printed").length,
                  date: date(result.answer.batch.postOn),
                })}
              </Text>
              <Group>
                <PdfButton
                  url={printBatchPdfUrl(result.answer.batch.id)}
                  mode="download"
                  size="xs"
                  leftSection={<IconDownload size={14} />}
                  aria-label={t("print.downloadOf", { id: result.answer.batch.id })}
                >
                  {t("print.download")}
                </PdfButton>
              </Group>
              {result.answer.leftOut.length > 0 && (
                <Stack gap={4}>
                  <Text size="sm" fw={600}>
                    {t("print.leftOutHeading")}
                  </Text>
                  <List size="sm" spacing={2}>
                    {result.answer.leftOut.map((left) => (
                      <List.Item key={left.reminderId}>
                        {t("print.leftOutLine", {
                          letter: nameOf(left.reminderId, result.letters),
                          reason: leftOutWords(left),
                        })}
                      </List.Item>
                    ))}
                  </List>
                </Stack>
              )}
            </Stack>
          </Alert>
        )}
      </Stack>
    </Card>
  );
};

/** The print batches, newest first, each with its state, its PDF, and its confirmation or reprint. */
const BatchesCard = ({ currentUserId }: { currentUserId?: string }) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const who = useWho(currentUserId);
  const meta = useQuery(invoicesMetaQueryOptions());
  const today = meta.data?.today ?? dayjs().format("YYYY-MM-DD");
  const [page, setPage] = useState(1);
  const batches = useQuery(printBatchesQueryOptions({ page }));
  const [confirming, setConfirming] = useState<PrintBatch | null>(null);
  const [posted, setPosted] = useState<PrintBatchPosted | null>(null);
  const reprintBatch = useReprint();
  return (
    <Card withBorder data-testid="print-batches">
      <Stack gap="sm">
        <Title order={4}>{t("print.batches")}</Title>
        {batches.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("print.failedToLoad")}>
            {refusalMessage(batches.error, t, date)}
          </Alert>
        )}
        {batches.isPending && <ContentSkeleton rows={2} rowHeight={32} />}
        {posted && <PostedResult posted={posted} />}
        {batches.data && batches.data.data.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("print.noBatches")}
          </Text>
        )}
        {batches.data && batches.data.data.length > 0 && (
          <Table.ScrollContainer minWidth={800}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("print.col.batch")}</Table.Th>
                  <Table.Th>{t("print.col.postOn")}</Table.Th>
                  <Table.Th ta="right">{t("print.col.letters")}</Table.Th>
                  <Table.Th>{t("print.col.printed")}</Table.Th>
                  <Table.Th>{t("print.col.state")}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {batches.data.data.map((batch) => {
                  const state = printBatchState(batch);
                  const withPdf = batch.letters.some((l) => l.status === "printed" || l.status === "sent");
                  return (
                    <Table.Tr key={batch.id} data-testid={`batch-${batch.id}`}>
                      <Table.Td>{t("print.batchName", { id: batch.id })}</Table.Td>
                      <Table.Td>{date(batch.postOn)}</Table.Td>
                      <Table.Td ta="right">{batch.letters.length}</Table.Td>
                      <Table.Td>
                        <Text size="sm">{dateTime(batch.createdAt)}</Text>
                        <Text size="xs" c="dimmed">
                          {who(batch.createdBy)}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        <Badge
                          variant="light"
                          color={state === "open" ? "cyan" : state === "posted" ? "green" : "gray"}
                        >
                          {t(`print.state.${state}`, { date: batch.postedOn ? date(batch.postedOn) : "" })}
                        </Badge>
                        {state === "open" && batch.postOn > today && (
                          <Text size="xs" c="dimmed">
                            {t("print.toBePosted", { date: date(batch.postOn) })}
                          </Text>
                        )}
                      </Table.Td>
                      <Table.Td>
                        <Group gap={4} justify="flex-end" wrap="nowrap">
                          {withPdf && (
                            <PdfButton
                              url={printBatchPdfUrl(batch.id)}
                              mode="download"
                              size="xs"
                              variant="subtle"
                              leftSection={<IconDownload size={14} />}
                              aria-label={t("print.downloadOf", { id: batch.id })}
                            >
                              {t("reminder.pdf")}
                            </PdfButton>
                          )}
                          {state === "open" && batch.postOn <= today && (
                            <Button
                              size="xs"
                              variant="subtle"
                              aria-label={t("print.confirmPostedOf", { id: batch.id })}
                              onClick={() => setConfirming(batch)}
                            >
                              {t("print.confirmPosted")}
                            </Button>
                          )}
                          {state === "open" && (
                            <Button
                              size="xs"
                              variant="subtle"
                              color="orange"
                              aria-label={t("print.reprintOf", { id: batch.id })}
                              loading={reprintBatch.isPending && reprintBatch.variables === batch.id}
                              disabled={reprintBatch.isPending}
                              onClick={() => reprintBatch.mutate(batch.id)}
                            >
                              {t("print.reprint")}
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
        {batches.data && batches.data.pagination.totalPages > 1 && (
          <Pagination total={batches.data.pagination.totalPages} value={page} onChange={setPage} />
        )}
      </Stack>
      {confirming && (
        <PostedModal
          batch={confirming}
          today={today}
          onPosted={(answer) => {
            setPosted(answer);
            setConfirming(null);
          }}
          onClose={() => setConfirming(null)}
        />
      )}
    </Card>
  );
};

/** Returns a batch's printed letters to awaiting print, said in a notification. */
const useReprint = (onDone?: () => void) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (batchId: number) => reprint(batchId),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("print.reprinted") });
      onDone?.();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("print.couldNotReprint"), message: refusalMessage(error, t, date) }),
  });
};

/**
 * Confirms a batch posted (D10): the day it went, today the latest — a later
 * day is never offered — its own posting day chosen to begin with. Only that
 * day is accepted; posted on another, the batch must be reprinted, which the
 * refusal offers.
 */
const PostedModal = ({
  batch,
  today,
  onPosted,
  onClose,
}: {
  batch: PrintBatch;
  today: string;
  onPosted: (answer: PrintBatchPosted) => void;
  onClose: () => void;
}) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  // The days since it was printed — at most a week back — today first, and its posting day.
  const earliest = [osloDay(batch.createdAt), addDays(today, -MAX_POST_ON_DAYS)].sort()[1];
  const days: string[] = [];
  for (let day = today; day >= earliest; day = addDays(day, -1)) days.push(day);
  if (batch.postOn <= today && !days.includes(batch.postOn)) days.push(batch.postOn);
  const [postedOn, setPostedOn] = useState(days.includes(batch.postOn) ? batch.postOn : today);
  const [refusal, setRefusal] = useState<{ words: string; reprint: boolean } | null>(null);
  const confirm = useMutation({
    mutationFn: () => confirmPosted(batch.id, postedOn),
    onSuccess: async (answer) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      onPosted(answer);
    },
    onError: (error) => {
      const code = refusalCode(error);
      setRefusal({
        words:
          error instanceof ApiValidationError
            ? fieldRefusals(error, t, () => false, "posted").elsewhere.join(" ")
            : refusalMessage(error, t, date),
        reprint: code === "reminder_posted_early" || code === "reminder_posted_late",
      });
    },
  });
  const reprintBatch = useReprint(onClose);
  return (
    <Modal opened onClose={onClose} title={t("print.confirmPostedOf", { id: batch.id })}>
      <Stack>
        <Text size="sm">{t("print.postedFor", { date: date(batch.postOn) })}</Text>
        <Radio.Group
          label={t("print.postedOn")}
          description={t("print.postedOnHint")}
          value={postedOn}
          onChange={(value) => {
            setPostedOn(value);
            setRefusal(null);
          }}
        >
          <Stack gap={4} mt="xs">
            {days.map((day) => (
              <Radio key={day} value={day} label={date(day)} />
            ))}
          </Stack>
        </Radio.Group>
        {refusal && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("print.couldNotConfirm")}>
            <Stack gap="xs">
              <Text size="sm">{refusal.words}</Text>
              {refusal.reprint && (
                <Group>
                  <Button
                    size="xs"
                    color="orange"
                    loading={reprintBatch.isPending}
                    onClick={() => reprintBatch.mutate(batch.id)}
                  >
                    {t("print.reprint")}
                  </Button>
                </Group>
              )}
            </Stack>
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button disabled={confirm.isPending} loading={confirm.isPending} onClick={() => confirm.mutate()}>
            {t("print.confirmPosted")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** What a posting did: the letters sent, the charges it waived and why, the letters it skipped. */
const PostedResult = ({ posted }: { posted: PrintBatchPosted }) => {
  const { t } = useInvoiceFormat();
  const words = useWords();
  const nameOf = (reminderId: number) => {
    const letter = posted.batch.letters.find((l) => l.id === reminderId);
    return letter ? letterName(t, letter.sequence) : t("print.letterId", { id: reminderId });
  };
  return (
    <Alert color="green" data-testid="posted-result">
      <Stack gap={4}>
        <Text size="sm">{t("print.posted", { id: posted.batch.id })}</Text>
        {posted.waived.length > 0 && (
          <List size="sm" spacing={2}>
            {posted.waived.map((w) => (
              <List.Item key={w.reminderId}>
                {t("print.waivedLine", {
                  letter: nameOf(w.reminderId),
                  kinds: w.kinds.length > 1 ? t("print.kind.both") : t(`print.kind.${w.kinds[0]}`),
                  reason: words(`print.waiver.${w.reason}`, w.reason),
                })}
              </List.Item>
            ))}
          </List>
        )}
        {posted.skipped.length > 0 && (
          <Text size="sm">{t("print.skipped", { letters: posted.skipped.map(nameOf).join(", ") })}</Text>
        )}
      </Stack>
    </Alert>
  );
};
