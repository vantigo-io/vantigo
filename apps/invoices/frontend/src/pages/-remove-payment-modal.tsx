import { Alert, Button, Group, Modal, Stack, Text, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { InvoiceDocument } from "../api/invoices";
import { type InvoicePayment, removePayment } from "../api/payments";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface RemovePaymentModalProps {
  invoice: InvoiceDocument;
  payment: InvoicePayment;
  onClose: () => void;
}

/**
 * Removes a registration with a reason (D2, D10). The registration stays on
 * the invoice, struck through with the reason, and a removal is never undone —
 * the dialog says so. The button waits for a reason and is disabled while the
 * request is on its way; a payment someone else removed meanwhile is
 * `payment_removed`, said in the reader's language.
 */
export const RemovePaymentModal = ({ invoice, payment, onClose }: RemovePaymentModalProps) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | undefined>();
  const remove = useMutation({
    mutationFn: () => removePayment(invoice.id, payment.id, reason.trim()),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("paymentRemoved") });
      onClose();
    },
    onError: (refusal) => {
      if (refusal instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(refusal, t, (field) => field === "reason", "payment");
        setError(onInputs.reason);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("couldNotRemovePayment"), message: elsewhere.join(" ") });
        }
        return;
      }
      notifications.show({
        color: "red",
        title: t("couldNotRemovePayment"),
        message: refusalMessage(refusal, t, date),
      });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("removePayment")}>
      <Stack>
        <Text size="sm">
          {date(payment.paidOn)} · {money(payment.amount, payment.currency)}
          {payment.reference ? ` · ${payment.reference}` : ""}
        </Text>
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
          {t("removePaymentHint")}
        </Alert>
        <Textarea
          label={t("removalReason")}
          required
          maxLength={200}
          value={reason}
          error={error}
          onChange={(e) => {
            setReason(e.currentTarget.value);
            setError(undefined);
          }}
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
            {t("removePayment")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
