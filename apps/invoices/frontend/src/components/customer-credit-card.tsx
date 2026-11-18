import { Alert, Button, Group, Modal, NumberInput, Stack, Table, Text, Textarea, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type CreditMovement,
  type CreditRefund,
  customerCreditQueryOptions,
  recordRefund,
  removeRefund,
} from "../api/credit";
import { invoicesMetaQueryOptions } from "../api/meta";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { accountNumber } from "../lib/bank";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { DocumentLink } from "./document-link";

export interface CustomerCreditCardProps {
  customerId: number;
  /** Whether the caller holds `invoices:payments`, which recording and removing a refund needs. */
  canAct: boolean;
}

/** The fields of a refund, each of which has an input here. */
const refundInputs = new Set(["refundedOn", "amount", "amountNok", "bankAccount", "reference", "note"]);

/**
 * The customer's credit (invoices phase 6 design I8, I12) on the customer's
 * Invoices tab: the balance in each currency, every movement — an
 * overpayment kept, a credit note's surplus, credit used on an invoice, a
 * refund, a reversal — with what it came from, and the refunds. A caller
 * with `invoices:payments` records money paid back outside Vantigo
 * (**Record refund**) and removes a refund with a reason. A customer with no
 * credit ever shows nothing.
 */
export const CustomerCreditCard = ({ customerId, canAct }: CustomerCreditCardProps) => {
  const { t, money, date } = useInvoiceFormat();
  const credit = useQuery(customerCreditQueryOptions(customerId));
  // The currency a refund is being recorded in, null for none (invoices
  // phase 6 plan reading 9: credit in another currency is refunded too).
  const [recording, setRecording] = useState<string | null>(null);
  const [removing, setRemoving] = useState<CreditRefund | null>(null);
  if (credit.isPending) return <ContentSkeleton rows={2} rowHeight={36} />;
  if (credit.isError) {
    return (
      <Alert mt="md" color="red" icon={<IconAlertCircle size={16} />} title={t("credit.couldNotLoad")}>
        {refusalMessage(credit.error, t, date)}
      </Alert>
    );
  }
  const data = credit.data;
  if (data.movements.length === 0 && data.refunds.length === 0) return null;
  const refundable = data.balances.filter((b) => b.balance > 0);
  return (
    <PageSection mt="md" data-testid="customer-credit-card" data-assist="invoices.customer.credit">
      <Stack gap="sm">
        <Group justify="space-between">
          <Text fw={600} component="h3">
            {t("credit.title")}
          </Text>
          {canAct && (
            <Group gap="xs">
              {refundable.map((b) => (
                <Button key={b.currency} size="xs" variant="default" onClick={() => setRecording(b.currency)}>
                  {refundable.length > 1 || b.currency !== "NOK"
                    ? t("credit.recordRefundIn", { currency: b.currency })
                    : t("credit.recordRefund")}
                </Button>
              ))}
            </Group>
          )}
        </Group>
        {data.balances.map((b) => (
          <Text key={b.currency} size="sm" data-testid={`credit-balance-${b.currency}`}>
            {t("credit.balanceIs", { balance: money(b.balance, b.currency) })}
          </Text>
        ))}
        <Table verticalSpacing={4}>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("credit.col.day")}</Table.Th>
              <Table.Th>{t("credit.col.movement")}</Table.Th>
              <Table.Th ta="right">{t("credit.col.amount")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {data.movements.map((m) => (
              <Table.Tr key={m.id} data-testid={`credit-movement-${m.id}`}>
                <Table.Td>{date(m.creditDay)}</Table.Td>
                <Table.Td>
                  <MovementWords movement={m} />
                  {m.note && (
                    <Text size="xs" c="dimmed">
                      {m.note}
                    </Text>
                  )}
                </Table.Td>
                <Table.Td ta="right">{money(m.amount, m.currency)}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
        {data.refunds.length > 0 && (
          <Stack gap={4}>
            <Text size="sm" fw={600}>
              {t("credit.refunds")}
            </Text>
            {data.refunds.map((r) => (
              <Group key={r.id} justify="space-between" data-testid={`credit-refund-${r.id}`}>
                <Text
                  size="sm"
                  style={r.removedAt ? { textDecoration: "line-through" } : undefined}
                  c={r.removedAt ? "dimmed" : undefined}
                >
                  {r.bankAccount
                    ? t("credit.refundLine", {
                        date: date(r.refundedOn),
                        amount: money(r.amount, r.currency),
                        account: accountNumber(r.bankAccount),
                      })
                    : t("credit.refundLineNoAccount", {
                        date: date(r.refundedOn),
                        amount: money(r.amount, r.currency),
                      })}
                  {r.removalReason && ` — ${t("credit.refundRemoved", { reason: r.removalReason })}`}
                </Text>
                {canAct && !r.removedAt && (
                  <Button size="compact-xs" variant="subtle" color="red" onClick={() => setRemoving(r)}>
                    {t("credit.removeRefund")}
                  </Button>
                )}
              </Group>
            ))}
          </Stack>
        )}
      </Stack>
      {recording && (
        <RefundModal
          customerId={customerId}
          currency={recording}
          balance={data.balances.find((b) => b.currency === recording)?.balance ?? 0}
          onClose={() => setRecording(null)}
        />
      )}
      {removing && <RemoveRefundModal customerId={customerId} refund={removing} onClose={() => setRemoving(null)} />}
    </PageSection>
  );
};

/** One movement in words, with what it came from linked. */
const MovementWords = ({ movement: m }: { movement: CreditMovement }) => {
  const { t } = useInvoiceFormat();
  const words = t(`credit.kind.${m.kind}`, {
    invoice: m.invoiceNumber ?? m.invoiceId ?? "",
    creditNote: m.documentNumber ?? m.documentId ?? "",
    line: m.lineRef ?? "",
  });
  return (
    <Text size="sm">
      {m.invoiceId !== undefined ? <DocumentLink invoiceId={m.invoiceId}>{words}</DocumentLink> : words}
      {m.reversed !== 0 && ` (${t("credit.reversedPart")})`}
    </Text>
  );
};

/** Record refund (I12): money paid back outside Vantigo, from one of the company's accounts. */
const RefundModal = ({
  customerId,
  currency,
  balance,
  onClose,
}: {
  customerId: number;
  currency: string;
  balance: number;
  onClose: () => void;
}) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const meta = useQuery(invoicesMetaQueryOptions());
  const today = meta.data?.today ?? null;
  const [refundedOn, setRefundedOn] = useState<string | null>(today);
  const [amount, setAmount] = useState<number | string>(balance);
  const [bankAccount, setBankAccount] = useState("");
  const [amountNok, setAmountNok] = useState<number | string>("");
  const [reference, setReference] = useState("");
  const [note, setNote] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const record = useMutation({
    mutationFn: () =>
      recordRefund(customerId, {
        refundedOn: refundedOn ?? today ?? "",
        amount: typeof amount === "number" ? amount : Number(amount),
        currency,
        ...(currency !== "NOK" ? { amountNok: Number(amountNok) } : {}),
        bankAccount: bankAccount.replace(/[\s.]/g, ""),
        ...(reference.trim() ? { reference: reference.trim() } : {}),
        ...(note.trim() ? { note: note.trim() } : {}),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("credit.refundRecorded") });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(error, t, (f) => refundInputs.has(f), "refund");
        setErrors(onInputs);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("credit.couldNotRefund"), message: elsewhere.join(" ") });
        }
        return;
      }
      notifications.show({
        color: "red",
        title: t("credit.couldNotRefund"),
        message: refusalMessage(error, t, date, (value) => money(value, "NOK")),
      });
    },
  });
  const field = (name: string) => ({ error: errors[name] });
  return (
    <Modal opened onClose={onClose} title={t("credit.recordRefund")}>
      <Stack>
        <Text size="sm">{t("credit.refundExplain")}</Text>
        <DateInput
          label={t("credit.refundedOn")}
          required
          valueFormat={t("dateInputFormat")}
          maxDate={today ?? undefined}
          value={refundedOn}
          onChange={setRefundedOn}
          {...field("refundedOn")}
        />
        <NumberInput
          label={t("paymentAmount")}
          required
          min={0}
          decimalScale={2}
          hideControls
          rightSection={currency}
          rightSectionWidth={48}
          value={amount}
          onChange={setAmount}
          {...field("amount")}
        />
        {currency !== "NOK" && (
          <NumberInput
            label={t("credit.leftInNok")}
            required
            min={0}
            decimalScale={2}
            hideControls
            rightSection="NOK"
            rightSectionWidth={48}
            value={amountNok}
            onChange={setAmountNok}
            {...field("amountNok")}
          />
        )}
        <TextInput
          label={t("credit.bankAccount")}
          description={t("credit.bankAccountHint")}
          required
          value={bankAccount}
          onChange={(e) => setBankAccount(e.currentTarget.value)}
          {...field("bankAccount")}
        />
        <TextInput
          label={t("paymentReference")}
          maxLength={100}
          value={reference}
          onChange={(e) => setReference(e.currentTarget.value)}
          {...field("reference")}
        />
        <Textarea
          label={t("note")}
          maxLength={500}
          value={note}
          onChange={(e) => setNote(e.currentTarget.value)}
          {...field("note")}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            disabled={record.isPending || !refundedOn || bankAccount.trim() === ""}
            loading={record.isPending}
            onClick={() => record.mutate()}
          >
            {t("credit.recordRefund")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** Removes a refund with a reason; the credit comes back. */
const RemoveRefundModal = ({
  customerId,
  refund,
  onClose,
}: {
  customerId: number;
  refund: CreditRefund;
  onClose: () => void;
}) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState("");
  const remove = useMutation({
    mutationFn: () => removeRefund(customerId, refund.id, reason.trim()),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("credit.refundRemovedDone") });
      onClose();
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("credit.couldNotRemoveRefund"),
        message: refusalMessage(error, t, date),
      }),
  });
  return (
    <Modal opened onClose={onClose} title={t("credit.removeRefund")}>
      <Stack>
        <Text size="sm">
          {date(refund.refundedOn)} · {money(refund.amount, refund.currency)}
        </Text>
        <Text size="sm">{t("credit.removeRefundHint")}</Text>
        <Textarea
          label={t("removalReason")}
          required
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.currentTarget.value)}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            color="red"
            disabled={remove.isPending || reason.trim() === ""}
            loading={remove.isPending}
            onClick={() => remove.mutate()}
          >
            {t("credit.removeRefund")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
