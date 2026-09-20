import { Alert, Badge, Group, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import type { Expense } from "../api/entries";
import "../i18n";
import { useExpenseFormat } from "../lib/format";
import { expenseKindLabelKey } from "../lib/status";
import { ExpenseStatusBadge } from "./expense-status-badge";
import { ReceiptThumbnails } from "./receipt-thumbnails";

export interface EntryDetailsProps {
  expense: Expense;
  /** Left out, the receipts are shown; the list rows pass false to keep a row short. */
  withReceipts?: boolean;
}

const Field = ({ label, children }: { label: string; children: ReactNode }) => (
  <Stack gap={0}>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text size="sm">{children}</Text>
  </Stack>
);

/**
 * One expense, read only — the single place this package writes an expense
 * out in full. The approval drawer uses it, and so does the expense form when
 * it is opened on something the caller may see but not change.
 *
 * It shows only what the server sent: the billing block is there when
 * `capabilities.canSeeBilling` put it there, the decision is there while one
 * stands, and the rate override is there while one stands. Nothing is
 * re-derived and nothing is guessed.
 */
export const EntryDetails = ({ expense, withReceipts = true }: EntryDetailsProps) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const mileage = expense.kind === "mileage";

  return (
    <Stack gap="md">
      <Group gap="xs">
        <ExpenseStatusBadge status={expense.status} />
        <Badge variant="default">{t(expenseKindLabelKey(expense.kind))}</Badge>
        {expense.rateOverride && <Badge color="orange">{t("rateOverridden")}</Badge>}
      </Group>

      {expense.decision?.status === "rejected" && expense.decision.reason && (
        <Alert color="red" title={t("rejectedBecause", { reason: expense.decision.reason })}>
          {expense.decision.by &&
            t("rejectedBy", { person: expense.decision.by.displayName, date: format.dateTime(expense.decision.at) })}
        </Alert>
      )}
      {expense.decision?.status === "approved" && expense.decision.by && (
        <Text size="sm" c="dimmed">
          {t("approvedBy", { person: expense.decision.by.displayName, date: format.dateTime(expense.decision.at) })}
        </Text>
      )}

      <SimpleGrid cols={{ base: 2, sm: 3 }} spacing="sm">
        <Field label={t("date")}>{format.date(expense.entryDate)}</Field>
        <Field label={t("description")}>{expense.description}</Field>
        <Field label={t("owner")}>{expense.owner.displayName}</Field>

        {mileage ? (
          <>
            <Field label={t("fromPlace")}>{expense.fromPlace ?? t("notAvailable")}</Field>
            <Field label={t("toPlace")}>{expense.toPlace ?? t("notAvailable")}</Field>
            <Field label={t("distanceKm")}>
              {expense.distanceKm === undefined ? t("notAvailable") : format.distance(expense.distanceKm)}
            </Field>
            <Field label={t("passengers")}>{expense.passengers ?? 0}</Field>
            <Field label={t("ratePerKm")}>
              {expense.rate === undefined ? t("notAvailable") : format.money(expense.rate, expense.currency)}
            </Field>
            {expense.passengerRate !== undefined && (
              <Field label={t("passengerRatePerKm")}>{format.money(expense.passengerRate, expense.currency)}</Field>
            )}
          </>
        ) : (
          <>
            <Field label={t("category")}>{expense.category?.name ?? t("notAvailable")}</Field>
            <Field label={t("supplier")}>{expense.supplier ?? t("notAvailable")}</Field>
            <Field label={t("paidBy")}>{expense.paidBy === "company" ? t("paidByCompany") : t("paidByEmployee")}</Field>
            <Field label={t("vatAmount")}>
              {expense.vatAmount === undefined ? t("notAvailable") : format.money(expense.vatAmount, expense.currency)}
            </Field>
          </>
        )}

        <Field label={t("grossAmount")}>{format.money(expense.grossAmount, expense.currency)}</Field>
        <Field label={t("netAmount")}>{format.money(expense.netAmount, expense.currency)}</Field>
        <Field label={t("owedToEmployee")}>{format.money(expense.owedToEmployee, expense.currency)}</Field>

        {expense.project && <Field label={t("project")}>{`${expense.project.code} · ${expense.project.name}`}</Field>}
        {expense.billingLine && <Field label={t("billingLine")}>{expense.billingLine.code}</Field>}
        {expense.project && <Field label={t("billable")}>{expense.billable ? t("yes") : t("no")}</Field>}
      </SimpleGrid>

      {expense.rateOverride && (
        <Text size="sm" c="dimmed">
          {t("rateOverriddenBy", {
            person: expense.rateOverride.byUser.displayName,
            table:
              expense.rateOverride.tableValue === undefined
                ? t("notAvailable")
                : format.money(expense.rateOverride.tableValue, expense.currency),
          })}
        </Text>
      )}

      {expense.reimbursement && (
        <Text size="sm" c="dimmed">
          {expense.reimbursement.reference
            ? `${t("reimbursedOn", { date: format.date(expense.reimbursement.date) })} · ${t("reimbursementReference", { reference: expense.reimbursement.reference })}`
            : t("reimbursedOn", { date: format.date(expense.reimbursement.date) })}
        </Text>
      )}

      {expense.capabilities.canSeeBilling && expense.billing && (
        <Stack gap={2} data-testid="expense-billing">
          <Title order={6}>{t("billingHeading")}</Title>
          <Text size="sm">{`${t("billAmount")}: ${format.money(expense.billing.billAmount, expense.currency)}`}</Text>
          {expense.billing.markupPercent !== undefined && (
            <Text size="sm">
              {`${t("markupPercent")}: ${t("vatPercent", { rate: format.number(expense.billing.markupPercent, 0) })}`}
            </Text>
          )}
          {expense.billing.billRatePerKm !== undefined && (
            <Text size="sm">
              {`${t("billRatePerKm")}: ${format.money(expense.billing.billRatePerKm, expense.currency)}`}
            </Text>
          )}
          {expense.billing.invoice && (
            <Text size="sm" c="dimmed">
              {t("invoicedOn", { date: format.dateTime(expense.billing.invoice.at) })}
            </Text>
          )}
        </Stack>
      )}

      {withReceipts && expense.kind === "outlay" && (
        <Stack gap={4}>
          <Title order={6}>{t("receipts")}</Title>
          {expense.attachmentCount === 0 ? (
            <Text size="sm" c="dimmed">
              {t("noReceipts")}
            </Text>
          ) : (
            <ReceiptThumbnails attachments={expense.attachments} size={72} />
          )}
        </Stack>
      )}
    </Stack>
  );
};
