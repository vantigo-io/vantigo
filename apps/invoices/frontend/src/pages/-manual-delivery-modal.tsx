import { Button, Group, Modal, SegmentedControl, Stack, Text, Textarea } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { MANUAL_DELIVERY_KINDS, type ManualDeliveryKind, recordDelivery } from "../api/charges";
import type { InvoiceDocument } from "../api/invoices";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface ManualDeliveryModalProps {
  invoice: InvoiceDocument;
  /** Today in Oslo, meta's: the day prefilled and the latest a delivery may be. */
  today: string;
  onClose: () => void;
}

const deliveryInputs = new Set(["kind", "deliveredOn", "note"]);

/**
 * Records a delivery made outside Vantigo (invoices payments and reminders
 * design D8): the invoice handed over or posted, on a day from its issue date
 * to today, with a note. A reminder may claim a charge only of an invoice
 * delivered by its due date — by e-mail, as EHF, or recorded here.
 */
export const ManualDeliveryModal = ({ invoice, today, onClose }: ManualDeliveryModalProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [kind, setKind] = useState<ManualDeliveryKind>("handed_over");
  const [deliveredOn, setDeliveredOn] = useState<string | null>(today);
  const [note, setNote] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const record = useMutation({
    mutationFn: () =>
      recordDelivery(invoice.id, {
        kind,
        deliveredOn: deliveredOn ?? "",
        ...(note.trim() ? { note: note.trim() } : {}),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("delivery.recorded") });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(error, t, (field) => deliveryInputs.has(field), "delivery");
        setErrors(onInputs);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("delivery.couldNotRecord"), message: elsewhere.join(" ") });
        }
        return;
      }
      notifications.show({
        color: "red",
        title: t("delivery.couldNotRecord"),
        message: refusalMessage(error, t, date),
      });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("delivery.record")}>
      <Stack>
        <Text size="sm">{t("delivery.recordHint")}</Text>
        <SegmentedControl
          aria-label={t("delivery.kind")}
          value={kind}
          onChange={(v) => setKind(v as ManualDeliveryKind)}
          data={MANUAL_DELIVERY_KINDS.map((k) => ({ value: k, label: t(`delivery.kind.${k}`) }))}
        />
        {errors.kind && (
          <Text size="sm" c="red">
            {errors.kind}
          </Text>
        )}
        <DateInput
          label={t("delivery.deliveredOn")}
          required
          valueFormat={t("dateInputFormat")}
          minDate={invoice.issueDate}
          maxDate={today}
          value={deliveredOn}
          error={errors.deliveredOn}
          onChange={(d) => {
            setDeliveredOn(d);
            setErrors((current) => Object.fromEntries(Object.entries(current).filter(([f]) => f !== "deliveredOn")));
          }}
        />
        <Textarea
          label={t("note")}
          maxLength={500}
          value={note}
          error={errors.note}
          onChange={(e) => {
            setNote(e.currentTarget.value);
            setErrors((current) => Object.fromEntries(Object.entries(current).filter(([f]) => f !== "note")));
          }}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            disabled={record.isPending || !deliveredOn}
            loading={record.isPending}
            onClick={() => record.mutate()}
          >
            {t("delivery.recordSubmit")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
