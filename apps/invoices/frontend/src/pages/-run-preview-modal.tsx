import { Alert, Button, Checkbox, Group, List, Modal, Stack, Table, Text, Title } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type BankFreshness,
  MAX_RUN_ITEMS,
  makeRun,
  type PreviewScope,
  type RunPreviewLetter,
  type RunResult,
  runPreviewQueryOptions,
} from "../api/overdue";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import { RouteLink } from "../components/document-link";
import "../i18n";
import { accountNumber } from "../lib/bank";
import { fieldRefusals, refusalCode, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { useLetterLevel, useWords } from "../lib/reminders";
import { reminderRunHref, reminderRunLinkOptions } from "../lib/routes";

/**
 * The bank data's freshness (D10, I3): a warning when the latest booking
 * imported is older than the stale limit — or when no file was ever imported
 * — so a run may claim charges on invoices already paid; otherwise the day it
 * is booked up to. And, for every account imported as OCR giro, the standing
 * note that payments without a KID never reach such a file.
 */
export const FreshnessAlert = ({ freshness }: { freshness: BankFreshness }) => {
  const { t, date } = useInvoiceFormat();
  const ocr =
    freshness.ocrAccounts.length > 0 ? (
      <Text size="sm">
        {t("overdue.fresh.ocr", {
          count: freshness.ocrAccounts.length,
          accounts: freshness.ocrAccounts.map(accountNumber).join(", "),
        })}
      </Text>
    ) : null;
  if (freshness.stale) {
    return (
      <Alert
        color="yellow"
        icon={<IconAlertCircle size={16} />}
        title={t("overdue.fresh.staleTitle")}
        data-testid="bank-freshness"
      >
        <Stack gap={4}>
          <Text size="sm">
            {freshness.lastBookedOn
              ? t("overdue.fresh.stale", { date: date(freshness.lastBookedOn), days: freshness.staleImportDays })
              : t("overdue.fresh.never")}
          </Text>
          {ocr}
        </Stack>
      </Alert>
    );
  }
  return (
    <Stack gap={4} data-testid="bank-freshness">
      {freshness.lastBookedOn && (
        <Text size="sm" c="dimmed">
          {t("overdue.fresh.current", { date: date(freshness.lastBookedOn) })}
        </Text>
      )}
      {ocr && (
        <Alert color="blue" icon={<IconAlertCircle size={16} />}>
          {ocr}
        </Alert>
      )}
    </Stack>
  );
};

/** Whether a letter claims anything beyond the principal: a fee, compensation or interest. */
const carriesCharge = (l: RunPreviewLetter): boolean =>
  l.letter.fee > 0 || l.letter.compensation > 0 || l.letter.interest > 0;

export interface RunPreviewModalProps {
  /** The list's narrowing, which the preview takes too. */
  scope: PreviewScope;
  currency: string;
  onClose: () => void;
}

/**
 * "Send reminders" (D10, D22): the letters as they would go today, each
 * chosen to begin with and deselectable, with its channel, recipient and
 * their warnings, its figures and why it claims less than it might; the
 * invoices blocked or waiting, with why; the bank data's freshness, and — when
 * it is old — the confirmation a run with charges needs. Then the run, every
 * refusal in words, and what it made and skipped.
 */
export const RunPreviewModal = ({ scope, currency, onClose }: RunPreviewModalProps) => {
  const { t, money, date } = useInvoiceFormat();
  const words = useWords();
  const level = useLetterLevel();
  const queryClient = useQueryClient();
  const [result, setResult] = useState<RunResult | null>(null);
  const preview = useQuery({ ...runPreviewQueryOptions(scope), enabled: result === null });
  // The letters left out of the run, by invoice; every other is chosen.
  const [deselected, setDeselected] = useState<ReadonlySet<number>>(new Set());
  const [acknowledged, setAcknowledged] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);
  // The server found the bank data old when the preview did not: the box is offered from then on.
  const [staleRefused, setStaleRefused] = useState(false);
  const letters = preview.data?.letters ?? [];
  const chosen = letters.filter((l) => !deselected.has(l.invoiceId));
  // The stale-import confirmation is offered only where it matters, and sent only when offered and ticked.
  const askAcknowledgement = Boolean(preview.data?.freshness.stale || staleRefused) && chosen.some(carriesCharge);
  const run = useMutation({
    mutationFn: () =>
      makeRun(
        chosen.map((l) => ({ invoiceId: l.invoiceId, action: l.action })),
        acknowledged && askAcknowledgement,
      ),
    onSuccess: async (answer) => {
      setResult(answer);
      setRefusal(null);
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
    },
    onError: (error) => {
      if (refusalCode(error) === "bank_import_stale") setStaleRefused(true);
      setRefusal(
        error instanceof ApiValidationError
          ? fieldRefusals(error, t, () => false, "run").elsewhere.join(" ")
          : refusalMessage(error, t, date, (amount) => money(amount, currency)),
      );
    },
  });
  const toggle = (invoiceId: number, on: boolean) => {
    setDeselected((current) => {
      const next = new Set(current);
      if (on) next.delete(invoiceId);
      else next.add(invoiceId);
      return next;
    });
    setRefusal(null);
  };
  // The invoice numbers the run's answer names by document id.
  const numberOf = new Map(letters.map((l) => [l.invoiceId, l.number]));
  return (
    <Modal opened onClose={onClose} title={t("run.previewTitle")} size="90%">
      {result ? (
        <Stack data-testid="run-result">
          <Text>{t("run.made", { count: result.created.length })}</Text>
          {result.skipped.length > 0 && (
            <Stack gap={4}>
              <Title order={5}>{t("run.skippedHeading")}</Title>
              <List size="sm" spacing={2}>
                {result.skipped.map((s) => (
                  <List.Item key={s.invoiceId}>
                    {t("run.skippedLine", {
                      number: numberOf.get(s.invoiceId) ?? s.invoiceId,
                      reason: words(`run.skip.${s.reason}`, s.reason),
                    })}
                  </List.Item>
                ))}
              </List>
            </Stack>
          )}
          <Group justify="space-between">
            <RouteLink href={reminderRunHref(result.run.id)} to={reminderRunLinkOptions(result.run.id)}>
              {t("run.openRun", { id: result.run.id })}
            </RouteLink>
            <Button onClick={onClose}>{t("run.close")}</Button>
          </Group>
        </Stack>
      ) : (
        <Stack>
          {preview.isPending && <ContentSkeleton rows={4} rowHeight={36} />}
          {preview.isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("run.couldNotPreview")}>
              {refusalMessage(preview.error, t, date)}
            </Alert>
          )}
          {preview.data && (
            <>
              <FreshnessAlert freshness={preview.data.freshness} />
              <ListWarnings warnings={preview.data.warnings} />
              <Title order={5}>{t("run.lettersHeading")}</Title>
              {letters.length === 0 ? (
                <Text size="sm" c="dimmed">
                  {t("run.noLetters")}
                </Text>
              ) : (
                <Table.ScrollContainer minWidth={900}>
                  <Table>
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th />
                        <Table.Th>{t("overdue.col.invoice")}</Table.Th>
                        <Table.Th>{t("overdue.col.customer")}</Table.Th>
                        <Table.Th>{t("run.col.letter")}</Table.Th>
                        <Table.Th>{t("reminder.col.channel")}</Table.Th>
                        <Table.Th>{t("reminder.col.deadline")}</Table.Th>
                        <Table.Th ta="right">{t("reminder.col.total")}</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {letters.map((l) => (
                        <Table.Tr key={l.invoiceId} data-testid={`preview-letter-${l.number}`}>
                          <Table.Td>
                            <Checkbox
                              aria-label={t("run.chooseLetter", { number: l.number })}
                              checked={!deselected.has(l.invoiceId)}
                              onChange={(event) => toggle(l.invoiceId, event.currentTarget.checked)}
                            />
                          </Table.Td>
                          <Table.Td>{l.number}</Table.Td>
                          <Table.Td>{l.buyerName}</Table.Td>
                          <Table.Td>
                            <Text size="sm">{level(l.letter)}</Text>
                            {l.chargeNotes.map((note) => (
                              <Text key={note} size="xs" c="dimmed">
                                {words(`reminder.chargeNote.${note}`, note)}
                              </Text>
                            ))}
                          </Table.Td>
                          <Table.Td>
                            <Text size="sm">{t(`reminder.channel.${l.channel}`)}</Text>
                            {l.recipient && (
                              <Text size="xs" c="dimmed">
                                {l.recipient}
                              </Text>
                            )}
                            {l.warnings.map((w) => (
                              <Text key={w} size="xs" c="orange">
                                {words(`run.warning.${w}`, w)}
                              </Text>
                            ))}
                          </Table.Td>
                          <Table.Td>{date(l.letter.deadline)}</Table.Td>
                          <Table.Td ta="right">
                            <Text size="sm">{money(l.letter.total, currency)}</Text>
                            {l.letter.fee > 0 && (
                              <Text size="xs" c="dimmed">
                                {t("reminder.next.fee", { amount: money(l.letter.fee, currency) })}
                              </Text>
                            )}
                            {l.letter.compensation > 0 && (
                              <Text size="xs" c="dimmed">
                                {t("reminder.next.compensation", { amount: money(l.letter.compensation, currency) })}
                              </Text>
                            )}
                            {l.letter.interest > 0 && (
                              <Text size="xs" c="dimmed">
                                {t("reminder.next.interest", { amount: money(l.letter.interest, currency) })}
                              </Text>
                            )}
                          </Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </Table.ScrollContainer>
              )}
              {preview.data.blockedOrWaiting.length > 0 && (
                <Stack gap={4} data-testid="preview-held">
                  <Title order={5}>{t("run.heldHeading")}</Title>
                  <Table>
                    <Table.Tbody>
                      {preview.data.blockedOrWaiting.map((h) => (
                        <Table.Tr key={h.invoiceId}>
                          <Table.Td>{h.number}</Table.Td>
                          <Table.Td>{h.buyerName}</Table.Td>
                          <Table.Td>
                            <Text size="sm">
                              {words(`overdue.action.${h.nextAction.action}`, h.nextAction.action)}
                              {h.nextAction.earliestOn &&
                                ` ${t("overdue.until", { date: date(h.nextAction.earliestOn) })}`}
                            </Text>
                            {h.nextAction.reasons.map((reason) => (
                              <Text key={reason} size="xs" c="dimmed">
                                {words(`reminder.reason.${reason}`, reason, {
                                  kind: h.nextAction.outdated
                                    ? words(`rates.kind.${h.nextAction.outdated.kind}`, h.nextAction.outdated.kind)
                                    : "",
                                  halfYear: h.nextAction.outdated?.halfYear ?? "",
                                })}
                              </Text>
                            ))}
                          </Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </Stack>
              )}
              {askAcknowledgement && (
                <Checkbox
                  label={t("run.acknowledgeStale")}
                  checked={acknowledged}
                  onChange={(event) => {
                    setAcknowledged(event.currentTarget.checked);
                    setRefusal(null);
                  }}
                />
              )}
              {chosen.length > MAX_RUN_ITEMS && (
                <Text size="sm" c="red">
                  {t("run.tooMany", { max: MAX_RUN_ITEMS, count: chosen.length })}
                </Text>
              )}
              {refusal && (
                <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("run.couldNotRun")}>
                  {refusal}
                </Alert>
              )}
              <Group justify="flex-end">
                <Button variant="default" onClick={onClose}>
                  {t("cancel")}
                </Button>
                <Button
                  disabled={chosen.length === 0 || chosen.length > MAX_RUN_ITEMS || run.isPending}
                  loading={run.isPending}
                  onClick={() => run.mutate()}
                >
                  {t("run.send", { count: chosen.length })}
                </Button>
              </Group>
            </>
          )}
        </Stack>
      )}
    </Modal>
  );
};

/**
 * The overdue list's and the preview's warnings in words — but the bank
 * data's two, which `FreshnessAlert` says with its dates and accounts.
 */
export const ListWarnings = ({ warnings }: { warnings: readonly string[] }) => {
  const words = useWords();
  const shown = warnings.filter((w) => w !== "bank_data_stale" && w !== "ocr_without_kid_payments");
  if (shown.length === 0) return null;
  return (
    <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
      <Stack gap={4}>
        {shown.map((w) => (
          <Text key={w} size="sm">
            {words(`overdue.warning.${w}`, w)}
          </Text>
        ))}
      </Stack>
    </Alert>
  );
};
