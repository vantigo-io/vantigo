import { Button, Group, Modal, NumberInput, Stack, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { applyCredit } from "../api/credit";
import type { InvoiceDocument } from "../api/invoices";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface ApplyCreditModalProps {
  invoice: InvoiceDocument;
  /** The customer's credit in the invoice's currency. */
  balance: number;
  /** Today in Oslo, meta's. */
  today: string;
  onClose: () => void;
}

/**
 * Applies the customer's credit to an issued invoice (invoices phase 6
 * design I11): the amount — the lesser of the credit and what is open unless
 * the person changes it — and the day, today. The server registers it as a
 * payment of source credit and judges the credit and the open amount under
 * the customer's lock; every refusal is said in words.
 */
export const ApplyCreditModal = ({ invoice, balance, today, onClose }: ApplyCreditModalProps) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [amount, setAmount] = useState<number | string>(Math.min(balance, Math.max(invoice.openAmount ?? 0, 0)));
  const [paidOn, setPaidOn] = useState<string | null>(today);
  // One key per amount and day (B-1 review m4): a retry of the same apply —
  // a second click, a lost answer — applies once; a changed one is a new key.
  const [idempotencyKey, setIdempotencyKey] = useState(() => crypto.randomUUID());
  const [errors, setErrors] = useState<Record<string, string>>({});
  const apply = useMutation({
    mutationFn: () =>
      applyCredit(
        invoice.id,
        {
          amount: typeof amount === "number" ? amount : Number(amount),
          ...(paidOn ? { paidOn } : {}),
        },
        idempotencyKey,
      ),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("credit.applied") });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(error, t, (f) => f === "amount" || f === "paidOn", "credit");
        setErrors(onInputs);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("credit.couldNotApply"), message: elsewhere.join(" ") });
        }
        return;
      }
      notifications.show({
        color: "red",
        title: t("credit.couldNotApply"),
        message: refusalMessage(error, t, date, (value) => money(value, invoice.currency)),
      });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("credit.applyTitle")}>
      <Stack>
        <Text size="sm">{t("credit.applyExplain", { balance: money(balance, invoice.currency) })}</Text>
        <NumberInput
          label={t("paymentAmount")}
          required
          min={0}
          decimalScale={2}
          hideControls
          rightSection={invoice.currency}
          rightSectionWidth={48}
          value={amount}
          error={errors.amount}
          onChange={(v) => {
            setAmount(v);
            setIdempotencyKey(crypto.randomUUID());
            setErrors({});
          }}
        />
        <DateInput
          label={t("credit.appliedOn")}
          valueFormat={t("dateInputFormat")}
          minDate={invoice.issueDate}
          maxDate={today}
          value={paidOn}
          error={errors.paidOn}
          onChange={(d) => {
            setPaidOn(d);
            setIdempotencyKey(crypto.randomUUID());
            setErrors({});
          }}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button disabled={apply.isPending} loading={apply.isPending} onClick={() => apply.mutate()}>
            {t("credit.use")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
