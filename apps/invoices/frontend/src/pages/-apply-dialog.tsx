import { ActionIcon, Alert, Button, Group, Modal, NumberInput, Stack, Text, Textarea, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { useState } from "react";
import { applyTransaction, type BankTransaction } from "../api/bank";
import { invoiceListQueryOptions, invoiceQueryOptions } from "../api/invoices";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalCode, refusalMessage, refusalProblem } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface ApplyDialogProps {
  /** An exception the person applies to invoices. */
  line: BankTransaction;
  /** The installation's currency, meta's. */
  currency: string;
  onClose: () => void;
}

/** One invoice the line pays, and what it pays of the principal and of the charges. */
interface Row {
  invoiceId: number;
  number: number;
  buyer: string;
  /** Its open amount when the screen knows it; the server judges it again. */
  openAmount?: number;
  amount: number | string;
  charges: number | string;
}

/** Amounts are added in øre, so 0.1 + 0.2 is never more than 0.3. */
const ore = (value: number | string): number =>
  Math.round((typeof value === "number" ? value : Number(value) || 0) * 100);

/**
 * The rows a dialog opens with: the invoice the line's KID named — or its one
 * suggestion — first, then every suggestion, each filled in order with up to
 * its open amount until the line's rest runs out.
 */
const initialRows = (line: BankTransaction, named?: Omit<Row, "amount" | "charges">): Row[] => {
  const candidates: Omit<Row, "amount" | "charges">[] = [];
  if (named) candidates.push(named);
  for (const s of line.suggestions ?? []) {
    if (!candidates.some((c) => c.invoiceId === s.invoiceId)) {
      candidates.push({ invoiceId: s.invoiceId, number: s.number, buyer: s.buyerName, openAmount: s.openAmount });
    }
  }
  let left = ore(line.unappliedAmount);
  return candidates.map((c) => {
    const amount = c.openAmount === undefined ? 0 : Math.max(0, Math.min(left, ore(c.openAmount)));
    left -= amount;
    return { ...c, amount: amount / 100, charges: 0 };
  });
};

/**
 * Applies an exception to invoices (D5): the suggestions pre-filled, an
 * invoice added by its number, and for each invoice the principal and the
 * reminder charges it pays — a settled invoice's charges alone included. The
 * totals are live, and allocations that add up to more than is left of the
 * line are refused in the form before anything is sent; every refusal of the
 * server's is said in words, an amount over an invoice's open amount naming
 * that invoice.
 */
export const ApplyDialog = ({ line, currency, onClose }: ApplyDialogProps) => {
  const { t } = useInvoiceFormat();
  // The invoice the KID named, when the suggestions do not carry it, is read
  // for its number and open amount before the form opens.
  const namedId =
    line.suggestedInvoiceId !== undefined && !line.suggestions?.some((s) => s.invoiceId === line.suggestedInvoiceId)
      ? line.suggestedInvoiceId
      : undefined;
  const named = useQuery({ ...invoiceQueryOptions(namedId ?? 0), enabled: namedId !== undefined });
  const ready = namedId === undefined || !named.isPending;
  const namedRow =
    named.data?.number !== undefined
      ? {
          invoiceId: named.data.id,
          number: named.data.number,
          buyer: named.data.customerName ?? "",
          openAmount: named.data.openAmount,
        }
      : undefined;
  return (
    <Modal opened onClose={onClose} size="lg" title={t("bank.apply.title", { ref: line.lineRef })}>
      {ready ? (
        <ApplyForm line={line} currency={currency} rows={initialRows(line, namedRow)} onClose={onClose} />
      ) : (
        <ContentSkeleton rows={3} rowHeight={36} />
      )}
    </Modal>
  );
};

