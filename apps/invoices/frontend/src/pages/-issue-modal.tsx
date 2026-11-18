import { Alert, Button, Checkbox, Group, Modal, Radio, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { balanceIn, customerCreditQueryOptions } from "../api/credit";
import { type InvoiceDocument, issueInvoice } from "../api/invoices";
import { invoicesMetaQueryOptions } from "../api/meta";
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
 * dialog never re-derives the rule. An invoice whose customer has credit
 * offers **Use the customer's credit** (invoices phase 6 design I11), off:
 * it is applied after the issue, and a failure there is a warning.
 */
export const IssueModal = ({ draft, onClose }: IssueModalProps) => {
  const { t, date, money } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const allowed = draft.allowedIssueDates ?? [];
  const [issueDate, setIssueDate] = useState(allowed[allowed.length - 1] ?? "");
  const [useCredit, setUseCredit] = useState(false);
  // Applying credit is invoices:payments' as well as the issue's.
  const meta = useQuery(invoicesMetaQueryOptions());
  const canApply = draft.kind === "invoice" && meta.data?.capabilities.canRegisterPayments === true;
  const credit = useQuery({ ...customerCreditQueryOptions(draft.customerId), enabled: canApply });
  const balance = canApply ? balanceIn(credit.data, draft.currency) : 0;
  const issue = useMutation({
    mutationFn: () => issueInvoice(draft.id, issueDate || undefined, balance > 0 && useCredit),
    onSuccess: async (issued) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("issuedAs", { number: issued.number }) });
      if (issued.warnings.includes("issued_late")) {
        notifications.show({ color: "yellow", title: t("warning.issued_late"), message: t("issuedLateAfter") });
      }
      if (issued.warnings.includes("credit_not_applied")) {
        notifications.show({ color: "yellow", message: t("warning.credit_not_applied") });
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
        {balance > 0 && (
          <Checkbox
            label={t("credit.useOnIssue")}
            description={t("credit.useOnIssueHint", { balance: money(balance, draft.currency) })}
            checked={useCredit}
            onChange={(e) => setUseCredit(e.currentTarget.checked)}
          />
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
