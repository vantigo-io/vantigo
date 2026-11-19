import { Button, type ButtonProps, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconSend } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { INVOICES_QUERY_KEY } from "../api/request";
import { postSendBatch, type SendBatchInput } from "../api/send-batches";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { sendBatchLinkOptions } from "../lib/routes";

export interface SendAllButtonProps extends ButtonProps {
  /** The documents chosen, or the billing run whose documents are sent. */
  input: SendBatchInput;
  /** How many documents the confirmation names. */
  count: number;
  /** How many of them are drafts, which are listed as skipped. */
  drafts?: number;
  /** Called once the batch is made, before its page opens. */
  onSent?: () => void;
}

/**
 * **Send all** (invoices phase 6 design I36, plan E6): a confirmation that
 * names how many documents go and how the channel is chosen — no preview per
 * document (reading E6), since the channel is chosen when each is sent — then
 * the batch, and its page, where each document's outcome shows as it lands.
 * The caller marks it with its tour target (`data-assist`), passed through.
 */
export const SendAllButton = ({ input, count, drafts = 0, onSent, ...button }: SendAllButtonProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const send = useMutation({
    mutationFn: () => postSendBatch(input),
    onSuccess: async (batch) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      onSent?.();
      navigate(sendBatchLinkOptions(batch.batch.id));
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("sendAll.couldNotSend"), message: refusalMessage(error, t, date) }),
  });
  const confirm = () =>
    modals.openConfirmModal({
      title: t("sendAll.confirmTitle", { count }),
      children: (
        <Stack gap="xs">
          <Text size="sm">{t("sendAll.confirmBody")}</Text>
          {drafts > 0 && (
            <Text size="sm" c="dimmed">
              {t("sendAll.draftsSkipped", { count: drafts })}
            </Text>
          )}
        </Stack>
      ),
      labels: { confirm: t("sendAll.confirm", { count }), cancel: t("cancel") },
      onConfirm: () => send.mutate(),
    });
  return (
    <Button leftSection={<IconSend size={16} />} loading={send.isPending} onClick={confirm} {...button}>
      {t("sendAll.button")}
    </Button>
  );
};
