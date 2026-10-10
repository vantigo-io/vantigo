import { Button, Group, Modal, NumberInput, Stack, Text, Textarea, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { registerChargePayment } from "../api/charges";
import type { InvoiceDocument } from "../api/invoices";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface ChargePaymentModalProps {
  invoice: InvoiceDocument;
  /** Today in Oslo, meta's: the day prefilled and the latest a payment may be. */
  today: string;
  onClose: () => void;
}

/** The fields of a charge payment, each of which has an input here. */
const chargePaymentInputs = new Set(["paidOn", "amount", "reference", "note"]);

/**
 * Registers a payment of the invoice's reminder charges (invoices payments and
 * reminders design D9): apart from the principal, never more than the charges
 * outstanding — the amount prefilled — and paying the fees and compensation
 * first, oldest letter first, then the interest. Every refusal in words, an
 * overpayment with the charges outstanding the server names.
 */
export const ChargePaymentModal = ({ invoice, today, onClose }: ChargePaymentModalProps) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [paidOn, setPaidOn] = useState<string | null>(today);
  const [amount, setAmount] = useState<number | string>(Math.max(invoice.charges?.outstanding ?? 0, 0));
  const [reference, setReference] = useState("");
  const [note, setNote] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const clear = (field: string) =>
    setErrors((current) => Object.fromEntries(Object.entries(current).filter(([f]) => f !== field)));
  const register = useMutation({
    mutationFn: () =>
      registerChargePayment(invoice.id, {
        paidOn: paidOn ?? "",
        amount: typeof amount === "number" ? amount : Number(amount),
        ...(reference.trim() ? { reference: reference.trim() } : {}),
        ...(note.trim() ? { note: note.trim() } : {}),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("charges.paymentRegistered") });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(
          error,
          t,
          (field) => chargePaymentInputs.has(field),
          "chargePayment",
        );
        setErrors(onInputs);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("charges.couldNotRegister"), message: elsewhere.join(" ") });
        }
        return;
      }
      notifications.show({
        color: "red",
        title: t("charges.couldNotRegister"),
        message: refusalMessage(error, t, date, (value) => money(value, invoice.currency)),
      });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("charges.registerPayment")}>
      <Stack>
        <Text size="sm">
          {t("charges.outstandingIs", { amount: money(invoice.charges?.outstanding ?? 0, invoice.currency) })}
        </Text>
        <Text size="xs" c="dimmed">
          {t("charges.paymentOrder")}
        </Text>
        <DateInput
          label={t("paidOn")}
          required
          valueFormat={t("dateInputFormat")}
          minDate={invoice.issueDate}
          maxDate={today}
          value={paidOn}
          error={errors.paidOn}
          onChange={(d) => {
            setPaidOn(d);
            clear("paidOn");
          }}
        />
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
            clear("amount");
          }}
        />
        <TextInput
          label={t("paymentReference")}
          maxLength={100}
          value={reference}
          error={errors.reference}
          onChange={(e) => {
            setReference(e.currentTarget.value);
            clear("reference");
          }}
        />
        <Textarea
          label={t("note")}
          maxLength={500}
          value={note}
          error={errors.note}
          onChange={(e) => {
            setNote(e.currentTarget.value);
            clear("note");
          }}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            disabled={register.isPending || !paidOn}
            loading={register.isPending}
            onClick={() => register.mutate()}
          >
            {t("register")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
