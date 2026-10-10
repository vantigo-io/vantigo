import { Button, Checkbox, Group, Modal, Select, Stack, Text, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { WAIVE_REASONS, type WaiveReason, waiveCharges } from "../api/charges";
import type { InvoiceDocument } from "../api/invoices";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { type WaivableCharge, waivableCharges } from "../lib/reminders";

export interface WaiveModalProps {
  invoice: InvoiceDocument;
  onClose: () => void;
}

/**
 * Waives reminder charges (D9): the person ticks each fee and compensation to
 * release — the letter's whole charge — and the interest, which is waived as
 * an amount: what the latest letter claimed, less what is waived or paid of it
 * already, nothing accrued since. A reason is required — the objection was
 * upheld, it was claimed in error, or goodwill — with a note. A charge that is
 * not claimed, or waived meanwhile, is refused in words.
 */
export const WaiveModal = ({ invoice, onClose }: WaiveModalProps) => {
  const { t, money, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const charges = waivableCharges(invoice);
  const [chosen, setChosen] = useState<Set<string>>(new Set());
  const [reason, setReason] = useState<WaiveReason | null>(null);
  const [note, setNote] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const keyOf = (c: WaivableCharge) => `${c.reminderId}:${c.kind}`;
  const waive = useMutation({
    mutationFn: () =>
      waiveCharges(invoice.id, {
        waivers: charges.filter((c) => chosen.has(keyOf(c))).map((c) => ({ reminderId: c.reminderId, kind: c.kind })),
        reason: reason ?? "goodwill",
        ...(note.trim() ? { note: note.trim() } : {}),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("charges.waived") });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(
          error,
          t,
          (field) => field === "reason" || field === "note",
          "waive",
        );
        setErrors(onInputs);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("charges.couldNotWaive"), message: elsewhere.join(" ") });
        }
        return;
      }
      notifications.show({ color: "red", title: t("charges.couldNotWaive"), message: refusalMessage(error, t, date) });
    },
  });
  const label = (c: WaivableCharge) =>
    c.kind === "interest"
      ? t("charges.waive.interest", { n: c.sequence, amount: money(c.amount, invoice.currency) })
      : t(`charges.waive.${c.kind}`, { n: c.sequence, amount: money(c.amount, invoice.currency) });
  return (
    <Modal opened onClose={onClose} title={t("charges.waiveTitle")}>
      <Stack>
        {charges.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("charges.waive.nothing")}
          </Text>
        ) : (
          <Stack gap="xs" role="group" aria-label={t("charges.waive.which")}>
            {charges.map((c) => (
              <Checkbox
                key={keyOf(c)}
                label={label(c)}
                checked={chosen.has(keyOf(c))}
                onChange={(e) => {
                  const checked = e.currentTarget.checked;
                  setChosen((current) => {
                    const next = new Set(current);
                    if (checked) next.add(keyOf(c));
                    else next.delete(keyOf(c));
                    return next;
                  });
                }}
              />
            ))}
          </Stack>
        )}
        <Text size="xs" c="dimmed">
          {t("charges.waive.interestHint")}
        </Text>
        <Select
          label={t("charges.waive.reason")}
          required
          data={WAIVE_REASONS.map((r) => ({ value: r, label: t(`charges.reason.${r}`) }))}
          value={reason}
          error={errors.reason}
          onChange={(v) => {
            setReason(v as WaiveReason | null);
            setErrors((current) => Object.fromEntries(Object.entries(current).filter(([f]) => f !== "reason")));
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
            disabled={waive.isPending || chosen.size === 0 || reason === null}
            loading={waive.isPending}
            onClick={() => waive.mutate()}
          >
            {t("charges.waive.submit")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
