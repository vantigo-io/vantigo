import { Button, Group, Modal, Stack, Textarea } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { rejectUnits } from "../api/approvals";
import type { FlowUnits } from "../api/entries";
import { ApiValidationError, EXPENSES_QUERY_KEY } from "../api/request";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { refusalMessages } from "../lib/errors";

/** What the contract allows in a rejection reason, once trimmed. */
export const REJECTION_REASON_MAX_LENGTH = 1000;

export interface RejectModalProps {
  /**
   * The units to send back — standalone expenses, travel claims or both — or
   * null when the modal is closed. A trip is rejected as one unit and its
   * reason is stamped on the claim, which its owner then edits and submits
   * afresh.
   */
  units: FlowUnits | null;
  onClose: () => void;
  onRejected: () => void;
}

/**
 * Rejecting units with a reason their owner sees. The batch is all or
 * nothing across both lists, so a refusal names each offending id and nothing
 * is rejected — every sentence is shown rather than the first.
 */
export const RejectModal = ({ units, onClose, onRejected }: RejectModalProps) => {
  const { t } = useI18n("expenses");
  const queryClient = useQueryClient();
  const [refusals, setRefusals] = useState<string[]>([]);
  const form = useForm({
    initialValues: { reason: "" },
    validate: {
      reason: (value) => {
        const reason = value.trim();
        if (!reason) return t("rejectReasonRequired");
        return reason.length > REJECTION_REASON_MAX_LENGTH ? t("rejectReasonTooLong") : null;
      },
    },
  });

  const reject = useMutation({
    mutationFn: (reason: string) => rejectUnits(units ?? {}, reason.trim()),
    onSuccess: async (rejected) => {
      setRefusals([]);
      form.reset();
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      const count = rejected.entries.length + rejected.claims.length;
      notifications.show({
        color: "teal",
        title: t("expensesRejected"),
        message: count === 1 ? t("oneExpense") : t("countOfExpenses", { count }),
      });
      onRejected();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError && error.fieldErrors.reason) {
        form.setErrors({ reason: error.fieldErrors.reason });
        return;
      }
      setRefusals(refusalMessages(error));
    },
  });

  return (
    <Modal
      opened={units !== null}
      onClose={onClose}
      title={t("rejectTitle")}
      centered
      onExitTransitionEnd={() => setRefusals([])}
    >
      <form onSubmit={form.onSubmit(({ reason }) => reject.mutate(reason))}>
        <Stack>
          <RefusalList messages={refusals} />
          <Textarea
            label={t("rejectReason")}
            description={t("rejectReasonDescription")}
            withAsterisk
            rows={4}
            data-autofocus
            {...form.getInputProps("reason")}
          />
          <Group justify="flex-end">
            <Button type="button" variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" color="red" loading={reject.isPending}>
              {t("reject")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
