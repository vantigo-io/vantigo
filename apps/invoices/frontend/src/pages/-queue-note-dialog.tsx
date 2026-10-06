import { Alert, Button, Group, Modal, Stack, Text, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { type BankTransaction, confirmDuplicate, dismissTransaction } from "../api/bank";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface QueueNoteDialogProps {
  line: BankTransaction;
  /** Dismiss as not a customer payment (a note required), or confirm a duplicate (a note optional). */
  kind: "dismiss" | "confirm";
  onClose: () => void;
}

/**
 * The two queue actions that take a note alone (D5): "Not a customer payment"
 * — a Vipps payout, a refund made outside Vantigo, another system's KID — with
 * the note required, and "Confirm duplicate", with the note optional. A
 * refusal — the line dealt with meanwhile, an action not for this line — is
 * said in words, and the dialog stays open.
 */
export const QueueNoteDialog = ({ line, kind, onClose }: QueueNoteDialogProps) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [note, setNote] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [refusal, setRefusal] = useState<string | null>(null);
  const submit = useMutation({
    mutationFn: () =>
      kind === "dismiss" ? dismissTransaction(line.id, note.trim()) : confirmDuplicate(line.id, note.trim()),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({
        color: "green",
        message: t(kind === "dismiss" ? "bank.done.dismissed" : "bank.done.confirmed"),
      });
      onClose();
    },
    onError: (failure) => {
      if (failure instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(failure, t, (field) => field === "note", kind);
        setError(onInputs.note ?? null);
        setRefusal(elsewhere.length > 0 ? elsewhere.join(" ") : null);
        return;
      }
      setRefusal(refusalMessage(failure, t, date, String, dateTime));
    },
  });
  return (
    <Modal
      opened
      onClose={onClose}
      title={t(kind === "dismiss" ? "bank.dismiss.title" : "bank.confirm.title", { ref: line.lineRef })}
    >
      <Stack>
        <Text size="sm">{t(kind === "dismiss" ? "bank.dismiss.explain" : "bank.confirm.explain")}</Text>
        <Textarea
          label={t("note")}
          required={kind === "dismiss"}
          maxLength={500}
          value={note}
          error={error ?? undefined}
          onChange={(e) => {
            setNote(e.currentTarget.value);
            setError(null);
            setRefusal(null);
          }}
        />
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
            disabled={submit.isPending || (kind === "dismiss" && !note.trim())}
            loading={submit.isPending}
            onClick={() => submit.mutate()}
          >
            {t(kind === "dismiss" ? "bank.dismiss.submit" : "bank.confirm.submit")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