const ApplyForm = ({ line, currency, rows: initial, onClose }: ApplyDialogProps & { rows: Row[] }) => {
  const { t, money, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [rows, setRows] = useState<Row[]>(initial);
  const [note, setNote] = useState("");
  const [number, setNumber] = useState("");
  const [addError, setAddError] = useState<string | null>(null);
  const [refusal, setRefusal] = useState<string | null>(null);
  const amount = (value: number) => money(value, currency);

  const left = ore(line.unappliedAmount);
  const allocated = rows.reduce((sum, r) => sum + ore(r.amount) + ore(r.charges), 0);
  const over = allocated - left;
  // A negative amount is never an allocation: refused here, as the server would.
  const negative = rows.some((r) => ore(r.amount) < 0 || ore(r.charges) < 0);
  const blocked = rows.length === 0 || allocated <= 0 || over > 0 || negative;

  const change = (invoiceId: number, patch: Partial<Row>) => {
    setRefusal(null);
    setRows((current) => current.map((r) => (r.invoiceId === invoiceId ? { ...r, ...patch } : r)));
  };

  const add = async () => {
    const wanted = Number(number.trim());
    setAddError(null);
    if (!Number.isSafeInteger(wanted) || wanted <= 0) {
      setAddError(t("bank.apply.notFound", { number: number.trim() }));
      return;
    }
    if (rows.some((r) => r.number === wanted)) {
      setAddError(t("bank.apply.already", { number: wanted }));
      return;
    }
    try {
      const list = await queryClient.fetchQuery(
        invoiceListQueryOptions({ search: String(wanted), kind: "invoice", status: "issued" }),
      );
      const found = list.data.find((d) => d.number === wanted);
      if (!found) {
        setAddError(t("bank.apply.notFound", { number: wanted }));
        return;
      }
      setRows((current) => [
        ...current,
        {
          invoiceId: found.id,
          number: wanted,
          buyer: found.customerName ?? "",
          openAmount: found.openAmount,
          amount: 0,
          charges: 0,
        },
      ]);
      setNumber("");
    } catch (error) {
      setAddError(refusalMessage(error, t, date, amount, dateTime));
    }
  };

  const apply = useMutation({
    mutationFn: () =>
      applyTransaction(line.id, {
        // An invoice with nothing to pay is left out, rather than refused for it.
        allocations: rows
          .filter((r) => ore(r.amount) + ore(r.charges) > 0)
          .map((r) => ({
            invoiceId: r.invoiceId,
            amount: ore(r.amount) / 100,
            ...(ore(r.charges) > 0 ? { chargesAmount: ore(r.charges) / 100 } : {}),
          })),
        ...(note.trim() ? { note: note.trim() } : {}),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("bank.done.applied") });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        setRefusal(fieldRefusals(error, t, () => false, "apply").elsewhere.join(" "));
        return;
      }
      // An amount over an invoice's open amount names that invoice by its number.
      const problem = refusalProblem(error);
      if (refusalCode(error) === "payment_exceeds_open" && typeof problem.invoiceId === "number") {
        const row = rows.find((r) => r.invoiceId === problem.invoiceId);
        setRefusal(
          t("applyRefusal.payment_exceeds_open", {
            number: row?.number ?? problem.invoiceId,
            openAmount: typeof problem.openAmount === "number" ? amount(problem.openAmount) : "",
          }),
        );
        return;
      }
      setRefusal(refusalMessage(error, t, date, amount, dateTime));
    },
  });

  return (
    <Stack>
      <Text size="sm">
        {t("bank.apply.lineSummary", {
          amount: amount(line.amount),
          date: date(line.bookedOn),
          left: amount(line.unappliedAmount),
        })}
      </Text>
      {rows.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("bank.apply.noRows")}
        </Text>
      )}
      {rows.map((r) => (
        <Stack key={r.invoiceId} gap={4} data-testid={`allocation-${r.number}`}>
          <Group justify="space-between" wrap="nowrap">
            <Text size="sm" fw={600}>
              {t("bank.apply.invoiceRow", { number: r.number, buyer: r.buyer })}
              {r.openAmount !== undefined && ` — ${t("bank.apply.openAmount", { open: amount(r.openAmount) })}`}
            </Text>
            <ActionIcon
              variant="subtle"
              color="red"
              aria-label={t("bank.apply.remove", { number: r.number })}
              onClick={() => setRows((current) => current.filter((x) => x.invoiceId !== r.invoiceId))}
            >
              <IconTrash size={16} />
            </ActionIcon>
          </Group>
          <Group grow>
            <NumberInput
              label={t("bank.apply.amount", { number: r.number })}
              min={0}
              allowNegative={false}
              decimalScale={2}
              hideControls
              rightSection={currency}
              rightSectionWidth={48}
              value={r.amount}
              onChange={(v) => change(r.invoiceId, { amount: v })}
            />
            <NumberInput
              label={t("bank.apply.charges", { number: r.number })}
              min={0}
              allowNegative={false}
              decimalScale={2}
              hideControls
              rightSection={currency}
              rightSectionWidth={48}
              value={r.charges}
              onChange={(v) => change(r.invoiceId, { charges: v })}
            />
          </Group>
        </Stack>
      ))}
      <Group align="flex-end">
        <TextInput
          label={t("bank.apply.addNumber")}
          inputMode="numeric"
          value={number}
          error={addError ?? undefined}
          onChange={(e) => {
            setNumber(e.currentTarget.value);
            setAddError(null);
          }}
        />
        <Button variant="default" disabled={!number.trim()} onClick={() => void add()}>
          {t("bank.apply.add")}
        </Button>
      </Group>
      <Text size="sm" fw={600} data-testid="apply-total">
        {t("bank.apply.total", {
          allocated: amount(allocated / 100),
          left: amount(left / 100),
          rest: amount(Math.max(0, left - allocated) / 100),
        })}
      </Text>
      {over > 0 && (
        <Text size="sm" c="red" role="alert">
          {t("bank.apply.over", { over: amount(over / 100) })}
        </Text>
      )}
      {rows.length > 0 && allocated <= 0 && (
        <Text size="sm" c="dimmed">
          {t("bank.apply.nothing")}
        </Text>
      )}
      <Text size="xs" c="dimmed">
        {t("bank.apply.restNote")}
      </Text>
      <Textarea label={t("note")} maxLength={500} value={note} onChange={(e) => setNote(e.currentTarget.value)} />
      {refusal && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bank.couldNotAct")}>
          {refusal}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button disabled={blocked || apply.isPending} loading={apply.isPending} onClick={() => apply.mutate()}>
          {t("bank.apply.submit")}
        </Button>
      </Group>
    </Stack>
  );
};
