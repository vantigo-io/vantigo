import { Alert, Button, Group, Modal, Radio, Stack, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { resolveTransmission, type Transmission, type TransmissionResolution } from "../api/ehf";
import type { InvoiceDocument } from "../api/invoices";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface ResolveTransmissionModalProps {
  document: InvoiceDocument;
  /** An unconfirmed transmission. */
  transmission: Transmission;
  onClose: () => void;
}

/**
 * A person's verdict on an unconfirmed transmission (EHF and KID design D9),
 * after checking with the provider: delivered closes it, failed allows a new
 * send carrying the same UBL. Both the outcome and a note are required; the
 * button waits for them and is disabled while the request is on its way. A
 * transmission no longer unconfirmed is `transmission_not_resolvable`, in
 * the reader's language.
 */
export const ResolveTransmissionModal = ({ document: doc, transmission, onClose }: ResolveTransmissionModalProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [outcome, setOutcome] = useState<TransmissionResolution["outcome"] | null>(null);
  const [note, setNote] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const resolve = useMutation({
    mutationFn: (chosen: TransmissionResolution["outcome"]) =>
      resolveTransmission(doc.id, transmission.id, { outcome: chosen, note: note.trim() }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("transmissionResolved") });
      onClose();
    },
    onError: (refusal) => {
      if (refusal instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(
          refusal,
          t,
          (field) => field === "note" || field === "outcome",
          "resolve",
        );
        setErrors(onInputs);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("couldNotResolveTransmission"), message: elsewhere.join(" ") });
        }
        return;
      }
      notifications.show({
        color: "red",
        title: t("couldNotResolveTransmission"),
        message: refusalMessage(refusal, t, date),
      });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("resolveTransmissionTitle")}>
      <Stack>
        <Alert color="gray" icon={<IconInfoCircle size={16} />} role="note">
          {t("resolveTransmissionHint")}
        </Alert>
        <Radio.Group
          label={t("resolveOutcome")}
          required
          value={outcome}
          error={errors.outcome}
          onChange={(value) => {
            setOutcome(value as TransmissionResolution["outcome"]);
            setErrors((current) => {
              const next = { ...current };
              delete next.outcome;
              return next;
            });
          }}
        >
          <Group mt="xs">
            <Radio value="delivered" label={t("resolveOutcome.delivered")} />
            <Radio value="failed" label={t("resolveOutcome.failed")} />
          </Group>
        </Radio.Group>
        <Textarea
          label={t("resolveNote")}
          required
          maxLength={500}
          value={note}
          error={errors.note}
          onChange={(e) => {
            setNote(e.currentTarget.value);
            setErrors((current) => {
              const next = { ...current };
              delete next.note;
              return next;
            });
          }}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            disabled={resolve.isPending || outcome === null || note.trim() === ""}
            loading={resolve.isPending}
            onClick={() => {
              if (outcome !== null) resolve.mutate(outcome);
            }}
          >
            {t("resolveTransmission")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
