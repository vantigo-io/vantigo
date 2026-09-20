import { Alert, Badge, Group, SimpleGrid, Stack, Table, Text, Title } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import type { Claim } from "../api/claims";
import "../i18n";
import { useExpenseFormat } from "../lib/format";
import { useLineName } from "../lib/line-name";
import { expenseKindLabelKey } from "../lib/status";
import { CurrencyTotals } from "./currency-totals";
import { ExpenseStatusBadge } from "./expense-status-badge";

const Field = ({ label, children }: { label: string; children: ReactNode }) => (
  <Stack gap={0}>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text size="sm">{children}</Text>
  </Stack>
);

export interface ClaimDetailsProps {
  claim: Claim;
  /** The installation's business time zone, from `/meta`. */
  timeZone: string;
}

/**
 * One travel claim written out in full, read only — the single place this
 * package renders a trip nobody is editing. The claim page uses it when
 * `capabilities.canEdit` is false, and an approver's drawer (a later
 * delivery) shows the same thing.
 *
 * Everything shown is what the server sent: the billable totals are there
 * because the caller's financial rights on the project put them there, the
 * decision is there while one stands, and nothing is re-derived.
 */
export const ClaimDetails = ({ claim, timeZone }: ClaimDetailsProps) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const lineName = useLineName();

  return (
    <Stack gap="md">
      <Group gap="xs">
        <ExpenseStatusBadge status={claim.status} />
        {claim.abroad && <Badge variant="default">{t("claimAbroad")}</Badge>}
      </Group>

      {claim.decision?.status === "rejected" && claim.decision.reason && (
        <Alert color="red" title={t("rejectedBecause", { reason: claim.decision.reason })}>
          {/* `by` is optional in the contract, for a stored decision with no
              decider that no operation can produce; the reason stands on its
              own when there is nobody to name. */}
          {claim.decision.by
            ? t("rejectedBy", { person: claim.decision.by.displayName, date: format.dateTime(claim.decision.at) })
            : t("rejectedOn", { date: format.dateTime(claim.decision.at) })}
        </Alert>
      )}
      {claim.decision?.status === "approved" && (
        <Text size="sm" c="dimmed">
          {claim.decision.by
            ? t("approvedBy", { person: claim.decision.by.displayName, date: format.dateTime(claim.decision.at) })
            : t("approvedOn", { date: format.dateTime(claim.decision.at) })}
        </Text>
      )}

      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
        <Field label={t("claimPurpose")}>{claim.purpose}</Field>
        <Field label={t("claimDestination")}>{claim.destination ?? t("notAvailable")}</Field>
        <Field label={t("claimDeparture")}>{format.zonedDateTime(claim.departureAt, timeZone)}</Field>
        <Field label={t("claimReturn")}>{format.zonedDateTime(claim.returnAt, timeZone)}</Field>
        <Field label={t("owner")}>{claim.owner.displayName}</Field>
        {claim.project && <Field label={t("project")}>{`${claim.project.code} · ${claim.project.name}`}</Field>}
        {claim.abroad && claim.abroadDayRate !== undefined && (
          <Field label={t("claimAbroadDayRate")}>{format.money(claim.abroadDayRate, claim.abroadCurrency ?? "")}</Field>
        )}
      </SimpleGrid>

      <Stack gap={4}>
        <Title order={6}>{t("claimTotals")}</Title>
        <CurrencyTotals totals={claim.totals} />
        {claim.billableTotals && claim.billableTotals.length > 0 && (
          <Stack gap={0} data-testid="claim-billable-totals">
            {claim.billableTotals.map((total) => (
              <Text key={total.currency} size="sm">
                {`${t("claimBillableTotal")}: ${format.money(total.amount, total.currency)}`}
              </Text>
            ))}
          </Stack>
        )}
      </Stack>

      {claim.reimbursement && (
        <Text size="sm" c="dimmed">
          {claim.reimbursement.reference
            ? `${t("reimbursedOn", { date: format.date(claim.reimbursement.date) })} · ${t("reimbursementReference", { reference: claim.reimbursement.reference })}`
            : t("reimbursedOn", { date: format.date(claim.reimbursement.date) })}
        </Text>
      )}

      <Table.ScrollContainer minWidth={520}>
        <Table aria-label={t("claimLines")}>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("date")}</Table.Th>
              <Table.Th>{t("description")}</Table.Th>
              <Table.Th>{t("kind")}</Table.Th>
              <Table.Th>{t("amount")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {claim.lines.map((line) => (
              <Table.Tr key={line.id} data-expense={line.id}>
                <Table.Td>{format.date(line.entryDate)}</Table.Td>
                <Table.Td>{lineName(line)}</Table.Td>
                <Table.Td>{t(expenseKindLabelKey(line.kind))}</Table.Td>
                <Table.Td>{format.money(line.grossAmount, line.currency)}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {claim.lines.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("claimHoldsNothing")}
        </Text>
      )}
    </Stack>
  );
};
