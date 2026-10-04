import { Alert, Button, Group, Modal, Stack, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { isSessionExpired } from "../api/export";
import type { InvoiceDocument } from "../api/invoices";
import { ApiValidationError, INVOICES_QUERY_KEY, NotFoundError } from "../api/request";
import { type InvoiceDelivery, type SendDefaults, sendInvoice } from "../api/send";
import { invoicesCatalog } from "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface SendDialogProps {
  document: InvoiceDocument;
  /**
   * The document's send defaults; absent when the directory could not be read
   * (the server's best effort), and the person then enters the address.
   */
  defaults?: SendDefaults;
  onClose: () => void;
}

/**
 * The warnings that cannot be missed (D4): the customer expects EHF — and,
 * where this caller can send it, it should go as EHF (`ehf_preferred`, EHF and
 * KID design D10) — or the buyer is a Norwegian business on or after
 * 2027-01-01, the day the server judges, never the browser. The rest are plain
 * notes. None refuses the send.
 */
const loud = new Set(["delivery_preference_ehf", "ehf_preferred", "buyer_norwegian_business_required"]);

/** The EHF states in which the document is on its way as EHF, or there (D10's cross-channel note). */
const ehfCarried = new Set(["queued", "submitted", "delivered", "unconfirmed"]);

/**
 * Sends an issued document by e-mail (D4, D10): the recipient prefilled with
 * the customer's invoice e-mail and editable — an edited one is sent as the
 * override, the customer's own is left to the server; without the send
 * defaults the field starts empty and a note says why — the send's warnings,
 * and on an invoice something has been paid on or credited against, what the
 * mail will say about payment. "Sent to …" on success, the address the
 * server logged; every refusal, the rate limit's included, in the reader's
 * language, and an expired session left to the host's sign-in. The button is
 * disabled while the mail is on its way, and while the field is empty when the
 * customer has an address — an emptied field must not fall back to it unseen.
 * A customer the server says is anonymised is offered no send at all: the
 * dialog says why, and nothing else.
 */
export const SendDialog = ({ document: doc, defaults, onClose }: SendDialogProps) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [recipient, setRecipient] = useState(defaults?.recipient ?? "");
  const [error, setError] = useState<string | undefined>();
  const chosen = recipient.trim();
  // The customer's own address is the server's default: only another one is an override.
  const override = chosen && chosen !== defaults?.recipient ? chosen : undefined;
  const anonymised = doc.customerAnonymised === true;
  const send = useMutation({
    mutationFn: () => sendInvoice(doc.id, override),
    onSuccess: async (sent) => {
      // Where it went is the server's log: the newest delivery, the row this
      // send wrote (ids only grow) — what was typed or prefilled only if the
      // answer names no address.
      const newest = (sent.deliveries ?? []).reduce<InvoiceDelivery | undefined>(
        (latest, d) => (latest === undefined || d.id > latest.id ? d : latest),
        undefined,
      );
      const to = newest?.recipient || (override ?? defaults?.recipient) || "";
      // Read again, as after every write, rather than set from the answer (design D4).
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("sentTo", { recipient: to }) });
      onClose();
    },
    onError: (refusal) => {
      // An expired session has signed the person out already: nothing to say here.
      if (isSessionExpired(refusal)) return;
      if (refusal instanceof NotFoundError) {
        notifications.show({ color: "red", title: t("couldNotSend"), message: t("documentNotFound") });
        return;
      }
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
  const preference = defaults?.preference ?? "";
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
  if (anonymised) {
    return (
      <Modal opened onClose={onClose} title={t("sendDocument")}>
        <Stack>
          <Alert color="gray" icon={<IconInfoCircle size={16} />} role="note">
            {t("refusal.customer_anonymised")}
          </Alert>
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
          </Group>
        </Stack>
      </Modal>
    );
  }
  return (
    <Modal opened onClose={onClose} title={t("sendDocument")}>
      <Stack>
        {doc.ehf && ehfCarried.has(doc.ehf.status) && (
          <Alert color="gray" icon={<IconInfoCircle size={16} />} role="note" data-testid="cross-channel-note">
            {t("alreadySentAsEhf")}
          </Alert>
        )}
        {!defaults && (
          <Alert color="gray" icon={<IconInfoCircle size={16} />} role="note" data-testid="send-defaults-unavailable">
            {t("sendDefaultsUnavailable")}
          </Alert>
        )}
        {(defaults?.warnings ?? []).map((warning) =>
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
          <Button
            disabled={send.isPending || (chosen === "" && defaults?.recipient !== undefined)}
            loading={send.isPending}
            onClick={() => send.mutate()}
          >
            {t("send")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
