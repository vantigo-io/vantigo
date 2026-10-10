import { Anchor, Badge, Button, Group, List, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconChevronDown, IconChevronRight } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { appUrl } from "@vantigo/frontend-shell";
import { Fragment, type ReactNode, useState } from "react";
import { type BankTransaction, type BankTransactionApplied, reopenTransaction, treatAsDistinct } from "../api/bank";
import { INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { accountNumber, useWho } from "../lib/bank";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { bankFileHref, bankFileLinkOptions } from "../lib/routes";
import { ApplyDialog } from "../pages/-apply-dialog";
import { QueueNoteDialog } from "../pages/-queue-note-dialog";
import { ReversalDialog } from "../pages/-reversal-dialog";
import { DocumentLink } from "./document-link";

/** A link to one bank file's page: a real href, and the router's own navigation on a plain click. */
export const BankFileLink = ({ bankFileId, children }: { bankFileId: number; children: ReactNode }) => {
  const navigate = useNavigate() as (options: unknown) => void;
  return (
    <Anchor
      href={appUrl(bankFileHref(bankFileId))}
      onClick={(event) => {
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
        event.preventDefault();
        navigate(bankFileLinkOptions(bankFileId));
      }}
    >
      {children}
    </Anchor>
  );
};

/** Which dialog a line has open. */
type Open = { line: BankTransaction; dialog: "apply" | "reversal" | "dismiss" | "confirm" };

/**
 * What a person may do to a line in its state (D5): an exception is applied
 * or dismissed — a reversal handled instead, a negative line only dismissed,
 * a possible duplicate also confirmed; a duplicate row confirmed or kept as a
 * payment of its own; a resolved line, or a matched one whose payments were
 * all removed, reopened — never a line the bank reversed a payment of, whose
 * money went back. The server judges each again.
 */
const actionsOf = (line: BankTransaction): ("apply" | "dismiss" | "reversal" | "confirm" | "distinct" | "reopen")[] => {
  if (line.status === "exception") {
    if (line.reason === "reversal") return ["reversal"];
    if (line.reason === "negative_amount") return ["dismiss"];
    if (line.reason === "possible_duplicate") return ["apply", "confirm", "dismiss"];
    return ["apply", "dismiss"];
  }
  if (line.status === "duplicate") return ["confirm", "distinct"];
  if (line.events.some((e) => e.event === "reversed")) return [];
  if (line.status === "resolved") return ["reopen"];
  if (line.status === "matched" && line.applied.length > 0 && line.applied.every((a) => a.removed)) return ["reopen"];
  return [];
};

export interface BankTransactionTableProps {
  lines: BankTransaction[];
  /** The installation's currency, meta's: every bank line is in NOK. */
  currency: string;
  /** `invoices:payments` — the queue's actions. */
  canAct: boolean;
  /** The signed-in user's id, so "by you" can be said. */
  currentUserId?: string;
  /** Whether each line names its file — the queue does, a file's own page does not. */
  showFile?: boolean;
}

/**
 * Bank lines as the bank wrote them and as matching and the queue left them
 * (D3–D5): the booking day, the KID or else the text, the debtor, the amount,
 * what was applied — each payment and charge payment linked to its invoice, a
 * removed one struck through — and the unapplied rest, the state and its
 * reason; under "Details", the events, the suggestions and the line a
 * possible duplicate may repeat. The actions open their dialogs; "Treat as
 * distinct" and "Reopen" act at once. Every refusal is said in words.
 */
export const BankTransactionTable = ({
  lines,
  currency,
  canAct,
  currentUserId,
  showFile = false,
}: BankTransactionTableProps) => {
  const { t, money, date, dateTime } = useInvoiceFormat();
  const who = useWho(currentUserId);
  const queryClient = useQueryClient();
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [open, setOpen] = useState<Open | null>(null);
  const amount = (value: number) => money(value, currency);

  const direct = useMutation({
    mutationFn: ({ line, kind }: { line: BankTransaction; kind: "distinct" | "reopen" }) =>
      kind === "distinct" ? treatAsDistinct(line.id) : reopenTransaction(line.id),
    onSuccess: async (_line, { kind }) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({
        color: "green",
        message: t(kind === "distinct" ? "bank.done.distinct" : "bank.done.reopened"),
      });
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("bank.couldNotAct"),
        message: refusalMessage(error, t, date, amount, dateTime),
      }),
  });

  const toggle = (id: number) =>
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const appliedItem = (a: BankTransactionApplied) => {
    const words =
      a.kind === "payment"
        ? t("bank.appliedPayment", { number: a.number ?? a.invoiceId, amount: amount(a.amount) })
        : t("bank.appliedCharges", { number: a.number ?? a.invoiceId, amount: amount(a.amount) });
    return (
      <Text
        key={`${a.kind}-${a.id}`}
        size="sm"
        data-removed={a.removed ? "true" : undefined}
        style={a.removed ? { textDecoration: "line-through" } : undefined}
        c={a.removed ? "dimmed" : undefined}
      >
        <DocumentLink invoiceId={a.invoiceId}>{words}</DocumentLink>
        {a.removed && ` (${t("bank.appliedRemoved")})`}
      </Text>
    );
  };

  const columns = 8 + (showFile ? 1 : 0);
  return (
    <>
      <Table.ScrollContainer minWidth={900}>
        <Table verticalSpacing="xs">
          <Table.Thead>
            <Table.Tr>
              <Table.Th />
              <Table.Th>{t("bank.col.bookedOn")}</Table.Th>
              <Table.Th>{t("bank.col.reference")}</Table.Th>
              <Table.Th>{t("bank.col.debtor")}</Table.Th>
              <Table.Th ta="right">{t("bank.col.amount")}</Table.Th>
              <Table.Th>{t("bank.col.applied")}</Table.Th>
              <Table.Th ta="right">{t("bank.col.unapplied")}</Table.Th>
              <Table.Th>{t("bank.col.state")}</Table.Th>
              {showFile && <Table.Th>{t("bank.filter.file")}</Table.Th>}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {lines.map((line) => {
              const isOpen = expanded.has(line.id);
              const actions = canAct ? actionsOf(line) : [];
              return (
                <Fragment key={line.id}>
                  <Table.Tr data-testid={`bank-line-${line.id}`}>
                    <Table.Td>
                      <Button
                        size="compact-xs"
                        variant="subtle"
                        aria-expanded={isOpen}
                        aria-label={t("bank.detailsOf", { ref: line.lineRef })}
                        onClick={() => toggle(line.id)}
                        leftSection={isOpen ? <IconChevronDown size={14} /> : <IconChevronRight size={14} />}
                      >
                        {t("bank.details")}
                      </Button>
                    </Table.Td>
                    <Table.Td>{date(line.bookedOn)}</Table.Td>
                    <Table.Td>
                      <Stack gap={0}>
                        {line.kid && <Text size="sm">{t("bank.kid", { kid: line.kid })}</Text>}
                        {line.remittanceText && (
                          <Text size="sm" c={line.kid ? "dimmed" : undefined}>
                            {line.remittanceText}
                          </Text>
                        )}
                        <Text size="xs" c="dimmed">
                          {t("bank.lineRef", { ref: line.lineRef })}
                        </Text>
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      <Stack gap={0}>
                        <Text size="sm">{line.debtorName}</Text>
                        {line.debtorAccount && (
                          <Text size="xs" c="dimmed">
                            {accountNumber(line.debtorAccount)}
                          </Text>
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td ta="right">
                      <Stack gap={2} align="flex-end">
                        <Text size="sm">{amount(line.negative ? -line.amount : line.amount)}</Text>
                        {line.direction === "debit" && (
                          <Badge color="orange" variant="light">
                            {t("bank.reversalBadge")}
                          </Badge>
                        )}
                        {line.negative && (
                          <Badge color="orange" variant="light">
                            {t("bank.negativeBadge")}
                          </Badge>
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      {line.applied.length === 0 ? (
                        <Text size="sm">{t("notAvailable")}</Text>
                      ) : (
                        <Stack gap={0}>{line.applied.map(appliedItem)}</Stack>
                      )}
                    </Table.Td>
                    <Table.Td ta="right">{amount(line.unappliedAmount)}</Table.Td>
                    <Table.Td>
                      <Stack gap={4}>
                        <Badge
                          variant="light"
                          color={line.status === "exception" || line.status === "duplicate" ? "yellow" : "gray"}
                        >
                          {t(`bank.status.${line.status}`)}
                        </Badge>
                        {line.reason && <Text size="sm">{t(`bank.reason.${line.reason}`)}</Text>}
                        {line.resolution && (
                          <Text size="xs" c="dimmed">
                            {t(`bank.resolution.${line.resolution}`)}
                          </Text>
                        )}
                        {actions.length > 0 && (
                          <Group gap={4}>
                            {actions.map((kind) => (
                              <Button
                                key={kind}
                                size="compact-xs"
                                variant={kind === "apply" || kind === "reversal" ? "filled" : "default"}
                                aria-label={t("bank.actionOn", { action: t(`bank.action.${kind}`), ref: line.lineRef })}
                                disabled={direct.isPending}
                                onClick={() => {
                                  if (kind === "distinct" || kind === "reopen") direct.mutate({ line, kind });
                                  else setOpen({ line, dialog: kind });
                                }}
                              >
                                {t(`bank.action.${kind}`)}
                              </Button>
                            ))}
                          </Group>
                        )}
                      </Stack>
                    </Table.Td>
                    {showFile && (
                      <Table.Td>
                        <BankFileLink bankFileId={line.bankFile.id}>
                          {t("bank.fileName", { id: line.bankFile.id })}
                        </BankFileLink>
                      </Table.Td>
                    )}
                  </Table.Tr>
                  {isOpen && (
                    <Table.Tr>
                      <Table.Td colSpan={columns}>
                        <LineDetails line={line} currency={currency} who={who} />
                      </Table.Td>
                    </Table.Tr>
                  )}
                </Fragment>
              );
            })}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {open?.dialog === "apply" && <ApplyDialog line={open.line} currency={currency} onClose={() => setOpen(null)} />}
      {open?.dialog === "reversal" && (
        <ReversalDialog line={open.line} currency={currency} onClose={() => setOpen(null)} />
      )}
      {(open?.dialog === "dismiss" || open?.dialog === "confirm") && (
        <QueueNoteDialog line={open.line} kind={open.dialog} onClose={() => setOpen(null)} />
      )}
    </>
  );
};

/**
 * What a line carries beyond its row: its resolution and note, what happened
 * to it, the invoices it may pay, and the line a possible duplicate may
 * repeat — with that line's payments and whether a reversal took one back.
 */
const LineDetails = ({
  line,
  currency,
  who,
}: {
  line: BankTransaction;
  currency: string;
  who: (userId: string | undefined) => string;
}) => {
  const { t, money, date, dateTime } = useInvoiceFormat();
  const twin = line.possibleDuplicateOf;
  const suggested = line.suggestions?.find((s) => s.invoiceId === line.suggestedInvoiceId);
  return (
    <Stack gap="xs" data-testid={`bank-line-details-${line.id}`}>
      {line.resolution && line.resolvedAt && (
        <Text size="sm">
          {t("bank.resolvedBy", {
            resolution: t(`bank.resolution.${line.resolution}`),
            at: dateTime(line.resolvedAt),
            who: who(line.resolvedBy),
          })}
        </Text>
      )}
      {line.resolutionNote && <Text size="sm">{t("bank.resolutionNote", { note: line.resolutionNote })}</Text>}
      {twin && (
        <Stack gap={2}>
          <Text size="sm">
            {t("bank.twin", {
              ref: twin.lineRef,
              file: twin.bankFileId,
              date: date(twin.bookedOn),
              amount: money(twin.amount, currency),
              status: t(`bank.status.${twin.status}`),
            })}{" "}
            <BankFileLink bankFileId={twin.bankFileId}>
              {t("bank.openEarlierFile", { id: twin.bankFileId })}
            </BankFileLink>
          </Text>
          {twin.reversed && (
            <Text size="sm" c="orange" data-testid="twin-reversed">
              {t("bank.twinReversed")}
            </Text>
          )}
          {twin.applied.length > 0 && (
            <>
              <Text size="sm">{t("bank.twinPayments")}</Text>
              <List size="sm">
                {twin.applied.map((a) => (
                  <List.Item
                    key={`${a.kind}-${a.id}`}
                    data-removed={a.removed ? "true" : undefined}
                    style={a.removed ? { textDecoration: "line-through" } : undefined}
                  >
                    <DocumentLink invoiceId={a.invoiceId}>
                      {t(a.kind === "payment" ? "bank.appliedPayment" : "bank.appliedCharges", {
                        number: a.number ?? a.invoiceId,
                        amount: money(a.amount, currency),
                      })}
                    </DocumentLink>
                  </List.Item>
                ))}
              </List>
            </>
          )}
        </Stack>
      )}
      {line.suggestions && line.suggestions.length > 0 && (
        <Stack gap={2}>
          <Text size="sm" fw={600}>
            {t("bank.suggestions")}
          </Text>
          <List size="sm">
            {line.suggestions.map((s) => (
              <List.Item key={s.invoiceId}>
                <DocumentLink invoiceId={s.invoiceId}>
                  {t("bank.suggestionLine", {
                    number: s.number,
                    buyer: s.buyerName,
                    open: money(s.openAmount, currency),
                    why: t(`bank.why.${s.why}`),
                  })}
                </DocumentLink>
              </List.Item>
            ))}
          </List>
        </Stack>
      )}
      {line.status === "exception" && line.suggestedInvoiceId !== undefined && !suggested && (
        <Text size="sm">
          <DocumentLink invoiceId={line.suggestedInvoiceId}>{t("bank.suggestedInvoice")}</DocumentLink>
        </Text>
      )}
      <Text size="sm" fw={600}>
        {t("bank.events")}
      </Text>
      <List size="sm">
        {line.events.map((e) => (
          <List.Item key={e.id}>
            {t("bank.eventLine", { event: t(`bank.event.${e.event}`), at: dateTime(e.at), who: who(e.by) })}
            {e.reason && ` — ${t("bank.eventReason", { reason: t(`bank.reason.${e.reason}`) })}`}
            {e.note && ` — ${t("bank.eventNote", { note: e.note })}`}
          </List.Item>
        ))}
      </List>
    </Stack>
  );
};
