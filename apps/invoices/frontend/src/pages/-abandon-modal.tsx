import { Alert, Button, Checkbox, Group, Modal, Stack, Text, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { abandonAttempt, type PaymentAttempt } from "../api/attempts";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface AbandonModalProps {
  attempt: PaymentAttempt;
  currency: string;
  onClose: () => void;
}

/**
 * Abandons a stuck Vipps payment (invoices payments and reminders design D16,
 * `invoices:manage`): after the person has looked it up in the Vipps portal
 * by its reference. The button waits for a reason (1–500 characters) and the
 * confirmation that nothing was captured; the server reads Vipps first, and a
 * refusal — Vipps still holds it, or shows money captured — is said in words
 * and leaves the dialog open.
 */
export const AbandonModal = ({ attempt, currency, onClose }: AbandonModalProps) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [refused, setRefused] = useState<string | null>(null);
  const unsetError = (field: string) =>
    setErrors((current) => {
      const next = { ...current };
      delete next[field];
      return next;
    });
  const abandon = useMutation({
    mutationFn: () => abandonAttempt(attempt.id, { reason: reason.trim(), confirmNotCaptured: confirmed }),
    onMutate: () => setRefused(null),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("abandon.done") });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(
          error,
          t,
          (field) => field === "reason" || field === "confirmNotCaptured",
          "abandon",
        );
        setErrors(onInputs);
        if (elsewhere.length > 0) setRefused(elsewhere.join(" "));
        return;
      }
      setRefused(refusalMessage(error, t, date));
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("abandon.title")}>
      <Stack>
        <Text size="sm">
          {attempt.reference} · {money(attempt.amount, currency)} · {t(`attempt.state.${attempt.state}`)}
        </Text>
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
          {t("abandon.hint")}
        </Alert>
        <Textarea
          label={t("abandon.reason")}
          required
          maxLength={500}
          value={reason}
          error={errors.reason}
          onChange={(e) => {
            setReason(e.currentTarget.value);
            unsetError("reason");
          }}
        />
        <Checkbox
          label={t("abandon.confirmNotCaptured")}
          checked={confirmed}
          error={errors.confirmNotCaptured}
          onChange={(e) => {
            setConfirmed(e.currentTarget.checked);
            unsetError("confirmNotCaptured");
          }}
        />
        {refused && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("abandon.couldNot")}>
            {refused}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            color="red"
            disabled={abandon.isPending || reason.trim() === "" || !confirmed}
            loading={abandon.isPending}
            onClick={() => abandon.mutate()}
          >
            {t("abandon.submit")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
