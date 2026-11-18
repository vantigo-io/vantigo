import { Alert, Button, Group, Modal, Stack, Text, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { BankTransaction } from "../api/bank";
import { keepAsCredit } from "../api/credit";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface KeepAsCreditDialogProps {
  /** An exception queued `exceeds_open` or `invoice_settled`. */
  line: BankTransaction;
  /** The installation's currency, meta's. */
  currency: string;
  onClose: () => void;
}

/**
 * Keep the rest as credit (invoices phase 6 design I9): what the line's
 * invoice — the one its KID names — has open is applied as a payment, and
 * the rest of the line becomes the invoice's customer's credit, which can be
 * applied to a later invoice or refunded. An optional note goes with it.
 */
export const KeepAsCreditDialog = ({ line, currency, onClose }: KeepAsCreditDialogProps) => {
  const { t, money, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [note, setNote] = useState("");
  const [refusal, setRefusal] = useState<string | null>(null);
  const amount = (value: number) => money(value, currency);
  const keep = useMutation({
    mutationFn: () => keepAsCredit(line.id, note.trim() ? { note: note.trim() } : {}),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("bank.done.keep") });
      onClose();
    },
    onError: (error) =>
      setRefusal(
        error instanceof ApiValidationError
          ? fieldRefusals(error, t, () => false, "keep").elsewhere.join(" ")
          : refusalMessage(error, t, date, amount, dateTime),
      ),
  });
  return (
    <Modal opened onClose={onClose} title={t("bank.keep.title", { ref: line.lineRef })}>
      <Stack>
        <Text size="sm">{t("bank.keep.explain", { amount: amount(line.unappliedAmount || line.amount) })}</Text>
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
          <Button
            disabled={keep.isPending}
            loading={keep.isPending}
            onClick={() => keep.mutate()}
            data-assist="invoices.queue.keep-as-credit"
          >
            {t("bank.action.keep")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
