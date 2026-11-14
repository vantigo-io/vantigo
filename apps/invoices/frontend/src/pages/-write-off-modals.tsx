import { Alert, Button, Checkbox, Group, Modal, Select, Stack, Text, Textarea } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { InvoiceDocument } from "../api/invoices";
import { undoWriteOff, type WriteOffReason, writeOff } from "../api/payments";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

const reasons: WriteOffReason[] = ["loss", "rounding", "other"];

/** The fields of a write-off, each of which has an input here. */
const writeOffInputs = new Set(["writtenOffOn", "reason", "vatCorrection"]);

export interface WriteOffModalProps {
  invoice: InvoiceDocument;
  /** Today in Oslo, meta's: the day prefilled and the latest a write-off may be. */
  today: string;
  onClose: () => void;
}

/**
 * Writes an invoice's whole open amount off (accounting design A16, R4): the
 * day, the reason — a loss that is konstatert, a rounding or another — and,
 * for a loss, whether its VAT is taken back (mval. § 4-7). The dialog says
 * the amount and what follows: the invoice is no longer open, overdue or
 * reminded, and money that arrives later is registered as recovered.
 */
export const WriteOffModal = ({ invoice, today, onClose }: WriteOffModalProps) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [writtenOffOn, setWrittenOffOn] = useState<string | null>(today);
  const [reason, setReason] = useState<WriteOffReason>("loss");
  const [vatCorrection, setVatCorrection] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const mutation = useMutation({
    mutationFn: () =>
      writeOff(invoice.id, {
        writtenOffOn: writtenOffOn ?? "",
        reason,
        ...(reason === "loss" ? { vatCorrection } : {}),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("writeOff.done") });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(error, t, (field) => writeOffInputs.has(field), "writeOff");
        setErrors(onInputs);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("writeOff.couldNot"), message: elsewhere.join(" ") });
        }
        return;
      }
      notifications.show({
        color: "red",
        title: t("writeOff.couldNot"),
        message: refusalMessage(error, t, date, (value) => money(value, invoice.currency)),
      });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("writeOff.title")}>
      <Stack>
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
          {t("writeOff.hint", { amount: money(invoice.openAmount ?? 0, invoice.currency) })}
        </Alert>
        <DateInput
          label={t("writeOff.writtenOffOn")}
          required
          valueFormat={t("dateInputFormat")}
          minDate={invoice.issueDate}
          maxDate={today}
          value={writtenOffOn}
          error={errors.writtenOffOn}
          onChange={setWrittenOffOn}
        />
        <Select
          label={t("writeOff.reason")}
          required
          allowDeselect={false}
          data={reasons.map((r) => ({ value: r, label: t(`writeOff.reason.${r}`) }))}
          value={reason}
          error={errors.reason}
          onChange={(value) => {
            if (!value) return;
            setReason(value as WriteOffReason);
            if (value !== "loss") setVatCorrection(false);
          }}
        />
        <Checkbox
          label={t("writeOff.vatCorrection")}
          description={t("writeOff.vatCorrectionHint")}
          disabled={reason !== "loss"}
          checked={vatCorrection}
          error={errors.vatCorrection}
          onChange={(e) => setVatCorrection(e.currentTarget.checked)}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            color="red"
            disabled={mutation.isPending || !writtenOffOn}
            loading={mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {t("writeOff.button")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

export interface UndoWriteOffModalProps {
  invoice: InvoiceDocument;
  onClose: () => void;
}

/**
 * Undoes the standing write-off with a reason (R4): the invoice is open
 * again, and the write-off stays on it, struck through with the reason.
 */
export const UndoWriteOffModal = ({ invoice, onClose }: UndoWriteOffModalProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | undefined>();
  const mutation = useMutation({
    mutationFn: () => undoWriteOff(invoice.id, reason.trim()),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("writeOff.undone") });
      onClose();
    },
    onError: (refusal) => {
      if (refusal instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(refusal, t, (field) => field === "reason", "payment");
        setError(onInputs.reason);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("writeOff.couldNotUndo"), message: elsewhere.join(" ") });
        }
        return;
      }
      notifications.show({
        color: "red",
        title: t("writeOff.couldNotUndo"),
        message: refusalMessage(refusal, t, date),
      });
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("writeOff.undo")}>
      <Stack>
        <Text size="sm">{t("writeOff.undoHint")}</Text>
        <Textarea
          label={t("removalReason")}
          required
          maxLength={200}
          value={reason}
          error={error}
          onChange={(e) => {
            setReason(e.currentTarget.value);
            setError(undefined);
          }}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            disabled={mutation.isPending || !reason.trim()}
            loading={mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {t("writeOff.undo")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
