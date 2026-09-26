import { Alert, Button, Group, Modal, Radio, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { type InvoiceDocument, issueInvoice } from "../api/invoices";
import { INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface IssueModalProps {
  draft: InvoiceDocument;
  onClose: () => void;
}

/**
 * The issue dialog (D12): it says what issuing does, and offers the dates the
 * server allows this draft today — today, and the last day of the previous
 * month only while D6 allows it. The server answers `allowedIssueDates`; this
 * dialog never re-derives the rule.
 */
export const IssueModal = ({ draft, onClose }: IssueModalProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const allowed = draft.allowedIssueDates ?? [];
  const [issueDate, setIssueDate] = useState(allowed[allowed.length - 1] ?? "");
  const issue = useMutation({
    mutationFn: () => issueInvoice(draft.id, issueDate || undefined),
    onSuccess: async (issued) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("issuedAs", { number: issued.number }) });
      if (issued.warnings.includes("issued_late")) {
        notifications.show({ color: "yellow", title: t("warning.issued_late"), message: t("issuedLateAfter") });
      }
      onClose();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotIssue"), message: refusalMessage(error, t, date) }),
  });
  return (
    <Modal opened onClose={onClose} title={draft.kind === "credit_note" ? t("issueCreditNote") : t("issueInvoice")}>
      <Stack>
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
          {t("issueCannotBeUndone")}
        </Alert>
        {allowed.length > 1 ? (
          <Radio.Group label={t("issueDate")} value={issueDate} onChange={setIssueDate}>
            <Stack gap="xs" mt="xs">
              {allowed.map((d) => (
                <Radio key={d} value={d} label={date(d)} />
              ))}
            </Stack>
          </Radio.Group>
        ) : (
          <Text>{t("issuedToday", { date: allowed[0] ? date(allowed[0]) : "" })}</Text>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={issue.isPending} onClick={() => issue.mutate()}>
            {t("issue")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
