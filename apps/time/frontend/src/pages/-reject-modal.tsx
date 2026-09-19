import { Button, Group, Modal, Stack, Textarea } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { rejectTimeEntries } from "../api/approvals";
import { ApiValidationError } from "../api/request";
import { RefusalList } from "../components/refusal-list";
import "../i18n";

/** What the server stores in `rejection_reason`, and refuses past. */
export const REJECTION_REASON_MAX_LENGTH = 1000;

export interface RejectModalProps {
  /** The entries to reject, or null when nothing is being rejected. */
  ids: number[] | null;
  onClose: () => void;
  onRejected: () => void;
}

/**
 * The reason a rejection carries (§4.2): required, and the one thing the
 * owner is told about why their time came back, so the modal asks for it
 * before anything is sent. The form is mounted fresh every time the modal
 * opens, so no previous reason survives a close.
 */
export const RejectModal = ({ ids, onClose, onRejected }: RejectModalProps) => {
  const { t } = useI18n("time");
  return (
    <Modal opened={ids !== null} onClose={onClose} title={t("rejectTitle")} centered>
      {ids && <RejectForm ids={ids} onClose={onClose} onRejected={onRejected} />}
    </Modal>
  );
};

const RejectForm = ({ ids, onClose, onRejected }: RejectModalProps & { ids: number[] }) => {
  const { t } = useI18n("time");
  const queryClient = useQueryClient();
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
    mutationFn: (reason: string) => rejectTimeEntries(ids, reason),
    onSuccess: async (entries) => {
      notifications.show({
        color: "teal",
        title: t("entriesRejected"),
        message: t("entriesChanged", { count: entries.length }),
      });
      onRejected();
      await queryClient.invalidateQueries({ queryKey: ["time"] });
    },
    onError: (failure) => {
      // A reason the server itself turned down belongs on the field; the
      // per-id refusals of an all-or-nothing batch belong in a notification.
      if (failure instanceof ApiValidationError && failure.fields.reason) {
        form.setFieldError("reason", failure.fields.reason[0]);
        return;
      }
      notifications.show({ color: "red", title: t("couldNotReject"), message: <RefusalList error={failure} /> });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => reject.mutate(values.reason.trim()))}>
      <Stack>
        <Textarea
          label={t("rejectReason")}
          description={t("rejectReasonDescription")}
          withAsterisk
          rows={4}
          data-autofocus
          {...form.getInputProps("reason")}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" color="red" loading={reject.isPending}>
            {t("reject")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
