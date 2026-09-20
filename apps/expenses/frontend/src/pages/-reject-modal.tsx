import { Button, Group, Modal, Stack, Textarea } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { rejectExpenses } from "../api/approvals";
import { ApiValidationError, EXPENSES_QUERY_KEY } from "../api/request";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { refusalMessages } from "../lib/errors";

/** What the contract allows in a rejection reason, once trimmed. */
export const REJECTION_REASON_MAX_LENGTH = 1000;

export interface RejectModalProps {
  /** The expenses to reject, or null when the modal is closed. */
  entryIds: number[] | null;
  onClose: () => void;
  onRejected: () => void;
}

/**
 * Rejecting expenses with a reason their owner sees. The batch is all or
 * nothing, so a refusal names each offending id and nothing is rejected —
 * every sentence is shown rather than the first.
 */
export const RejectModal = ({ entryIds, onClose, onRejected }: RejectModalProps) => {
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
    mutationFn: (reason: string) => rejectExpenses(entryIds ?? [], reason.trim()),
    onSuccess: async (rejected) => {
      setRefusals([]);
      form.reset();
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({
        color: "teal",
        title: t("expensesRejected"),
        message:
          rejected.entries.length === 1 ? t("oneExpense") : t("countOfExpenses", { count: rejected.entries.length }),
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
      opened={entryIds !== null}
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
