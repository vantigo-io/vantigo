import { Alert, Button, Group, Modal, Stack, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { InvoiceDocument } from "../api/invoices";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import { type SendDefaults, sendInvoice } from "../api/send";
import { invoicesCatalog } from "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface SendDialogProps {
  document: InvoiceDocument;
  /** The document's send defaults, which the page offers Send only with. */
  defaults: SendDefaults;
  onClose: () => void;
}

/**
 * The warnings that cannot be missed (D4): the customer expects EHF, or the
 * buyer is a Norwegian business on or after 2027-01-01, the day the server
 * judges, never the browser. The rest are plain notes. None refuses the send.
 */
const loud = new Set(["delivery_preference_ehf", "buyer_norwegian_business_required"]);

/**
 * Sends an issued document by e-mail (D4, D10): the recipient prefilled with
 * the customer's invoice e-mail and editable — an edited one is sent as the
 * override, the customer's own is left to the server — the send's warnings,
 * and on an invoice something has been paid on or credited against, what the
 * mail will say about payment. "Sent to …" on success; every refusal, the
 * rate limit's included, in the reader's language. The button is disabled
 * while the mail is on its way.
 */
export const SendDialog = ({ document: doc, defaults, onClose }: SendDialogProps) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [recipient, setRecipient] = useState(defaults.recipient ?? "");
  const [error, setError] = useState<string | undefined>();
  const chosen = recipient.trim();
  // The customer's own address is the server's default: only another one is an override.
  const override = chosen && chosen !== defaults.recipient ? chosen : undefined;
  const send = useMutation({
    mutationFn: () => sendInvoice(doc.id, override),
    onSuccess: async () => {
      // Read again, as after every write, rather than set from the answer (reading 5b).
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("sentTo", { recipient: override ?? defaults.recipient }) });
      onClose();
    },
    onError: (refusal) => {
      if (refusal instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(refusal, t, (field) => field === "recipient", "send");
        setError(onInputs.recipient);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("couldNotSend"), message: elsewhere.join(" ") });
        }
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSend"), message: refusalMessage(refusal, t, date) });
    },
  });
  const preference = defaults.preference ?? "";
  const preferenceWords = `preference.${preference}` in invoicesCatalog.en ? t(`preference.${preference}`) : preference;
  const warningWords = (warning: string) => {
    const key = `sendWarning.${warning}`;
    return key in invoicesCatalog.en ? t(key, { preference: preferenceWords }) : t("warningUnknown", { code: warning });
  };
  // What the cover mail's payment paragraph will say (D4): only an invoice's
  // has one, and it follows the open amount at the send.
  const open = doc.kind === "invoice" ? doc.openAmount : undefined;
  const paymentNote =
    open === undefined
      ? undefined
      : open <= 0
        ? t("sendSettledNote")
        : open < doc.grossTotal
          ? t("sendPartlyNote", { open: money(open, doc.currency) })
          : undefined;
  return (
    <Modal opened onClose={onClose} title={t("sendDocument")}>
      <Stack>
        {defaults.warnings.map((warning) =>
          loud.has(warning) ? (
            <Alert
              key={warning}
              color="red"
              variant="filled"
              icon={<IconAlertTriangle size={16} />}
              title={`sendWarningTitle.${warning}` in invoicesCatalog.en ? t(`sendWarningTitle.${warning}`) : undefined}
              data-send-warning={warning}
              data-loud="true"
            >
              {warningWords(warning)}
            </Alert>
          ) : (
            <Alert
              key={warning}
              color="gray"
              icon={<IconInfoCircle size={16} />}
              role="note"
              data-send-warning={warning}
              data-loud="false"
            >
              {warningWords(warning)}
            </Alert>
          ),
        )}
        <TextInput
          label={t("recipient")}
          description={t("recipientHint")}
          type="email"
          maxLength={254}
          value={recipient}
          error={error}
          onChange={(e) => {
            setRecipient(e.currentTarget.value);
            setError(undefined);
          }}
        />
        {paymentNote && (
          <Alert color="blue" icon={<IconInfoCircle size={16} />} role="note">
            {paymentNote}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button disabled={send.isPending} loading={send.isPending} onClick={() => send.mutate()}>
            {t("send")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
