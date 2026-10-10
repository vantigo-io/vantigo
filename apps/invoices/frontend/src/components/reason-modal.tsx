import { Alert, Button, Group, Modal, Stack, Text, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface ReasonModalProps {
  title: string;
  /** What is being acted on, in a line. */
  summary: string;
  /** What the action does and that it is not undone. */
  hint: string;
  /** The confirm button's words, which are also red. */
  submitLabel: string;
  /** The notification after it is done. */
  doneMessage: string;
  /** The notification's title when it is refused. */
  errorTitle: string;
  /** Sends the reason, trimmed. */
  onSubmit: (reason: string) => Promise<unknown>;
  onClose: () => void;
}

/**
 * A record removed or a letter withdrawn with a reason of at most 200
 * characters — a charge payment, a delivery recorded by hand, a reminder
 * letter. The button waits for a reason and is disabled while the request is
 * on its way; every refusal is said in the reader's language, and on success
 * every invoices query is read again.
 */
export const ReasonModal = ({
  title,
  summary,
  hint,
  submitLabel,
  doneMessage,
  errorTitle,
  onSubmit,
  onClose,
}: ReasonModalProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | undefined>();
  const act = useMutation({
    mutationFn: () => onSubmit(reason.trim()),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: doneMessage });
      onClose();
    },
    onError: (refusal) => {
      if (refusal instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(refusal, t, (field) => field === "reason", "payment");
        setError(onInputs.reason);
        if (elsewhere.length > 0) notifications.show({ color: "red", title: errorTitle, message: elsewhere.join(" ") });
        return;
      }
      notifications.show({ color: "red", title: errorTitle, message: refusalMessage(refusal, t, date) });
    },
  });
  return (
    <Modal opened onClose={onClose} title={title}>
      <Stack>
        <Text size="sm">{summary}</Text>
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
          {hint}
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
            disabled={act.isPending || reason.trim() === ""}
            loading={act.isPending}
            onClick={() => act.mutate()}
          >
            {submitLabel}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
