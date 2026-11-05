import { Alert, Button, Group, Modal, Stack, TagsInput, Textarea, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { IconSend } from "@tabler/icons-react";
import { useState } from "react";
import type { SendInput } from "../api/types";
import { useOrdersFormat } from "../lib/format";

export interface SendDialogProps {
  kind: "quote" | "order";
  /** The document's default recipient: the contact's e-mail, else the customer's invoice e-mail (O25). */
  defaultRecipient?: string | null;
  /** A quote's valid-until, required on send (O20); the page offers its default. */
  validUntil?: string | null;
  loading?: boolean;
  error?: string;
  onSend: (input: Omit<SendInput, "revision" | "version">) => void;
  onClose: () => void;
}

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/**
 * The send dialog (O25): the recipient, which defaults to the contact's
 * e-mail, else the customer's invoice e-mail, up to ten CC addresses and a message; for a quote, the date it is
 * valid until, which the send requires (O20).
 */
export const SendDialog = ({
  kind,
  defaultRecipient,
  validUntil: initialValidUntil,
  loading,
  error,
  onSend,
  onClose,
}: SendDialogProps) => {
  const { t } = useOrdersFormat();
  const [recipient, setRecipient] = useState(defaultRecipient ?? "");
  const [cc, setCc] = useState<string[]>([]);
  const [message, setMessage] = useState("");
  const [validUntil, setValidUntil] = useState<string | null>(initialValidUntil ?? null);
  const badCc = cc.find((address) => !EMAIL.test(address));
  const ready = EMAIL.test(recipient.trim()) && !badCc && (kind === "order" || validUntil !== null);
  return (
    <Modal opened onClose={onClose} title={kind === "quote" ? t("sendQuoteTitle") : t("sendConfirmationTitle")}>
      <Stack>
        <TextInput
          label={t("recipient")}
          type="email"
          required
          value={recipient}
          onChange={(e) => setRecipient(e.currentTarget.value)}
          error={recipient && !EMAIL.test(recipient.trim()) ? t("emailInvalid") : undefined}
          data-autofocus
        />
        <TagsInput
          label={t("cc")}
          description={t("ccHint")}
          value={cc}
          onChange={(next) => setCc(next.slice(0, 10))}
          maxTags={10}
          error={badCc ? t("emailInvalidNamed", { address: badCc }) : undefined}
        />
        {kind === "quote" && (
          <DateInput
            label={t("validUntil")}
            required
            valueFormat={t("dateInputFormat")}
            value={validUntil}
            onChange={(value) => setValidUntil(value ?? null)}
          />
        )}
        <Textarea
          label={t("message")}
          description={t("messageHint")}
          autosize
          minRows={3}
          maxLength={4000}
          value={message}
          onChange={(e) => setMessage(e.currentTarget.value)}
        />
        {error && (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancelDialog")}
          </Button>
          <Button
            leftSection={<IconSend size={16} />}
            loading={loading}
            disabled={!ready}
            onClick={() =>
              onSend({
                recipient: recipient.trim(),
                ...(cc.length > 0 ? { cc } : {}),
                ...(message.trim() ? { message: message.trim() } : {}),
                ...(kind === "quote" ? { validUntil } : {}),
              })
            }
          >
            {t("sendNow")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
