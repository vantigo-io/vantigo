import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  Group,
  Modal,
  NumberInput,
  SegmentedControl,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconArrowBackUp, IconChecks, IconPlus, IconSettings, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AccountingBankLine,
  type AccountingBankSuggestion,
  type AccountingMeta,
  acceptExact,
  acceptSuggestion,
  codeLine,
  reconciliationQueryOptions,
  undoMatch,
} from "../api/accounting";
import { ACCOUNTING_QUERY_KEY, refusedFields } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { AccountSelect, TaxCodeSelect } from "../components/pickers";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import { decimalOf, oreOf } from "../lib/ore";
import { bankAccountSettingsPath, INVOICES_BANK_PATH, voucherPath } from "../lib/routes";

/** The month of a day, first and last. */
const monthOf = (today: string) => {
  const [y, m] = today.split("-").map(Number);
  const last = new Date(Date.UTC(y, m, 0)).getUTCDate();
  const mm = String(m).padStart(2, "0");
  return { from: `${y}-${mm}-01`, to: `${y}-${mm}-${String(last).padStart(2, "0")}` };
};

/**
 * Reconcile a bank account (A28, R11): its lines in a period, each matched
 * to the postings on its ledger account or open with the suggestions most
 * certain first, each with its reason; accept one, code the line, or undo a
 * match while the period is open. The foot says the statement's balance, the
 * ledger's, the difference, and the ledger's postings no line is matched to.
 */
export const ReconciliationPage = () => {
  const { bankAccountId } = useParams({ strict: false }) as { bankAccountId: string };
  return <BooksGate>{(meta) => <Reconciliation id={Number(bankAccountId)} meta={meta} />}</BooksGate>;
};

