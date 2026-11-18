import { Alert, Button, Group, Modal, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { sendEfaktura } from "../api/efaktura";
import { isSessionExpired } from "../api/export";
import type { InvoiceDocument } from "../api/invoices";
import { INVOICES_QUERY_KEY, NotFoundError } from "../api/request";
import { refusalCode, refusalMessage, refusalProblem } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface SendEfakturaDialogProps {
  document: InvoiceDocument;
  onClose: () => void;
}

/**
 * Sends an issued invoice as eFaktura (invoices phase 6 design I31, I33): to
 * the customer's online bank, and with AvtaleGiro when the customer has a
 * mandate. A credit note is never sent this way — the dialog says to send it
 * by e-mail or post. On 200 the notification says whether AvtaleGiro went
 * too, and why not; every refusal stays in the dialog in words.
 */
export const SendEfakturaDialog = ({ document: doc, onClose }: SendEfakturaDialogProps) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [refused, setRefused] = useState<string | null>(null);
  const creditNote = doc.kind === "credit_note";
  const send = useMutation({
    mutationFn: () => sendEfaktura(doc.id),
    onMutate: () => setRefused(null),
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      const avtalegiro = result.avtalegiro.included
        ? t("efaktura.withAvtaleGiro")
        : result.avtalegiro.reason && result.avtalegiro.reason !== "avtalegiro_off"
          ? t(`efaktura.reason.${result.avtalegiro.reason}`)
          : "";
      notifications.show({ color: "green", message: [t("efaktura.queued"), avtalegiro].filter(Boolean).join(" ") });
      onClose();
    },
    onError: (error) => {
      if (isSessionExpired(error)) return;
      if (error instanceof NotFoundError) {
        setRefused(t("documentNotFound"));
        return;
      }
      const message = refusalMessage(error, t, date);
      const reason = (refusalProblem(error) as { efakturaReason?: string }).efakturaReason;
      setRefused(
        refusalCode(error) === "efaktura_not_found" && reason
          ? `${message} ${t(`efaktura.notFound.${reason}`)}`
          : message,
      );
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("efaktura.send")}>
      <Stack>
        {creditNote ? (
          <Alert color="gray" icon={<IconInfoCircle size={16} />} role="note">
            {t("efaktura.creditNote")}
          </Alert>
        ) : (
          <>
            <Text size="sm">
              {t("sendEhfDocument", {
                document: t("documentNumbered", { kind: t("kindInvoice"), number: doc.number ?? "" }),
                amount: money(doc.grossTotal, doc.currency),
              })}
            </Text>
            <Alert color="blue" icon={<IconInfoCircle size={16} />} role="note">
              {t("efaktura.explanation")}
            </Alert>
          </>
        )}
        {refused && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("efaktura.couldNotSend")}>
            <Text size="sm">{refused}</Text>
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          {!creditNote && (
            <Button disabled={send.isPending} loading={send.isPending} onClick={() => send.mutate()}>
              {t("efaktura.send")}
            </Button>
          )}
        </Group>
      </Stack>
    </Modal>
  );
};
