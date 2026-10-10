import { Button, Group, Modal, Stack, Text, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { type LetterLeft, placeHold } from "../api/collection";
import type { InvoiceDocument } from "../api/invoices";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface HoldModalProps {
  invoice: InvoiceDocument;
  /** Called with the letters the hold did not withdraw, so the page can name them. */
  onLettersLeft: (letters: LetterLeft[]) => void;
  onClose: () => void;
}

/**
 * Puts an invoice the customer disputes on hold (invoices payments and
 * reminders design D11), with what is disputed. While it is held no letter
 * goes, the late interest runs on, and payments are still registered; the
 * letters not yet gone are withdrawn, but a printed one — perhaps in the post
 * already — and one being sent are left, and named.
 */
export const HoldModal = ({ invoice, onLettersLeft, onClose }: HoldModalProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [note, setNote] = useState("");
  const [error, setError] = useState<string | undefined>();
  const hold = useMutation({
    mutationFn: () => placeHold(invoice.id, note.trim()),
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("hold.placed") });
      onLettersLeft(result.lettersLeft);
      onClose();
    },
    onError: (refusal) => {
      if (refusal instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(refusal, t, (field) => field === "note", "hold");
        setError(onInputs.note);
        if (elsewhere.length > 0)
          notifications.show({ color: "red", title: t("hold.couldNotPlace"), message: elsewhere.join(" ") });
        return;
      }
      notifications.show({ color: "red", title: t("hold.couldNotPlace"), message: refusalMessage(refusal, t, date) });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("hold.place")}>
      <Stack>
        <Text size="sm">{t("hold.placeHint")}</Text>
        <Textarea
          label={t("hold.note")}
          required
          maxLength={500}
          value={note}
          error={error}
          onChange={(e) => {
            setNote(e.currentTarget.value);
            setError(undefined);
          }}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            disabled={hold.isPending || note.trim() === ""}
            loading={hold.isPending}
            onClick={() => hold.mutate()}
          >
            {t("hold.place")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
