import { Alert, Button, Group, Modal, Radio, Stack, Text, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { type LetterLeft, liftHold } from "../api/collection";
import type { InvoiceDocument } from "../api/invoices";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface LiftModalProps {
  invoice: InvoiceDocument;
  onLettersLeft: (letters: LetterLeft[]) => void;
  onClose: () => void;
}

/**
 * Lifts a hold (invoices payments and reminders design D11), asking the one
 * question the law turns on: was the objection obviously groundless? The
 * answer starts at **no** — the objection had reasonable grounds — and then
 * every fee and compensation claimed on the invoice is waived (objection
 * upheld) and none is claimed on it again (inkassoloven § 17; the new act's
 * § 18). Only a person who answers yes keeps the charges. Late interest is
 * not a cost and runs on either way.
 */
export const LiftModal = ({ invoice, onLettersLeft, onClose }: LiftModalProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [groundless, setGroundless] = useState<"no" | "yes">("no");
  const [note, setNote] = useState("");
  const [error, setError] = useState<string | undefined>();
  const lift = useMutation({
    mutationFn: () =>
      liftHold(invoice.id, {
        chargesAllowed: groundless === "yes",
        ...(note.trim() ? { note: note.trim() } : {}),
      }),
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("hold.lifted") });
      onLettersLeft(result.lettersLeft);
      onClose();
    },
    onError: (refusal) => {
      if (refusal instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(refusal, t, (field) => field === "note", "hold");
        setError(onInputs.note);
        if (elsewhere.length > 0)
          notifications.show({ color: "red", title: t("hold.couldNotLift"), message: elsewhere.join(" ") });
        return;
      }
      notifications.show({ color: "red", title: t("hold.couldNotLift"), message: refusalMessage(refusal, t, date) });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("hold.lift")}>
      <Stack>
        <Radio.Group label={t("hold.groundless")} value={groundless} onChange={(v) => setGroundless(v as "no" | "yes")}>
          <Stack gap="xs" mt="xs">
            <Radio value="no" label={t("hold.groundlessNo")} />
            <Radio value="yes" label={t("hold.groundlessYes")} />
          </Stack>
        </Radio.Group>
        {groundless === "no" ? (
          <Alert color="yellow" icon={<IconAlertTriangle size={16} />} data-testid="lift-consequence">
            {t("hold.liftWaives")}
          </Alert>
        ) : (
          <Text size="sm" data-testid="lift-consequence">
            {t("hold.liftKeeps")}
          </Text>
        )}
        <Textarea
          label={t("hold.liftNote")}
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
          <Button disabled={lift.isPending} loading={lift.isPending} onClick={() => lift.mutate()}>
            {t("hold.lift")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
