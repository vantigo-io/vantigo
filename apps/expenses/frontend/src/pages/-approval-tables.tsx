import { Badge, Button, Checkbox, Group, Stack, Table, Text, UnstyledButton, VisuallyHidden } from "@mantine/core";
import { IconPencilDollar, IconReceiptOff } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import type { ClaimListItem, ClaimSummary } from "../api/claims";
import type { Expense } from "../api/entries";
import { ClaimSummaryLine } from "../components/claim-summary";
import { ExpenseStatusBadge } from "../components/expense-status-badge";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { useExpenseFormat } from "../lib/format";
import { expenseKindLabelKey } from "../lib/status";

/**
 * A trip as a queue row. The waiting queue carries `ExpensesClaimSummary`,
 * which counts the receipts missing and the rates replaced on the trip; the
 * approved half comes from `GET /claims`, which does not — an approved trip
 * has been looked at already, and those two flags are what an approver needs
 * *before* deciding. `flagsOf` is the one place that tells them apart.
 */
export type ClaimRow = ClaimSummary | ClaimListItem;

const flagsOf = (claim: ClaimRow): ClaimSummary | undefined =>
  "receiptsMissing" in claim ? (claim as ClaimSummary) : undefined;

/**
 * The person's trips, one row each. A trip is a unit, not a run of lines: the
 * row says what it was for, where it went, when — in the installation's own
 * time zone — how many expenses it holds and what it comes to per currency,
 * and its lines are one click away in the drawer.
 *
 * A flag is a word **and** an icon, never a colour alone.
 */
export const ClaimTable = ({
  label,
  claims,
  timeZone,
  refusals,
  selected,
  selectable,
  onToggle,
  onOpen,
}: {
  label: string;
  claims: ClaimRow[];
  timeZone: string;
  refusals: Map<number, string[]>;
  selected: number[];
  selectable: (claim: ClaimRow) => boolean;
  onToggle: (id: number, on: boolean) => void;
  onOpen: (claim: ClaimRow) => void;
}) => {
  const { t } = useI18n("expenses");
  return (
    <Table.ScrollContainer minWidth={760}>
      <Table striped highlightOnHover aria-label={label}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>
              <VisuallyHidden>{t("select")}</VisuallyHidden>
            </Table.Th>
            <Table.Th>{t("claimTrip")}</Table.Th>
            <Table.Th>{t("claimFlags")}</Table.Th>
            <Table.Th>{t("rowActions")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {claims.map((claim) => (
            <Table.Tr key={claim.id} data-claim={claim.id}>
              <Table.Td>
                {selectable(claim) && (
                  <Checkbox
                    aria-label={t("selectTravelClaim", { purpose: claim.purpose })}
                    checked={selected.includes(claim.id)}
                    onChange={(event) => onToggle(claim.id, event.currentTarget.checked)}
                  />
                )}
              </Table.Td>
              <Table.Td>
                <Stack gap={2}>
                  <ClaimSummaryLine claim={claim} timeZone={timeZone} />
                  <RefusalList messages={refusals.get(claim.id) ?? []} />
                </Stack>
              </Table.Td>
              <Table.Td>
                <Group gap={4} wrap="wrap">
                  {flagsOf(claim)?.receiptsMissing ? (
                    <Badge color="orange" leftSection={<IconReceiptOff size={12} />}>
                      {t("receiptsMissing", { count: flagsOf(claim)?.receiptsMissing })}
                    </Badge>
                  ) : null}
                  {flagsOf(claim)?.overriddenRates ? (
                    <Badge color="grape" leftSection={<IconPencilDollar size={12} />}>
                      {t("overriddenRates", { count: flagsOf(claim)?.overriddenRates })}
                    </Badge>
                  ) : null}
                </Group>
              </Table.Td>
              <Table.Td>
                <Button
                  size="sm"
                  h={40}
                  variant="subtle"
                  aria-label={t("openTravelClaim", { purpose: claim.purpose })}
                  onClick={() => onOpen(claim)}
                >
                  {t("open")}
                </Button>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};

/** The person's standalone expenses — a travel claim's lines are never here. */
export const EntryTable = ({
  label,
  entries,
  refusals,
  selected,
  selectable,
  onToggle,
  onOpen,
}: {
  label: string;
  entries: Expense[];
  refusals: Map<number, string[]>;
  selected: number[];
  selectable: (entry: Expense) => boolean;
  onToggle: (id: number, on: boolean) => void;
  onOpen: (expense: Expense) => void;
}) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  return (
    <Table.ScrollContainer minWidth={820}>
      <Table striped highlightOnHover aria-label={label}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>
              <VisuallyHidden>{t("select")}</VisuallyHidden>
            </Table.Th>
            <Table.Th>{t("date")}</Table.Th>
            <Table.Th>{t("description")}</Table.Th>
            <Table.Th>{t("kind")}</Table.Th>
            <Table.Th>{t("project")}</Table.Th>
            <Table.Th>{t("grossAmount")}</Table.Th>
            <Table.Th>{t("owedToEmployee")}</Table.Th>
            <Table.Th>{t("receipts")}</Table.Th>
            <Table.Th>{t("status")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {entries.map((entry) => (
            <Table.Tr key={entry.id} data-expense={entry.id}>
              <Table.Td>
                {selectable(entry) && (
                  <Checkbox
                    aria-label={t("selectExpense", { description: entry.description })}
                    checked={selected.includes(entry.id)}
                    onChange={(event) => onToggle(entry.id, event.currentTarget.checked)}
                  />
                )}
              </Table.Td>
              <Table.Td>{format.date(entry.entryDate)}</Table.Td>
              <Table.Td>
                <Stack gap={2}>
                  <UnstyledButton
                    aria-label={t("openExpense", { description: entry.description })}
                    onClick={() => onOpen(entry)}
                  >
                    <Text size="sm" td="underline">
                      {entry.description}
                    </Text>
                  </UnstyledButton>
                  <RefusalList messages={refusals.get(entry.id) ?? []} />
                </Stack>
              </Table.Td>
              <Table.Td>
                <Group gap={4} wrap="nowrap">
                  <Text size="sm">{t(expenseKindLabelKey(entry.kind))}</Text>
                  {entry.rateOverride && (
                    <Badge size="xs" color="grape" leftSection={<IconPencilDollar size={10} />}>
                      {t("rateOverridden")}
                    </Badge>
                  )}
                </Group>
              </Table.Td>
              <Table.Td>
                <Text size="sm">{entry.project ? entry.project.code : t("notAvailable")}</Text>
              </Table.Td>
              <Table.Td>{format.money(entry.grossAmount, entry.currency)}</Table.Td>
              <Table.Td>{format.money(entry.owedToEmployee, entry.currency)}</Table.Td>
              <Table.Td>
                {entry.kind === "mileage" ? (
                  <Text size="xs" c="dimmed">
                    {t("notAvailable")}
                  </Text>
                ) : entry.attachmentCount === 0 ? (
                  <Text size="xs" c="orange">
                    {t("receiptMissing")}
                  </Text>
                ) : (
                  <Text size="xs">
                    {entry.kind === "supplier_invoice"
                      ? t("supplierInvoiceAttached")
                      : entry.attachmentCount === 1
                        ? t("oneReceipt")
                        : t("receiptCount", { count: entry.attachmentCount })}
                  </Text>
                )}
              </Table.Td>
              <Table.Td>
                <ExpenseStatusBadge status={entry.status} size="sm" />
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};
