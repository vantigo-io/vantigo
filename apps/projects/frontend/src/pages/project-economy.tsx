import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Box,
  Button,
  Card,
  Group,
  Menu,
  SimpleGrid,
  Stack,
  Table,
  Text,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDots, IconInfoCircle, IconLock, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n, useShellLink } from "@vantigo/frontend-shell";
import { type ReactNode, useId, useState } from "react";
import {
  type Economy,
  type EconomyExpenseCurrency,
  type EconomyLine,
  projectEconomyQueryOptions,
} from "../api/economy";
import {
  type BillingMilestone,
  type BillingMilestoneTotals,
  deleteMilestone,
  milestonePlanQueryOptions,
  moveMilestone,
  setMilestoneStatus,
} from "../api/milestones";
import { type Project, projectQueryOptions } from "../api/projects";
import type { ApiError } from "../api/request";
import { ApiValidationError } from "../api/request";
import { BudgetBar } from "../components/budget-bar";
import { Field } from "../components/field";
import { LoggedSplit } from "../components/logged-split";
import "../i18n";
import { useProjectDates } from "../lib/dates";
import { type BudgetBasis, useEconomyFormat } from "../lib/economy";
import { type MilestoneStatus, milestoneStatusColor, milestoneStatusLabelKey } from "../lib/milestones";
import { MilestoneFormModal, type MilestoneModalState } from "./-milestone-form-modal";
import { MilestoneInvoicedModal } from "./-milestone-invoiced-modal";
import { ProjectFormModal, type ProjectModalState } from "./-project-form-modal";

export interface ProjectEconomyProps {
  projectId: number;
  /**
   * Where this project's expenses live in the host's routes, for the link
   * beside the billable expenses waiting to go on an invoice. Without it the
   * row is a plain sentence: this package knows no route of the Expenses app
   * and imports nothing from it, so the host is the one that can say.
   */
  expensesHref?: string;
}

/**
 * The Economy tab (design §7): the budget against what has been logged, what
 * the project's expenses cost, and below them the invoice plan.
 *
 * The economy read never answers 403 — a caller who may not see the money is
 * answered without it, progressively emptier — so the budget half is shown to
 * everyone who sees the project, in hours when that is all they may see. The
 * invoice plan is financial data throughout and the API refuses it outright,
 * so that half alone stays behind `canSeeFinancials` and is never asked for.
 * The costs section carries nothing but money, so it stands or falls with the
 * `expenses` block the server either sends or does not.
 */
export const ProjectEconomy = ({ projectId, expensesHref }: ProjectEconomyProps) => {
  const { t } = useI18n("projects");
  const { data: project, isPending, isError, error } = useQuery(projectQueryOptions(projectId));

  if (isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProject")} mt="md">
        {error.message}
      </Alert>
    );
  }
  if (isPending)
    return (
      <Box mt="md">
        <ContentSkeleton rows={4} rowHeight={48} />
      </Box>
    );

  return (
    <Stack gap="lg" mt="md">
      <BudgetSection projectId={projectId} />
      <ExpensesSection projectId={projectId} />
      {project.capabilities.canSeeFinancials ? (
        <InvoicePlan projectId={projectId} project={project} expensesHref={expensesHref} />
      ) : (
        <EmptyState icon={IconLock} title={t("financialsHidden")} description={t("invoicePlanHiddenDescription")} />
      )}
    </Stack>
  );
};

/**
 * What was budgeted and what has been logged against it. Everything here is
 * rendered from what the response carries rather than from what the caller is
 * allowed: an absent field is absent, never a zero, and an absent `budgetUsed`
 * is "there is nothing to measure against" rather than "none of it is used".
 */
