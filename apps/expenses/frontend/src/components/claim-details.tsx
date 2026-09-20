import { Alert, Badge, Group, SimpleGrid, Stack, Table, Text, Title } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import type { Claim } from "../api/claims";
import type { Expense } from "../api/entries";
import "../i18n";
import { useExpenseFormat } from "../lib/format";
import { useLineName } from "../lib/line-name";
import { expenseKindLabelKey } from "../lib/status";
import { CurrencyTotals } from "./currency-totals";
import { ExpenseStatusBadge } from "./expense-status-badge";
import { PerDiemDetails } from "./per-diem-details";
import { ReceiptThumbnails } from "./receipt-thumbnails";
import { RefusalList } from "./refusal-list";

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
  /**
   * Left out, the lines are one line each. An approver asks for the whole of
   * each: a per diem day's figures, an outlay's receipts (which open in the
   * viewer) and who replaced a rate — everything a decision is made on,
   * without a second table beside this one.
   */
  withLineDetails?: boolean;
  /**
   * An extra, last column on the lines table. The approver's drawer puts the
   * per-line doors there — the rate override, the pricing and the invoicing —
   * so a trip is still **one** named table rather than two.
   */
  lineActions?: (line: Expense) => ReactNode;
  /** The sentences a refusal named, keyed by the line each one is about. */
  lineRefusals?: Map<number, string[]>;
}

/**
 * One travel claim written out in full, read only — the single place this
 * package renders a trip nobody is editing. The claim page uses it when
 * `capabilities.canEdit` is false, and the approver's drawer shows the same
 * thing with the per-line doors on the end of each row.
 *
 * Everything shown is what the server sent: the billable totals are there
 * because the caller's financial rights on the project put them there, the
 * decision is there while one stands, and nothing is re-derived.
 *
 * **Every instant on this screen is written in the installation's own zone**,
 * not the reader's — the decision, the invoice stamp and the trip's two ends
 * alike, so no one date on the page disagrees with the calendar the company
 * keeps. (`reimbursement.date` is a calendar date already and needs none.)
 */
export const ClaimDetails = ({
  claim,
  timeZone,
  withLineDetails = false,
  lineActions,
  lineRefusals,
}: ClaimDetailsProps) => {
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
            ? t("rejectedBy", {
                person: claim.decision.by.displayName,
                date: format.zonedDate(claim.decision.at, timeZone),
              })
            : t("rejectedOn", { date: format.zonedDate(claim.decision.at, timeZone) })}
        </Alert>
      )}
      {claim.decision?.status === "approved" && (
        <Text size="sm" c="dimmed">
          {claim.decision.by
            ? t("approvedBy", {
                person: claim.decision.by.displayName,
                date: format.zonedDate(claim.decision.at, timeZone),
              })
            : t("approvedOn", { date: format.zonedDate(claim.decision.at, timeZone) })}
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
              {lineActions && <Table.Th>{t("rowActions")}</Table.Th>}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {claim.lines.map((line) => (
              <Table.Tr key={line.id} data-expense={line.id}>
                <Table.Td>{format.date(line.entryDate)}</Table.Td>
                <Table.Td>
                  <Stack gap={2}>
                    <Text size="sm">{lineName(line)}</Text>
                    {withLineDetails && line.kind === "per_diem" && line.perDiem && (
                      <PerDiemDetails perDiem={line.perDiem} currency={line.currency} />
                    )}
                    {withLineDetails && line.kind === "mileage" && line.distanceKm !== undefined && (
                      <Text size="xs" c="dimmed">
                        {format.distance(line.distanceKm)}
                      </Text>
                    )}
                    {withLineDetails && line.rateOverride && (
                      <Text size="xs" c="dimmed">
                        {t("rateOverriddenBy", {
                          person: line.rateOverride.byUser.displayName,
                          table:
                            line.rateOverride.tableValue === undefined
                              ? t("notAvailable")
                              : format.money(line.rateOverride.tableValue, line.currency),
                        })}
                      </Text>
                    )}
                    {withLineDetails &&
                      line.kind === "outlay" &&
                      (line.attachmentCount === 0 ? (
                        <Text size="xs" c="orange">
                          {t("receiptMissing")}
                        </Text>
                      ) : (
                        <ReceiptThumbnails attachments={line.attachments} size={40} />
                      ))}
                    {withLineDetails && line.billing?.invoice && (
                      <Text size="xs" c="dimmed">
                        {t("invoicedOn", { date: format.zonedDate(line.billing.invoice.at, timeZone) })}
                      </Text>
                    )}
                    <RefusalList messages={lineRefusals?.get(line.id) ?? []} />
                  </Stack>
                </Table.Td>
                <Table.Td>{t(expenseKindLabelKey(line.kind))}</Table.Td>
                <Table.Td>{format.money(line.grossAmount, line.currency)}</Table.Td>
                {lineActions && <Table.Td>{lineActions(line)}</Table.Td>}
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
