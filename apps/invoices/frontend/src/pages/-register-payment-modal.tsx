import { Button, Group, Modal, NumberInput, Stack, Textarea, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { InvoiceDocument } from "../api/invoices";
import { registerPayment } from "../api/payments";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface RegisterPaymentModalProps {
  invoice: InvoiceDocument;
  /** Today in Oslo, meta's: the day a payment is prefilled with and the latest it may be. */
  today: string;
  onClose: () => void;
}

/** The fields of a registration, each of which has an input here. */
const paymentInputs = new Set(["paidOn", "amount", "reference", "note"]);

/**
 * Registers money received against an issued invoice (D2, D10): the day it
 * arrived — today, at the earliest the issue date — and the amount, the open
 * amount unless the person changes it, with a reference and a note. The
 * button is disabled while the request is on its way, so a double click never
 * registers twice; every refusal is said in the reader's language, an
 * overpayment with the open amount the server names.
 */
export const RegisterPaymentModal = ({ invoice, today, onClose }: RegisterPaymentModalProps) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [paidOn, setPaidOn] = useState<string | null>(today);
  const [amount, setAmount] = useState<number | string>(Math.max(invoice.openAmount ?? 0, 0));
  const [reference, setReference] = useState("");
  const [note, setNote] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const clear = (field: string) =>
    setErrors((current) => Object.fromEntries(Object.entries(current).filter(([f]) => f !== field)));
  const register = useMutation({
    mutationFn: () =>
      registerPayment(invoice.id, {
        paidOn: paidOn ?? "",
        amount: typeof amount === "number" ? amount : Number(amount),
        ...(reference.trim() ? { reference: reference.trim() } : {}),
        ...(note.trim() ? { note: note.trim() } : {}),
      }),
    onSuccess: async () => {
      // The answer carries no send defaults (design D4): the document is read again.
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("paymentRegistered") });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(error, t, (field) => paymentInputs.has(field), "payment");
        setErrors(onInputs);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("couldNotRegisterPayment"), message: elsewhere.join(" ") });
        }
        return;
      }
      notifications.show({
        color: "red",
        title: t("couldNotRegisterPayment"),
        message: refusalMessage(error, t, date, (value) => money(value, invoice.currency)),
      });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("registerPayment")}>
      <Stack>
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
