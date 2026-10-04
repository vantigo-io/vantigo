import { Alert, Button, Group, List, Modal, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { sendEhf } from "../api/ehf";
import { isSessionExpired } from "../api/export";
import type { InvoiceDocument } from "../api/invoices";
import { INVOICES_QUERY_KEY, NotFoundError } from "../api/request";
import { invoicesCatalog } from "../i18n";
import { refusalCode, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface SendEhfDialogProps {
  document: InvoiceDocument;
  onClose: () => void;
}

/** What a refusal of the send names beside its code (EHF and KID design D8). */
interface Refused {
  message: string;
  details: string[];
  rules: string[];
}

/** The rules an `ehf_invalid` names: the module's own in words, an official one by its id. */
const ruleWords = (id: string, t: (key: string) => string) =>
  `ehfRule.${id}` in invoicesCatalog.en ? t(`ehfRule.${id}`) : id;

/**
 * Sends an issued document as EHF (EHF and KID design D8, D15): the dialog
 * names the receiver's Peppol id and the document, says the network will be
 * asked whether the receiver accepts this kind of document, and notes when the
 * document already went by e-mail. On 200 "Queued for sending as EHF" and the
 * document is read again; every refusal stays in the dialog in the reader's
 * language — `peppol_not_receivable` with what the network answered,
 * `ehf_invalid` with the rules it breaks, the 502 and 503s by their codes.
 */
export const SendEhfDialog = ({ document: doc, onClose }: SendEhfDialogProps) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [refused, setRefused] = useState<Refused | null>(null);
  const send = useMutation({
    mutationFn: () => sendEhf(doc.id),
    onMutate: () => setRefused(null),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("sendEhfQueued") });
      onClose();
    },
    onError: (error) => {
      // An expired session has signed the person out already: nothing to say here.
      if (isSessionExpired(error)) return;
      if (error instanceof NotFoundError) {
        setRefused({ message: t("documentNotFound"), details: [], rules: [] });
        return;
      }
      const problem = ((error as { problem?: Record<string, unknown> }).problem ?? {}) as {
        peppolRegistered?: boolean;
        peppolCanReceive?: boolean;
        rules?: { id: string }[];
      };
      const details: string[] = [];
      if (refusalCode(error) === "peppol_not_receivable") {
        if (problem.peppolRegistered === false) details.push(t("peppolNotRegistered"));
        else if (problem.peppolCanReceive === false) details.push(t("peppolCannotReceive"));
      }
      const rules = refusalCode(error) === "ehf_invalid" ? (problem.rules ?? []).map((r) => ruleWords(r.id, t)) : [];
      setRefused({ message: refusalMessage(error, t, date), details, rules });
    },
  });
  const kind = doc.kind === "credit_note" ? t("kindCreditNote") : t("kindInvoice");
  const heading = t("documentNumbered", { kind, number: doc.number ?? "" });
  return (
    <Modal opened onClose={onClose} title={t("sendAsEhf")}>
      <Stack>
        <Stack gap={2}>
          <Text fw={600}>{t("sendEhfReceiver", { peppolId: doc.buyer?.peppolId ?? "" })}</Text>
          <Text size="sm">
            {t("sendEhfDocument", { document: heading, amount: money(doc.grossTotal, doc.currency) })}
          </Text>
        </Stack>
        <Alert color="blue" icon={<IconInfoCircle size={16} />} role="note">
          {t("sendEhfExplanation")}
        </Alert>
        {(doc.deliveries ?? []).length > 0 && (
          <Alert color="gray" icon={<IconInfoCircle size={16} />} role="note" data-testid="cross-channel-note">
            {t("alreadySentByEmail")}
          </Alert>
        )}
        {refused && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSendEhf")}>
            <Stack gap={4}>
              <Text size="sm">{refused.message}</Text>
              {refused.details.map((d) => (
                <Text key={d} size="sm">
                  {d}
                </Text>
              ))}
              {refused.rules.length > 0 && (
                <>
                  <Text size="sm">{t("ehfRulesBroken")}</Text>
                  <List size="sm">
                    {refused.rules.map((rule) => (
                      <List.Item key={rule}>{rule}</List.Item>
                    ))}
                  </List>
                </>
              )}
            </Stack>
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button disabled={send.isPending} loading={send.isPending} onClick={() => send.mutate()}>
            {t("sendAsEhf")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