const BudgetSection = ({ projectId }: { projectId: number }) => {
  const { t } = useI18n("projects");
  const { data: economy, isPending, isError, error } = useQuery(projectEconomyQueryOptions(projectId));
  const currency = economy?.currency ?? undefined;
  const { hours, money, percent, basisPhrase } = useEconomyFormat(currency);
  const headingId = useId();

  if (isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadEconomy")}>
        {error.message}
      </Alert>
    );
  }
  if (isPending) return <ContentSkeleton rows={3} rowHeight={48} />;

  const used = economy.budgetUsed;
  const basisValue = used ? basisAmount(economy, used.basis) : undefined;
  // `budgetUsed` is absent for two different reasons — no basis to measure
  // against, and no time tracking — and they are not the same sentence. With no
  // hours to compare, the field says what *was* budgeted, picking the basis in
  // the server's own order; "No budget set" is kept for the case where none of
  // the three is there at all.
  const budgeted =
    economy.budget.amount != null
      ? t("budgetedValue", { value: money(economy.budget.amount) })
      : economy.budget.fixedPrice != null
        ? t("budgetedFixedPrice", { amount: money(economy.budget.fixedPrice) })
        : economy.budget.hours != null
          ? t("budgetedValue", { value: hours(economy.budget.hours) })
          : undefined;
  const usedText = used
    ? t("budgetUsedPercentOf", {
        percent: percent(used.percent),
        basis: basisPhrase(used.basis, basisValue),
      })
    : !economy.timeTracking && budgeted !== undefined
      ? budgeted
      : t("noBudgetSet");

  const linesAddUp: string[] = [];
  if (economy.budget.linesHours != null && economy.budget.hours != null) {
    linesAddUp.push(
      t("budgetLinesAddUp", { lines: hours(economy.budget.linesHours), project: hours(economy.budget.hours) }),
    );
  }
  if (economy.budget.linesAmount != null && economy.budget.amount != null) {
    linesAddUp.push(
      t("budgetLinesAddUp", { lines: money(economy.budget.linesAmount), project: money(economy.budget.amount) }),
    );
  }

  return (
    <Card withBorder padding="lg" radius="md" data-testid="project-budget">
      <Stack gap="md">
        <Stack gap={2}>
          <Text fw={600} component="h3" id={headingId}>
            {t("budgetAndWork")}
          </Text>
          <Text size="sm" c="dimmed">
            {t("budgetAndWorkDescription")}
          </Text>
        </Stack>

        <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }} spacing="md" data-testid="budget-headline">
          <Field label={t("budgetUsed")}>
            {usedText}
            {/* The server decides this on the exact ratio: 100.04 % arrives as
                percent 100 with overBudget true, so the badge follows the
                boolean and never the rounded number. */}
            {economy.overBudget && (
              <Badge component="span" variant="light" color="red" size="sm" ml={6}>
                {t("overBudget")}
              </Badge>
            )}
          </Field>
          {economy.actuals?.totalAmount != null && (
            <Field label={t("valueOfWork")}>{money(economy.actuals.totalAmount)}</Field>
          )}
          {economy.budget.fixedPrice != null && (
            <Field label={t("fixedPriceAmount")}>{money(economy.budget.fixedPrice)}</Field>
          )}
          {/* canSeeCosts says the caller *may* be shown costs; the block itself
              is also absent on a currencyless project and without time
              tracking, so the panel follows the block. */}
          {economy.cost && <Field label={t("margin")}>{money(economy.cost.margin)}</Field>}
        </SimpleGrid>

        {economy.timeTracking ? (
          economy.actuals &&
          // The project's own bar follows the same rule as its lines and the
          // portfolio's rows: with no basis a bar fills its whole width whatever
          // was logged, which is what a project at 100 % looks like — right
          // under a headline saying there is no budget.
          (used === undefined ? (
            <LoggedSplit segments={economy.actuals} totalHours={economy.actuals.totalHours} />
          ) : (
            <Box data-testid="project-budget-bar">
              <BudgetBar
                segments={economy.actuals}
                basis={used.basis}
                budget={basisValue}
                currency={currency}
                overBudget={economy.overBudget}
              />
            </Box>
          ))
        ) : (
          <Text size="sm" c="dimmed" data-testid="time-tracking-off">
            {t("timeTrackingOff")}
          </Text>
        )}

        <Stack gap={4}>
          {/* On a money basis, hours with no rate bill nothing and so make the
              bar understate: the figure is said out loud rather than left to
              be inferred from a percentage that looks too low. */}
          {economy.actuals && economy.actuals.unpricedHours > 0 && (
            <Text size="sm" c="dimmed" data-testid="unpriced-note">
              {t("unpricedHoursNote", { hours: hours(economy.actuals.unpricedHours) })}
            </Text>
          )}
          {economy.cost && economy.cost.uncostedHours > 0 && (
            <Text size="sm" c="dimmed" data-testid="uncosted-note">
              {t("uncostedHoursNote", { hours: hours(economy.cost.uncostedHours) })}
            </Text>
          )}
          {/* The margin is both halves of the project now, so it says so and
              shows what each half cost — the server publishes the two figures
              precisely so this does not have to subtract one from the other.
              `expenseCost` is there exactly when the cost block is and the
              expenses are tracked, so the block itself is the condition. */}
          {economy.cost?.expenseCost != null && (
            <>
              <Text size="sm" c="dimmed" data-testid="margin-counts-expenses">
                {t("marginCountsExpenses")}
              </Text>
              <Text size="sm" c="dimmed" data-testid="margin-cost-split">
                {t("marginCostSplit", { labour: money(economy.cost.total), expenses: money(economy.cost.expenseCost) })}
              </Text>
              {/* What the margin is short by, the same way the uncosted hours
                  are said out loud: a billable line nobody priced is in no
                  amount, and another currency is never converted into this one. */}
              {(economy.expenses?.unpricedCount ?? 0) > 0 && (
                <Text size="sm" c="dimmed" data-testid="margin-unpriced-expenses-note">
                  {t("marginLeavesOutUnpricedExpenses", { count: economy.expenses?.unpricedCount ?? 0 })}
                </Text>
              )}
              {economy.expenses?.otherCurrencies && economy.expenses.otherCurrencies.length > 0 && (
                <Text size="sm" c="dimmed" data-testid="margin-other-currencies-note">
                  {t("marginLeavesOutOtherCurrencies", {
                    currencies: economy.expenses.otherCurrencies.map((entry) => entry.currency).join(", "),
                  })}
                </Text>
              )}
            </>
          )}
          {economy.taskEstimateHours != null && (
            <Text size="sm" c="dimmed">
              {t("taskEstimateTotal", { hours: hours(economy.taskEstimateHours) })}
            </Text>
          )}
          {linesAddUp.map((sentence) => (
            <Text key={sentence} size="sm" c="dimmed">
              {sentence}
            </Text>
          ))}
        </Stack>

        {economy.lines.length > 0 && (
          <Table.ScrollContainer minWidth={720}>
            <Table striped highlightOnHover aria-labelledby={headingId}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("line")}</Table.Th>
                  <Table.Th>{t("budget")}</Table.Th>
                  <Table.Th>{t("logged")}</Table.Th>
                  <Table.Th>{t("used")}</Table.Th>
                  <Table.Th>{t("remaining")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {economy.lines.map((line) => (
                  <EconomyLineRow key={line.billingLineId ?? "no-line"} line={line} currency={currency} />
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}

        <WorkTypeTable economy={economy} currency={currency} />
      </Stack>
    </Card>
  );
};

/**
 * "Hours by work type" (work types design D4): the hours of each type the
 * project's entries were logged as, and — when the answer carries them —
 * what they are worth and what they cost. Ordinary hours are the absence of
 * a type, so they are no row, and the figures are already inside every total
 * above, multiplied where Time summed them: this is a split, never a sum.
 * The rows come in the order of the project's work types list (active first,
 * each half by name) and are drawn as they come. The list is absent without
 * time tracking and empty when no entry picked a type, and both draw nothing.
 * A column is drawn when the answer carries its figure, the way the cost
 * panel follows the cost block.
 */
const WorkTypeTable = ({ economy, currency }: { economy: Economy; currency?: string }) => {
  const { t } = useI18n("projects");
  const { hours, money } = useEconomyFormat(currency);
  const headingId = useId();
  const workTypes = economy.workTypes ?? [];
  if (workTypes.length === 0) return null;
  const showValue = workTypes.some((row) => row.billAmount != null);
  const showCost = workTypes.some((row) => row.costAmount != null);

  return (
    <Stack gap="xs">
      <Text fw={600} size="sm" component="h4" id={headingId}>
        {t("hoursByWorkType")}
      </Text>
      <Table.ScrollContainer minWidth={480}>
        <Table striped aria-labelledby={headingId}>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("workTypeColumn")}</Table.Th>
              <Table.Th>{t("workTypeHoursColumn")}</Table.Th>
              {showValue && <Table.Th>{t("workTypeValueColumn")}</Table.Th>}
              {showCost && <Table.Th>{t("workTypeCostColumn")}</Table.Th>}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {workTypes.map((row) => (
              <Table.Tr key={row.id}>
                <Table.Td>{row.name}</Table.Td>
                <Table.Td>{hours(row.hours)}</Table.Td>
                {showValue && <Table.Td>{money(row.billAmount)}</Table.Td>}
                {showCost && <Table.Td>{money(row.costAmount)}</Table.Td>}
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
    </Stack>
  );
};

/** Which of the project's budgets a basis names. */
const basisAmount = (economy: Economy, basis: BudgetBasis): number | null | undefined =>
  basis === "amount"
    ? economy.budget.amount
    : basis === "fixedPrice"
      ? economy.budget.fixedPrice
      : economy.budget.hours;

/**
 * What the project's expenses cost and what of them the customer is charged
 * (X12): a section of its own, because none of it is work measured against a
 * budget — and it says so out loud, since that is the first thing somebody
 * looking at "Budget used" above it will wonder.
 *
 * Three absences mean three different things and all three are rendered
 * differently: no expenses module at all (`expenseTracking` false) puts nothing
 * here, a caller who may not see the project's money gets no `expenses` block
 * and so nothing here either, and a block with nothing recorded in it is one
 * plain sentence rather than a table of zeroes.
 */
const ExpensesSection = ({ projectId }: { projectId: number }) => {
  const { t, formatters } = useI18n("projects");
  const { data: economy } = useQuery(projectEconomyQueryOptions(projectId));
  const dates = useProjectDates();
  const headingId = useId();
  const { money } = useEconomyFormat(economy?.currency ?? undefined);

  if (!economy?.expenseTracking) return null;
  const expenses = economy.expenses;
  if (!expenses) return null;

  const others = expenses.otherCurrencies ?? [];
  // The ten figures of the project's own currency are one group: a project
  // that carries no currency has none of them, and the block can be empty.
  const buckets = [
    { key: "approved", label: t("expenseStateApproved"), bucket: expenses.approved },
    { key: "submitted", label: t("expenseStateSubmitted"), bucket: expenses.submitted },
    // Rejected expenses are back with the person who recorded them, which is
    // where a draft is; the row says so rather than leaving them unaccounted for.
    { key: "draft", label: t("expenseStateDraft"), bucket: expenses.draft, note: t("expenseDraftIncludesRejected") },
  ];
  const recorded = buckets.some(({ bucket }) => (bucket?.count ?? 0) > 0) || others.length > 0;

  return (
    <Card withBorder padding="lg" radius="md" data-testid="project-expenses">
      <Stack gap="md">
        <Stack gap={2}>
          <Text fw={600} component="h3" id={headingId}>
            {t("expenseCosts")}
          </Text>
          <Text size="sm" c="dimmed">
            {t("expenseCostsDescription")}
          </Text>
        </Stack>

        {!recorded ? (
          <Text size="sm">{t("noExpensesRecorded")}</Text>
        ) : (
          <>
            {expenses.approved && expenses.submitted && expenses.draft && (
              <Table.ScrollContainer minWidth={560}>
                <Table striped aria-labelledby={headingId}>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("status")}</Table.Th>
                      <Table.Th>{t("expenseLines")}</Table.Th>
                      <Table.Th>{t("expenseCostColumn")}</Table.Th>
                      <Table.Th>{t("expensePassedOn")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {buckets.map(({ key, label, bucket, note }) =>
                      bucket === undefined ? null : (
                        <Table.Tr key={key}>
                          <Table.Td>
                            {/* What is approved and what is not is the point of
                                the table, so each state is written out and never
                                left to a colour. */}
                            <Stack gap={2}>
                              <Text size="sm" fw={500}>
                                {label}
                              </Text>
                              {note && (
                                <Text size="xs" c="dimmed">
                                  {note}
                                </Text>
                              )}
                            </Stack>
                          </Table.Td>
                          <Table.Td>
                            <Text size="sm">{formatters.formatNumber(bucket.count)}</Text>
                          </Table.Td>
                          <Table.Td>
                            <Text size="sm">{money(bucket.cost)}</Text>
                          </Table.Td>
                          <Table.Td>
                            <Text size="sm">{money(bucket.amount)}</Text>
                          </Table.Td>
                        </Table.Tr>
                      ),
                    )}
                    {/* Each bucket is rounded on its own, so the three need not
                        add up to the cent: the totals are the server's own
                        across-bucket figures and are never summed here. There
                        is no across-bucket count to publish, hence the dash. */}
                    <Table.Tr data-testid="expense-totals">
                      <Table.Td>
                        <Text size="sm" fw={600}>
                          {t("expenseTotal")}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm">{t("notAvailable")}</Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm" fw={600}>
                          {money(expenses.totalCost)}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm" fw={600}>
                          {money(expenses.totalAmount)}
                        </Text>
                      </Table.Td>
                    </Table.Tr>
                    {/* The supplier invoices' share of the totals above — the
                        server's own figures, never derived here, and a line of
                        its own: the totals are still every expense. */}
                    {expenses.supplierInvoices && (
                      <Table.Tr data-testid="expense-supplier-invoices">
                        <Table.Td>
                          <Text size="sm">{t("expensesOfWhichSupplierInvoices")}</Text>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{formatters.formatNumber(expenses.supplierInvoices.total.count)}</Text>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{money(expenses.supplierInvoices.total.cost)}</Text>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">{money(expenses.supplierInvoices.total.amount)}</Text>
                        </Table.Td>
                      </Table.Tr>
                    )}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            )}

            <Stack gap={4}>
              {/* Nothing ready and nothing invoiced is what the table above
                  already says; the lines are here to name lines that exist. */}
              {(expenses.readyCount ?? 0) > 0 && (
                <Text size="sm">
                  {t("expensesReadyLines", { count: expenses.readyCount ?? 0, amount: money(expenses.readyAmount) })}
                </Text>
              )}
              {(expenses.invoicedCount ?? 0) > 0 && (
                <Text size="sm">
                  {t("expensesInvoicedLines", {
                    count: expenses.invoicedCount ?? 0,
                    amount: money(expenses.invoicedAmount),
                  })}
                </Text>
              )}
              {/* A billable line nobody has priced is in no amount, so the
                  figures say how many lines they are short by — a missing price
                  is not a price of nothing. */}
              {(expenses.unpricedCount ?? 0) > 0 && (
                <Text size="sm" c="dimmed" data-testid="expenses-unpriced-note">
                  {t("expensesUnpriced", { count: expenses.unpricedCount ?? 0 })}
                </Text>
              )}
              {others.map((entry) => (
                <OtherCurrencyNote key={entry.currency} entry={entry} />
              ))}
              {expenses.lastEntryDate && (
                <Text size="sm" c="dimmed">
                  {t("lastExpense", { date: dates.day(expenses.lastEntryDate) })}
                </Text>
              )}
            </Stack>
          </>
        )}

        {/* Somebody reading the budget headline above will ask, so it is
            answered here rather than in a tooltip nobody opens. The field it
            names is interpolated, so the two cannot drift apart. */}
        <Text size="sm" c="dimmed" data-testid="expenses-not-in-budget">
          {t("expensesNotInBudget", { budgetUsed: t("budgetUsed") })}
        </Text>
      </Stack>
    </Card>
  );
};

/**
 * One currency the project itself is not in. Nothing is converted — two
 * currencies added together are a number in neither — so the line is written in
 * the currency it was recorded in and says it stands outside the figures above.
 */
const OtherCurrencyNote = ({ entry }: { entry: EconomyExpenseCurrency }) => {
  const { t } = useI18n("projects");
  const { money } = useEconomyFormat(entry.currency);

  return (
    <Text size="sm" c="dimmed" data-testid="expenses-other-currency-note">
      {t("expensesOtherCurrency", {
        count: entry.count,
        currency: entry.currency,
        cost: money(entry.cost),
        amount: money(entry.amount),
        ready: money(entry.readyAmount),
      })}
    </Text>
  );
};

/**
 * One billing line's budget against its own logged work. A line's percentage
 * is measured against its budget *amount* when it has one, while the hours
 * left over are always hours — so "over budget" and "5 h remaining" can
 * legitimately sit in the same row, and the bar names the basis it used.
 */
const EconomyLineRow = ({ line, currency }: { line: EconomyLine; currency?: string }) => {
  const { t } = useI18n("projects");
  const { hours, money, percent, basisPhrase } = useEconomyFormat(currency);
  const inactive = line.active === false;
  const basis: BudgetBasis | undefined =
    line.budgetAmount != null ? "amount" : line.budgetHours != null ? "hours" : undefined;
  const budget = line.budgetAmount ?? line.budgetHours ?? undefined;

  const budgets: string[] = [];
  if (line.budgetHours != null) budgets.push(hours(line.budgetHours));
  if (line.budgetAmount != null) budgets.push(money(line.budgetAmount));

  return (
    <Table.Tr>
      <Table.Td>
        <Group gap="xs" wrap="nowrap">
          <Text
            size="sm"
            fw={500}
            ff={line.code ? "monospace" : undefined}
            c={inactive ? "dimmed" : undefined}
            data-testid="economy-line-name"
          >
            {/* Work logged against no line at all, and work logged against a
                line this project does not have, share one row. */}
            {line.code ?? t("noBillingLine")}
          </Text>
          {/* Dimming alone is not a state anybody can read, so the row says it. */}
          {inactive && (
            <Badge variant="light" color="gray" size="sm">
              {t("inactive")}
            </Badge>
          )}
        </Group>
      </Table.Td>
      <Table.Td>
        <Text size="sm">{budgets.length > 0 ? budgets.join(" · ") : t("notAvailable")}</Text>
      </Table.Td>
      <Table.Td miw={180}>
        {/* Spec §8: a line without a budget shows its actuals and no bar. A bar
            with nothing to measure against fills its whole width whatever was
            logged, which is exactly what a line at 100 % looks like. */}
        {line.actuals === undefined ? (
          <Text size="sm">{t("notAvailable")}</Text>
        ) : basis === undefined ? (
          <LoggedSplit segments={line.actuals} totalHours={line.actuals.totalHours} />
        ) : (
          <Stack gap={4}>
            <BudgetBar
              size="sm"
              segments={line.actuals}
              basis={basis}
              budget={budget}
              currency={currency}
              overBudget={line.overBudget}
            />
            {/* The bar's label already reads the total out; this copy is for
                eyes only, so a reader is not told the same hours twice. */}
            <Text size="sm" aria-hidden>
              {hours(line.actuals.totalHours)}
            </Text>
          </Stack>
        )}
      </Table.Td>
      <Table.Td>
        <Group gap="xs" wrap="nowrap">
          <Text size="sm">
            {line.usedPercent == null || basis === undefined
              ? t("notAvailable")
              : t("budgetUsedPercentOf", { percent: percent(line.usedPercent), basis: basisPhrase(basis, budget) })}
          </Text>
          {line.overBudget && (
            <Badge variant="light" color="red" size="sm">
              {t("overBudget")}
            </Badge>
          )}
        </Group>
      </Table.Td>
      <Table.Td>
        <Text size="sm">{line.remainingHours != null ? hours(line.remainingHours) : t("notAvailable")}</Text>
      </Table.Td>
    </Table.Tr>
  );
};

const InvoicePlan = ({
  projectId,
  project,
  expensesHref,
}: {
  projectId: number;
  project: Project;
  expensesHref?: string;
}) => {
  const { t } = useI18n("projects");
  const { data: plan, isPending, isError, error } = useQuery(milestonePlanQueryOptions(projectId));
  const [modalState, setModalState] = useState<MilestoneModalState | null>(null);
  const [invoicing, setInvoicing] = useState<BillingMilestone | null>(null);
  const [projectModal, setProjectModal] = useState<ProjectModalState | null>(null);
  const headingId = useId();

  const currency = project.financials?.currency ?? undefined;
  const fixedPrice = project.financials?.fixedPriceAmount ?? undefined;
  const canManage = project.capabilities.canManageMilestones;
  const milestones = plan?.milestones ?? [];
  // Cancelled rows are listed last but keep their stored numbers, so the plan's
  // order is the array's order and the reordering targets are read off the
  // rows that are still open.
  const open = milestones.filter((milestone) => milestone.status !== "cancelled");

  return (
    <>
      {plan && <HeadlineFigures totals={plan.totals} />}

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group justify="space-between" wrap="wrap">
            <Stack gap={2}>
              <Text fw={600} component="h3" id={headingId}>
                {t("invoicePlan")}
              </Text>
              <Text size="sm" c="dimmed">
                {t("invoicePlanDescription")}
              </Text>
            </Stack>
            {canManage && currency && (
              <Button size="xs" leftSection={<IconPlus size={14} />} onClick={() => setModalState({ mode: "create" })}>
                {t("addMilestone")}
              </Button>
            )}
          </Group>

          {!currency && (
            <Alert color="yellow" icon={<IconInfoCircle size={16} />}>
              <Group justify="space-between" wrap="wrap" gap="xs">
                <Text size="sm">{t("milestonesNeedCurrency")}</Text>
                {project.capabilities.canManage && (
                  <Button size="compact-xs" variant="light" onClick={() => setProjectModal({ mode: "edit", project })}>
                    {t("editTheProject")}
                  </Button>
                )}
              </Group>
            </Alert>
          )}

          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadInvoicePlan")}>
              {error.message}
            </Alert>
          )}
          {isPending && <ContentSkeleton rows={3} rowHeight={48} />}
          {plan && milestones.length === 0 && <EmptyState title={t("noMilestones")} size="sm" />}

          {milestones.length > 0 && (
            <Table.ScrollContainer minWidth={760}>
              <Table striped highlightOnHover aria-labelledby={headingId}>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("milestoneName")}</Table.Th>
                    <Table.Th>{t("plannedDate")}</Table.Th>
                    <Table.Th>{t("amount")}</Table.Th>
                    <Table.Th>{t("status")}</Table.Th>
                    <Table.Th>{t("actions")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {milestones.map((milestone) => {
                    const at = open.indexOf(milestone);
                    return (
                      <MilestoneRow
                        key={milestone.id}
                        milestone={milestone}
                        canManage={canManage}
                        moveUpTo={at > 0 ? open[at - 1].position : undefined}
                        moveDownTo={at >= 0 && at < open.length - 1 ? open[at + 1].position : undefined}
                        onEdit={setModalState}
                        onInvoice={setInvoicing}
                      />
                    );
                  })}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}

          <ReadyExpenses projectId={projectId} expensesHref={expensesHref} />

          {plan && <PlanFooter totals={plan.totals} />}
        </Stack>
      </Card>

      <MilestoneFormModal
        projectId={projectId}
        fixedPrice={fixedPrice}
        currency={currency}
        state={modalState}
        onClose={() => setModalState(null)}
      />
      <MilestoneInvoicedModal milestone={invoicing} onClose={() => setInvoicing(null)} />
      <ProjectFormModal state={projectModal} onClose={() => setProjectModal(null)} />
    </>
  );
};

/**
 * The billable expenses that can go on an invoice today, beside the milestones
 * that can: approved, billable, priced and not yet invoiced, in the project's
 * own currency. They are not milestones and never join the plan's own totals —
 * this is the one line that says somebody planning an invoice has more to put
 * on it than the rows above.
 */
const ReadyExpenses = ({ projectId, expensesHref }: { projectId: number; expensesHref?: string }) => {
  const { t } = useI18n("projects");
  const Link = useShellLink();
  const { data: economy } = useQuery(projectEconomyQueryOptions(projectId));
  const { money } = useEconomyFormat(economy?.currency ?? undefined);

  const expenses = economy?.expenseTracking ? economy.expenses : undefined;
  const count = expenses?.readyCount ?? 0;
  if (count <= 0) return null;

  return (
    <Alert color="teal" variant="light" icon={<IconInfoCircle size={16} />} data-testid="expenses-ready-to-invoice">
      <Group justify="space-between" wrap="wrap" gap="xs">
        <Text size="sm">{t("expensesReadyToInvoiceRow", { count, amount: money(expenses?.readyAmount) })}</Text>
        {expensesHref &&
          (Link ? (
            <Anchor size="sm" renderRoot={(props) => <Link to={expensesHref} {...props} />}>
              {t("viewTheExpenses")}
            </Anchor>
          ) : (
            <Anchor size="sm" href={expensesHref}>
              {t("viewTheExpenses")}
            </Anchor>
          ))}
      </Group>
    </Alert>
  );
};

/** What the plan stands at, in the three figures somebody planning invoices reads first. */
const HeadlineFigures = ({ totals }: { totals: BillingMilestoneTotals }) => {
  const { t } = useI18n("projects");
  const { money } = useEconomyFormat(totals.currency ?? undefined);

  return (
    <Card withBorder padding="lg" radius="md" data-testid="milestone-totals">
      <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="md">
        <Field label={t("plannedTotal")}>{money(totals.planned)}</Field>
        <Field label={t("readyTotal")}>{money(totals.ready)}</Field>
        <Field label={t("invoicedTotal")}>{money(totals.invoiced)}</Field>
      </SimpleGrid>
    </Card>
  );
};

/**
 * The fixed price the plan is read against, and the one sentence that says how
 * far off it is. Over-planning is a thing to look at rather than a failure, so
 * it is a quiet note and never an error.
 */
const PlanFooter = ({ totals }: { totals: BillingMilestoneTotals }) => {
  const { t } = useI18n("projects");
  const { money } = useEconomyFormat(totals.currency ?? undefined);
  if (totals.fixedPrice === undefined || totals.fixedPrice === null) return null;

  return (
    <Stack gap="xs" data-testid="milestone-plan-footer">
      <Group gap="xs">
        <Text size="sm" c="dimmed">
          {t("fixedPriceAmount")}
        </Text>
        <Text size="sm" fw={600}>
          {money(totals.fixedPrice)}
        </Text>
      </Group>
      {totals.unplanned !== undefined && totals.unplanned !== null && (
        <Text size="sm" c="dimmed">
          {t("milestonesUnplanned", { amount: money(totals.unplanned) })}
        </Text>
      )}
      {totals.overPlanned !== undefined && totals.overPlanned !== null && (
        <Alert color="yellow" icon={<IconInfoCircle size={16} />} data-testid="over-planned-note">
          {t("milestonesOverPlanned", { amount: money(totals.overPlanned) })}
        </Alert>
      )}
    </Stack>
  );
};

/** How a milestone is priced, in words: a flat amount, or a share and what it comes to. */
const MilestoneAmount = ({ milestone }: { milestone: BillingMilestone }) => {
  const { t, formatters } = useI18n("projects");
  // A flat amount remembers the currency it was entered in, so each row reads
  // in its own rather than in the plan's current one.
  const { money } = useEconomyFormat(milestone.currency ?? undefined);
  const amount = money(milestone.effectiveAmount);
  if (milestone.percent === undefined || milestone.percent === null) return <Text size="sm">{amount}</Text>;
  return (
    <Text size="sm">
      {t("milestonePercentAmount", { percent: formatters.formatNumber(milestone.percent), amount })}
    </Text>
  );
};

const MilestoneRow = ({
  milestone,
  canManage,
  moveUpTo,
  moveDownTo,
  onEdit,
  onInvoice,
}: {
  milestone: BillingMilestone;
  /** The project's `capabilities.canManageMilestones` — who may reorder the plan. */
  canManage: boolean;
  /** The position a "move up" asks for, absent when the row is already first among the open ones. */
  moveUpTo?: number;
  moveDownTo?: number;
  onEdit: (state: MilestoneModalState) => void;
  onInvoice: (milestone: BillingMilestone) => void;
}) => {
  const { t } = useI18n("projects");
  const dates = useProjectDates();
  const cancelled = milestone.status === "cancelled";
  const actions = useMilestoneActions(milestone);

  const items: ReactNode[] = [];
  const can = milestone.capabilities;
  if (can.canEdit) {
    items.push(
      <Menu.Item key="edit" onClick={() => onEdit({ mode: "edit", milestone })}>
        {t("edit")}
      </Menu.Item>,
    );
  }
  // Reordering is the project's right, not the milestone's, and a cancelled
  // row has no place in the order to move within.
  if (canManage && !cancelled && moveUpTo !== undefined) {
    items.push(
      <Menu.Item key="up" onClick={() => actions.move(moveUpTo)}>
        {t("moveMilestoneUp")}
      </Menu.Item>,
    );
  }
  if (canManage && !cancelled && moveDownTo !== undefined) {
    items.push(
      <Menu.Item key="down" onClick={() => actions.move(moveDownTo)}>
        {t("moveMilestoneDown")}
      </Menu.Item>,
    );
  }
  if (can.canMarkReady) {
    items.push(
      <Menu.Item key="ready" onClick={() => actions.moveTo("ready")}>
        {t("markMilestoneReady")}
      </Menu.Item>,
    );
  }
  if (can.canMarkPlanned) {
    items.push(
      <Menu.Item key="planned" onClick={() => actions.moveTo("planned")}>
        {t("markMilestonePlanned")}
      </Menu.Item>,
    );
  }
  if (can.canMarkInvoiced) {
    items.push(
      <Menu.Item key="invoiced" onClick={() => onInvoice(milestone)}>
        {t("markMilestoneInvoiced")}
      </Menu.Item>,
    );
  }
  if (can.canUndoInvoiced) {
    items.push(
      <Menu.Item key="undo" onClick={actions.confirmUndo}>
        {t("undoMilestoneInvoiced")}
      </Menu.Item>,
    );
  }
  if (can.canReopen) {
    items.push(
      <Menu.Item key="reopen" onClick={() => actions.moveTo("planned")}>
        {t("reopenMilestone")}
      </Menu.Item>,
    );
  }
  if (can.canCancel) {
    items.push(
      <Menu.Item key="cancel" color="red" onClick={actions.confirmCancel}>
        {t("cancelMilestone")}
      </Menu.Item>,
    );
  }
  if (can.canDelete) {
    items.push(
      <Menu.Item key="delete" color="red" onClick={actions.confirmDelete}>
        {t("deleteMilestone")}
      </Menu.Item>,
    );
  }

  return (
    <Table.Tr>
      <Table.Td>
        {/* The description is written out rather than hidden in a tooltip: a
            viewer with financial rights may not edit a milestone, so the row
            is the only place they would ever read what it is for. */}
        <Stack gap={2}>
          <Text
            size="sm"
            fw={500}
            data-testid="milestone-name"
            td={cancelled ? "line-through" : undefined}
            c={cancelled ? "dimmed" : undefined}
          >
            {milestone.name}
          </Text>
          {milestone.description && (
            <Text size="xs" c="dimmed" lineClamp={2} maw={360}>
              {milestone.description}
            </Text>
          )}
        </Stack>
      </Table.Td>
      <Table.Td>
        <Group gap="xs" wrap="nowrap">
          <Text size="sm">{milestone.plannedDate ? dates.day(milestone.plannedDate) : t("notAvailable")}</Text>
          {milestone.overdue && (
            <Badge variant="light" color="red" size="sm">
              {t("overdue")}
            </Badge>
          )}
        </Group>
      </Table.Td>
      <Table.Td>
        <MilestoneAmount milestone={milestone} />
      </Table.Td>
      <Table.Td>
        <Badge variant="light" color={milestoneStatusColor(milestone.status)}>
          {t(milestoneStatusLabelKey(milestone.status))}
        </Badge>
      </Table.Td>
      <Table.Td>
        {items.length > 0 && (
          <Menu position="bottom-end" withinPortal>
            <Menu.Target>
              {/* Named after its own row: a reader listing the page's buttons
                  gets one per milestone, not six of the same. */}
              <ActionIcon
                variant="subtle"
                aria-label={t("milestoneActionsFor", { name: milestone.name })}
                loading={actions.isPending}
              >
                <IconDots size={16} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>{items}</Menu.Dropdown>
          </Menu>
        )}
      </Table.Td>
    </Table.Tr>
  );
};

/**
 * Every write one row can make. A refused status move arrives as a 400 on
 * `status` and a revision conflict as the module's own project wording, so
 * both are turned into something a reader of this plan can act on rather than
 * being swallowed.
 */
const useMilestoneActions = (milestone: BillingMilestone) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();

  const report = (title: string) => (error: Error) => {
    if (error instanceof ApiValidationError) {
      const message = error.fieldErrors.status ?? Object.values(error.fieldErrors)[0] ?? error.message;
      notifications.show({ color: "red", title, message });
      return;
    }
    const conflict = (error as ApiError).status === 409;
    notifications.show({ color: "red", title, message: conflict ? t("milestoneChangedElsewhere") : error.message });
  };

  const done = (title: string) => () => {
    queryClient.invalidateQueries({ queryKey: ["projects"] });
    notifications.show({ color: "teal", title, message: milestone.name });
  };

  const status = useMutation({
    mutationFn: (to: MilestoneStatus) => setMilestoneStatus(milestone.id, { status: to, revision: milestone.revision }),
    onSuccess: done(t("milestoneUpdated")),
    onError: report(t("couldNotChangeMilestone")),
  });

  const move = useMutation({
    mutationFn: (position: number) => moveMilestone(milestone.id, { position, revision: milestone.revision }),
    onSuccess: done(t("planReordered")),
    onError: report(t("couldNotReorderPlan")),
  });

  const remove = useMutation({
    mutationFn: () => deleteMilestone(milestone.id),
    onSuccess: done(t("milestoneDeleted")),
    onError: report(t("couldNotDeleteMilestone")),
  });

  return {
    isPending: status.isPending || move.isPending || remove.isPending,
    moveTo: (to: MilestoneStatus) => status.mutate(to),
    move: (position: number) => move.mutate(position),
    confirmCancel: () =>
      modals.openConfirmModal({
        title: t("cancelMilestoneTitle", { name: milestone.name }),
        children: <Text size="sm">{t("cancelMilestoneWarning")}</Text>,
        labels: { confirm: t("cancelMilestone"), cancel: t("cancel") },
        confirmProps: { color: "red" },
        onConfirm: () => status.mutate("cancelled"),
      }),
    confirmUndo: () =>
      modals.openConfirmModal({
        title: t("undoMilestoneInvoicedTitle", { name: milestone.name }),
        children: <Text size="sm">{t("undoMilestoneInvoicedWarning")}</Text>,
        labels: { confirm: t("undoMilestoneInvoiced"), cancel: t("cancel") },
        onConfirm: () => status.mutate("ready"),
      }),
    confirmDelete: () =>
      modals.openConfirmModal({
        title: t("deleteMilestoneTitle", { name: milestone.name }),
        children: <Text size="sm">{t("deleteMilestoneWarning")}</Text>,
        labels: { confirm: t("deleteMilestone"), cancel: t("cancel") },
        confirmProps: { color: "red" },
        onConfirm: () => remove.mutate(),
      }),
  };
};