const Reconciliation = ({ id, meta }: { id: number; meta: AccountingMeta }) => {
  const { t, money, day } = useAccountingFormat();
  const month = monthOf(meta.today);
  const [from, setFrom] = useState(month.from);
  const [to, setTo] = useState(month.to);
  const [state, setState] = useState<"all" | "open" | "matched">("all");
  const query = useQuery(reconciliationQueryOptions(id, from, to, state === "all" ? undefined : state));
  const queryClient = useQueryClient();
  const invalidate = () => queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
  const bulk = useMutation({
    mutationFn: (ids: number[]) => acceptExact(ids),
    onSuccess: async (answer) => {
      await invalidate();
      notifications.show({ color: "green", message: t("bankBulkDone", { count: answer.accepted.length }) });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  if (query.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {query.error.message}
      </Alert>
    );
  if (query.isPending) return <ContentSkeleton rows={8} />;
  const r = query.data;
  const exact = r.lines.filter((l) => l.state === "open" && l.suggestions[0]?.exact);
  const confirmBulk = () =>
    modals.openConfirmModal({
      title: t("bankBulkTitle"),
      children: <Text size="sm">{t("bankBulkConfirm", { count: exact.length })}</Text>,
      labels: { confirm: t("bankBulkAccept"), cancel: t("cancel") },
      onConfirm: () => bulk.mutate(exact.map((l) => l.id)),
    });
  return (
    <Stack gap="lg">
      <PageHeader
        title={r.bankAccount.name}
        description={`${r.bankAccount.accountNumber} · ${r.bankAccount.ledgerAccountNumber} ${r.bankAccount.ledgerAccountName}`}
        actions={
          meta.canClose && (
            <Button
              variant="light"
              leftSection={<IconSettings size={16} />}
              renderRoot={(props) => <Link to={bankAccountSettingsPath(id)} {...props} />}
            >
              {t("navSettings")}
            </Button>
          )
        }
      />
      <Group align="flex-end" gap="sm">
        <TextInput type="date" label={t("from")} value={from} onChange={(e) => setFrom(e.currentTarget.value)} />
        <TextInput type="date" label={t("to")} value={to} onChange={(e) => setTo(e.currentTarget.value)} />
        <SegmentedControl
          value={state}
          onChange={(v) => setState(v as "all" | "open" | "matched")}
          data={[
            { value: "all", label: t("bankFilterAll") },
            { value: "open", label: t("bankFilterOpen") },
            { value: "matched", label: t("bankFilterMatched") },
          ]}
          aria-label={t("bankFilter")}
        />
        {meta.canPost && exact.length > 0 && (
          <Button leftSection={<IconChecks size={16} />} onClick={confirmBulk} loading={bulk.isPending}>
            {t("bankBulkAcceptCount", { count: exact.length })}
          </Button>
        )}
      </Group>
      {r.lines.length === 0 ? (
        <EmptyState title={t("bankNoLines")} description={t("bankNoLinesHint")} />
      ) : (
        <Stack gap="sm" data-testid="bank-lines">
          {r.lines.map((l) => (
            <LineCard key={l.id} line={l} canPost={meta.canPost} />
          ))}
        </Stack>
      )}
      <Card withBorder padding="md" data-testid="bank-foot">
        <SimpleGrid cols={{ base: 1, sm: 3 }}>
          <Stack gap={0}>
            <Text size="sm" c="dimmed">
              {t("bankStatementBalance", { day: day(r.to) })}
            </Text>
            <Text fw={600}>
              {r.foot.statementBalance === null ? t("bankNoBalance") : money(r.foot.statementBalance)}
            </Text>
          </Stack>
          <Stack gap={0}>
            <Text size="sm" c="dimmed">
              {t("bankLedgerBalance", { day: day(r.to) })}
            </Text>
            <Text fw={600}>{money(r.foot.ledgerBalance)}</Text>
          </Stack>
          <Stack gap={0}>
            <Text size="sm" c="dimmed">
              {t("bankDifference")}
            </Text>
            <Text fw={600} c={r.foot.difference !== null && Number(r.foot.difference) !== 0 ? "red" : undefined}>
              {r.foot.difference === null ? "–" : money(r.foot.difference)}
            </Text>
          </Stack>
        </SimpleGrid>
        {r.foot.unmatchedPostingsTotal > 0 && (
          <Stack gap={4} mt="md">
            <Text size="sm" fw={500}>
              {t("bankUnmatchedPostings", { count: r.foot.unmatchedPostingsTotal })}
            </Text>
            {r.foot.unmatchedPostings.map((p) => (
              <Text size="sm" key={p.postingId}>
                <Anchor component={Link} to={voucherPath(p.voucherId)} size="sm">
                  {p.voucherDisplay}
                </Anchor>{" "}
                · {day(p.voucherDate)} · {p.description} · {money(p.amount)}
              </Text>
            ))}
          </Stack>
        )}
      </Card>
    </Stack>
  );
};

/** One statement line: what the bank said, its match or its suggestions. */
const LineCard = ({ line, canPost }: { line: AccountingBankLine; canPost: boolean }) => {
  const { t, money, day } = useAccountingFormat();
  const queryClient = useQueryClient();
  const [coding, setCoding] = useState(false);
  const done = async () => {
    await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
  };
  const accept = useMutation({
    mutationFn: (key: string) => acceptSuggestion(line.id, key),
    onSuccess: async () => {
      await done();
      notifications.show({ color: "green", message: t("bankMatched") });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  const undo = useMutation({
    mutationFn: () => undoMatch(line.id),
    onSuccess: async () => {
      await done();
      notifications.show({ color: "green", message: t("bankUndone") });
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(t, error) }),
  });
  const confirmUndo = () =>
    modals.openConfirmModal({
      title: t("bankUndoTitle"),
      children: <Text size="sm">{line.match?.voucherId ? t("bankUndoReverses") : t("bankUndoUnlinks")}</Text>,
      labels: { confirm: t("bankUndo"), cancel: t("cancel") },
      onConfirm: () => undo.mutate(),
    });
  const who = [line.counterpartyName, line.counterpartyAccount].filter(Boolean).join(" · ");
  return (
    <Card withBorder padding="sm" data-testid={`bank-line-${line.id}`}>
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Stack gap={2}>
          <Group gap="xs">
            <Text fw={600}>{day(line.bookedOn)}</Text>
            <Badge variant="light" color={line.state === "matched" ? "teal" : "orange"}>
              {t(`bankLineState_${line.state}`)}
            </Badge>
            {line.kind !== "transfer" && <Badge variant="outline">{t(`bankLineKind_${line.kind}`)}</Badge>}
            {line.reversal && (
              <Badge variant="outline" color="grape">
                {t("bankReversal")}
              </Badge>
            )}
          </Group>
          <Text size="sm">{line.text || who || t("bankNoText")}</Text>
          {who && line.text && (
            <Text size="xs" c="dimmed">
              {who}
            </Text>
          )}
          {(line.kid || line.reference) && (
            <Text size="xs" c="dimmed">
              {[line.kid && `KID ${line.kid}`, line.reference].filter(Boolean).join(" · ")}
            </Text>
          )}
          {line.batch.length > 0 && (
            <Text size="xs" c="dimmed">
              {t("bankBatchOf", { count: line.batch.length })}:{" "}
              {line.batch.map((b) => `${money(b.amount)}${b.kid ? ` (${b.kid})` : ""}`).join(", ")}
            </Text>
          )}
        </Stack>
        <Text fw={700} c={Number(line.amount) < 0 ? "red" : undefined}>
          {money(line.amount)}
        </Text>
      </Group>
      {line.match ? (
        <Group justify="space-between" mt="xs">
          <Stack gap={2}>
            <Text size="sm">
              {t(`bankMatchKind_${line.match.kind}`)} · {line.match.reason}
            </Text>
            {line.match.postings.map((p) => (
              <Text size="xs" key={p.postingId}>
                <Anchor component={Link} to={voucherPath(p.voucherId)} size="xs">
                  {p.voucherDisplay}
                </Anchor>{" "}
                · {p.description} · {money(p.amount)}
              </Text>
            ))}
          </Stack>
          {canPost && (
            <Button
              size="xs"
              variant="subtle"
              leftSection={<IconArrowBackUp size={14} />}
              onClick={confirmUndo}
              loading={undo.isPending}
            >
              {t("bankUndo")}
            </Button>
          )}
        </Group>
      ) : (
        <Stack gap="xs" mt="xs">
          {line.suggestions.map((s, i) => (
            <SuggestionRow
              key={s.key}
              suggestion={s}
              first={i === 0}
              canPost={canPost}
              accepting={accept.isPending && accept.variables === s.key}
              onAccept={() => accept.mutate(s.key)}
            />
          ))}
          {line.suggestions.length === 0 && (
            <Text size="sm" c="dimmed">
              {t("bankNoSuggestions")}
            </Text>
          )}
          {canPost && !line.customerPayment && (
            <Group>
              <Button size="xs" variant="light" onClick={() => setCoding(true)}>
                {t("bankCodeIt")}
              </Button>
            </Group>
          )}
        </Stack>
      )}
      {coding && <CodeModal line={line} onClose={() => setCoding(false)} />}
    </Card>
  );
};

/** A suggestion with its reason, what it would post, and its accept. */
const SuggestionRow = ({
  suggestion: s,
  first,
  canPost,
  accepting,
  onAccept,
}: {
  suggestion: AccountingBankSuggestion;
  first: boolean;
  canPost: boolean;
  accepting: boolean;
  onAccept: () => void;
}) => {
  const { t, money } = useAccountingFormat();
  return (
    <Card withBorder padding="xs" bg={first ? "var(--mantine-color-gray-0)" : undefined}>
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Stack gap={2}>
          <Group gap="xs">
            <Badge size="sm" color={s.certainty === "exact" ? "teal" : s.certainty === "likely" ? "blue" : "gray"}>
              {t(`bankCertainty_${s.certainty}`)}
            </Badge>
            <Text size="sm" fw={500}>
              {t(`bankSuggestion_${s.kind}`)}
            </Text>
          </Group>
          <Text size="sm">{s.reason}</Text>
          {s.lines.length > 0 && (
            <Text size="xs" c="dimmed" data-testid="bank-proposed">
              {t("bankWouldPost")}:{" "}
              {s.lines
                .map(
                  (l) => `${l.accountNumber} ${money(l.amount)}${l.taxCode ? ` (${t("taxCode")} ${l.taxCode})` : ""}`,
                )
                .join(" · ")}
            </Text>
          )}
        </Stack>
        {s.kind === "invoices" ? (
          <Button size="xs" variant="light" renderRoot={(props) => <Link to={INVOICES_BANK_PATH} {...props} />}>
            {t("bankOpenInvoices")}
          </Button>
        ) : (
          canPost && (
            <Button
              size="xs"
              onClick={onAccept}
              loading={accepting}
              aria-label={t("bankAcceptNamed", { kind: t(`bankSuggestion_${s.kind}`) })}
            >
              {t("bankAccept")}
            </Button>
          )
        )}
      </Group>
    </Card>
  );
};

interface CodeRow {
  /** The row's own key, for the list. */
  id: number;
  accountId: string | null;
  /** The amount in øre, as a whole number: no float ever reaches the voucher. */
  ore: number;
  taxCodeId: string | null;
  text: string;
}

/** Code it (A28): a bank voucher with the accounts and tax codes the person gives, in øre, gross by default. */
const CodeModal = ({ line, onClose }: { line: AccountingBankLine; onClose: () => void }) => {
  const { t, money } = useAccountingFormat();
  const queryClient = useQueryClient();
  const target = -oreOf(line.amount);
  const [rows, setRows] = useState<CodeRow[]>([
    { id: 1, accountId: null, ore: target, taxCodeId: null, text: line.text },
  ]);
  const [description, setDescription] = useState(line.text);
  const [key] = useState(() => `code-${line.id}-${Date.now()}`);
  const sum = rows.reduce((total, r) => total + r.ore, 0);
  const balanced = sum === target;
  const save = useMutation({
    mutationFn: () =>
      codeLine(line.id, {
        key,
        description: description.trim() || null,
        entry: "gross",
        lines: rows.map((r) => ({
          accountId: Number(r.accountId),
          amount: decimalOf(r.ore),
          taxCodeId: r.taxCodeId ? Number(r.taxCodeId) : null,
          text: r.text.trim() || null,
        })),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("bankCoded") });
      onClose();
    },
  });
  const fields = refusedFields(save.error);
  const set = (i: number, change: Partial<CodeRow>) =>
    setRows((all) => all.map((r, j) => (j === i ? { ...r, ...change } : r)));
  return (
    <Modal opened onClose={onClose} title={t("bankCodeTitle", { amount: money(line.amount) })} size="xl">
      <Stack gap="sm">
        <Text size="sm">{t("bankCodeHint")}</Text>
        <TextInput
          label={t("description")}
          value={description}
          onChange={(e) => setDescription(e.currentTarget.value)}
        />
        {rows.map((r, i) => (
          <Group key={r.id} align="flex-end" wrap="nowrap">
            <AccountSelect
              label={t("account")}
              value={r.accountId}
              onChange={(v) => set(i, { accountId: v })}
              error={fields[`lines[${i}].accountId`]}
              style={{ flex: 2 }}
            />
            <NumberInput
              label={t("amount")}
              decimalScale={2}
              fixedDecimalScale
              value={r.ore / 100}
              onChange={(v) => set(i, { ore: Math.round(Number(v || 0) * 100) })}
              error={fields[`lines[${i}].amount`]}
              style={{ flex: 1 }}
            />
            <TaxCodeSelect
              label={t("taxCode")}
              value={r.taxCodeId}
              onChange={(v) => set(i, { taxCodeId: v })}
              error={fields[`lines[${i}].taxCodeId`]}
              style={{ flex: 2 }}
            />
            <TextInput
              label={t("text")}
              value={r.text}
              onChange={(e) => set(i, { text: e.currentTarget.value })}
              style={{ flex: 2 }}
            />
            {rows.length > 1 && (
              <ActionIcon
                variant="subtle"
                color="red"
                aria-label={t("removeLine")}
                onClick={() => setRows((all) => all.filter((_, j) => j !== i))}
              >
                <IconTrash size={16} />
              </ActionIcon>
            )}
          </Group>
        ))}
        <Group justify="space-between">
          <Button
            size="xs"
            variant="subtle"
            leftSection={<IconPlus size={14} />}
            onClick={() =>
              setRows((all) => [
                ...all,
                {
                  id: Math.max(...all.map((x) => x.id)) + 1,
                  accountId: null,
                  ore: target - sum,
                  taxCodeId: null,
                  text: "",
                },
              ])
            }
          >
            {t("addLine")}
          </Button>
          <Text size="sm" c={balanced ? undefined : "red"}>
            {balanced ? t("bankCodeBalanced") : t("bankCodeLeft", { amount: money(decimalOf(target - sum)) })}
          </Text>
        </Group>
        {save.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {refusalMessage(t, save.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            onClick={() => save.mutate()}
            loading={save.isPending}
            disabled={!balanced || rows.some((r) => !r.accountId)}
          >
            {t("bankCodePost")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
