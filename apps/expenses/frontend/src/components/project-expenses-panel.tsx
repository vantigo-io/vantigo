import {
  Alert,
  Anchor,
  Box,
  Button,
  Card,
  Chip,
  Group,
  Pagination,
  SimpleGrid,
  Stack,
  Table,
  Text,
  UnstyledButton,
} from "@mantine/core";
import { IconAlertCircle, IconPlus, IconReceipt } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n, useShellLink } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type Expense, expensesQueryOptions } from "../api/entries";
import { expensesMetaQueryOptions } from "../api/meta";
import {
  type ProjectExpensesBucket,
  type ProjectExpensesCurrency,
  projectExpensesSummaryQueryOptions,
} from "../api/project-expenses";
import { NotFoundError } from "../api/request";
import { EntryDrawer } from "../pages/-entry-drawer";
import { ExpenseFormModal, type ExpenseModalState } from "../pages/-expense-form-modal";
import { ExpenseStatusBadge } from "./expense-status-badge";
import "../i18n";
import { useExpenseFormat } from "../lib/format";
import { useProjectOptions } from "../lib/project-options";
import { claimHref } from "../lib/routes";
import { expenseKindLabelKey } from "../lib/status";

/** Which expenses the list is asked for. `all` sends no filter at all. */
type ProjectExpenseFilter = "all" | "ready";

export interface ProjectExpensesPanelProps {
  /** The project the page is on. */
  projectId: number;
  /**
   * Called after anything that moves the figures — a cost recorded, a line
   * priced, approved, marked invoiced or un-invoiced. This package's own
   * queries are refreshed by the writes themselves; this is for the **host**,
   * which is the only place that may reach another module's cache (the
   * project's Economy tab reads the same expenses from the projects API).
   */
  onChanged?: () => void;
}

/**
 * The project page's Expenses tab (design §8 / delivery C): what one project's
 * expenses cost it and bill its customer, and the expenses behind those
 * figures that this caller may open.
 *
 * **The totals and the rows are gated differently, on purpose.** The summary
 * is the project's money and follows financial rights on the project; the list
 * is individual expenses and keeps the visibility rule it has always had.
 * Either half can therefore be empty while the other is not, and the panel
 * says which rather than showing a blank table that looks like a project with
 * nothing on it. A summary that answers a bare **404** means only "these
 * figures are not yours" — one indistinguishable answer for an installation
 * without projects, an unknown project and a caller without the right — so the
 * block is simply absent and nothing is shown in red.
 *
 * It is mounted by the host **outside** the expenses route tree, so it keeps
 * its filter and its page in local state rather than in the URL, and links
 * through the shell's link component rather than this package's router.
 */
export const ProjectExpensesPanel = ({ projectId, onChanged }: ProjectExpensesPanelProps) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const Link = useShellLink();
  const [filter, setFilter] = useState<ProjectExpenseFilter>("all");
  const [page, setPage] = useState(1);
  const [opened, setOpened] = useState<Expense | null>(null);
  const [recording, setRecording] = useState<ExpenseModalState | null>(null);

  const { data: meta } = useQuery(expensesMetaQueryOptions());
  // Without the projects module an expense is booked on nothing: there is no
  // project read to make, and the host would not have mounted this at all.
  const projectsOff = meta?.projectsAvailable === false;

  const summary = useQuery({ ...projectExpensesSummaryQueryOptions(projectId), enabled: !projectsOff });
  const totals = summary.data;
  const summaryMissing = summary.error instanceof NotFoundError;

  const list = useQuery({
    ...expensesQueryOptions({ projectId, page, ...(filter === "ready" ? { toInvoice: true } : {}) }),
    enabled: !projectsOff,
  });

  // The projects this caller may book on. It is **not** where the project's
  // own currency comes from — that is on the summary, because this list is
  // empty for a finance reader who is on no project team — only where the
  // billing lines a new cost may be booked against come from.
  const { projects } = useProjectOptions(undefined, !projectsOff);
  const bookable = projects?.find((project) => project.id === projectId);
  /**
   * Who may record a cost. When the summary was readable its own
   * `capabilities.canRecord` decides and nothing here re-derives it; when it
   * was not, the project being among the caller's bookable ones is the same
   * question asked of the endpoint that can still answer it.
   */
  const mayRecord = totals ? totals.capabilities.canRecord : summaryMissing;
  const showRecord = mayRecord && bookable !== undefined;

  const currencies = totals?.currencies ?? [];
  const own = totals?.projectCurrency;
  // The project's own currency first: it is the one whose figures are the
  // project's economy. Everything else is reported beside it, never into it.
  const ordered = own
    ? [...currencies.filter((one) => one.currency === own), ...currencies.filter((one) => one.currency !== own)]
    : currencies;

  const rows = list.data?.data ?? [];
  const listed = list.data?.pagination.totalCount;
  /**
   * What the totals say there are, under the same filter the list is asking
   * with — so the two figures compared are always about the same set.
   */
  const counted = totals
    ? currencies.reduce((sum, one) => sum + (filter === "ready" ? one.readyCount : one.total.count), 0)
    : undefined;
  const hidden = counted !== undefined && listed !== undefined && listed < counted;

  const onFilter = (next: string) => {
    setFilter(next === "ready" ? "ready" : "all");
    setPage(1);
  };

  if (projectsOff) {
    return (
      <Box mt="md">
        <EmptyState
          icon={IconReceipt}
          title={t("projectsNotEnabledHere")}
          description={t("projectsNotEnabledHereDescription")}
        />
      </Box>
    );
  }

  return (
    <Stack gap="md" mt="md">
      {summary.isError && !summaryMissing && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProjectTotals")}>
          {summary.error.message}
        </Alert>
      )}

      {totals && currencies.length > 0 && (
        <Card withBorder padding="lg" radius="md" data-testid="project-expense-totals">
          <Stack gap="md">
            <Stack gap={0}>
              <Text fw={600} component="h3">
                {t("projectExpenseTotals")}
              </Text>
              <Text size="sm" c="dimmed">
                {t("projectExpenseTotalsDescription")}
              </Text>
            </Stack>
            <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
              {ordered.map((one) => (
                <CurrencyCard key={one.currency} figures={one} projectCurrency={own} />
              ))}
            </SimpleGrid>
            {totals.lastEntryDate && (
              <Text size="sm" c="dimmed">
                {t("lastExpenseOn", { date: format.date(totals.lastEntryDate) })}
              </Text>
            )}
          </Stack>
        </Card>
      )}

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group justify="space-between" align="start" wrap="wrap">
            <Stack gap={0}>
              <Text fw={600} component="h3">
                {t("projectExpenses")}
              </Text>
              <Text size="sm" c="dimmed">
                {t("projectExpensesDescription")}
              </Text>
            </Stack>
            {showRecord && (
              <Button
                h={40}
                leftSection={<IconPlus size={16} />}
                onClick={() => setRecording({ mode: "create", project: bookable })}
              >
                {t("recordACost")}
              </Button>
            )}
          </Group>

          {/* Offered only when the totals were readable: "ready to invoice" is
              a figure from the summary, and a filter nothing on screen can be
              checked against is a filter nobody can trust. It never travels
              with a status or a kind — the API refuses those combinations
              rather than answering an empty page — and "All" sends no filter
              at all. */}
          {totals && (
            <Chip.Group value={filter} onChange={(value) => onFilter(String(value))}>
              <Group gap="xs" role="group" aria-label={t("projectExpenseFilters")}>
                <Chip value="all" size="lg" styles={{ label: { height: 40 } }}>
                  {t("filterAllExpenses")}
                </Chip>
                <Chip value="ready" size="lg" styles={{ label: { height: 40 } }}>
                  {t("readyToInvoice")}
                </Chip>
              </Group>
            </Chip.Group>
          )}

          {list.isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProjectExpenses")}>
              {list.error.message}
            </Alert>
          )}
          {list.isPending && <ContentSkeleton rows={4} rowHeight={52} />}

          {list.data && (
            <>
              {rows.length > 0 && (
                <Table.ScrollContainer minWidth={900}>
                  <Table striped highlightOnHover aria-label={t("projectExpenses")}>
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>{t("date")}</Table.Th>
                        <Table.Th>{t("description")}</Table.Th>
                        <Table.Th>{t("kind")}</Table.Th>
                        <Table.Th>{t("owner")}</Table.Th>
                        <Table.Th>{t("amount")}</Table.Th>
                        <Table.Th>{t("status")}</Table.Th>
                        <Table.Th>{t("receipts")}</Table.Th>
                        <Table.Th>{t("rowActions")}</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {rows.map((expense) => (
                        <Table.Tr key={expense.id} data-expense={expense.id}>
                          <Table.Td>{format.date(expense.entryDate)}</Table.Td>
                          <Table.Td>
                            <UnstyledButton
                              aria-label={t("openExpense", { description: expense.description })}
                              onClick={() => setOpened(expense)}
                            >
                              <Text size="sm" td="underline">
                                {expense.description}
                              </Text>
                            </UnstyledButton>
                          </Table.Td>
                          <Table.Td>
                            <Stack gap={0}>
                              <Text size="sm">{t(expenseKindLabelKey(expense.kind))}</Text>
                              {expense.claimId !== undefined && (
                                <Text size="xs" c="dimmed">
                                  {t("partOfTravelClaim")}
                                </Text>
                              )}
                            </Stack>
                          </Table.Td>
                          <Table.Td>
                            <Text size="sm">{expense.owner.displayName}</Text>
                          </Table.Td>
                          <Table.Td>{format.money(expense.grossAmount, expense.currency)}</Table.Td>
                          <Table.Td>
                            <ExpenseStatusBadge status={expense.status} size="sm" />
                          </Table.Td>
                          <Table.Td>
                            {expense.kind === "mileage" ? (
                              <Text size="xs" c="dimmed">
                                {t("notAvailable")}
                              </Text>
                            ) : expense.attachmentCount === 0 ? (
                              <Text size="xs" c="orange">
                                {t("receiptMissing")}
                              </Text>
                            ) : (
                              <Text size="xs">
                                {expense.attachmentCount === 1
                                  ? t("oneReceipt")
                                  : t("receiptCount", { count: expense.attachmentCount })}
                              </Text>
                            )}
                          </Table.Td>
                          <Table.Td>
                            {/* A trip's line is the trip's: it is decided,
                                submitted and paid there, so the row points at
                                the trip rather than pretending it stands
                                alone. */}
                            {expense.claimId !== undefined && <ClaimLink claimId={expense.claimId} Link={Link} />}
                          </Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </Table.ScrollContainer>
              )}

              {rows.length > 0 && hidden && (
                <Text size="sm" c="dimmed" data-testid="project-expenses-partial">
                  {t("totalsCoverMoreThanTheList")}
                </Text>
              )}

              {rows.length === 0 &&
                (hidden ? (
                  // The normal state for somebody who may read a project's
                  // money without managing it: every figure above, and not one
                  // of the receipts behind them. Said in words, because an
                  // empty table here would read as a project nobody has spent
                  // anything on.
                  <Box data-testid="project-expenses-hidden">
                    <EmptyState icon={IconReceipt} title={t("totalsButNotTheExpenses")} />
                  </Box>
                ) : filter === "ready" ? (
                  <EmptyState
                    icon={IconReceipt}
                    title={t("nothingReadyToInvoice")}
                    description={t("nothingReadyToInvoiceDescription")}
                  />
                ) : (
                  <EmptyState
                    icon={IconReceipt}
                    title={t("noProjectExpenses")}
                    description={t("noProjectExpensesDescription")}
                  />
                ))}

              {list.data.pagination.totalPages > 1 && (
                <Group justify="center">
                  <Pagination total={list.data.pagination.totalPages} value={page} onChange={setPage} />
                </Group>
              )}
            </>
          )}
        </Stack>
      </Card>

      <EntryDrawer expense={opened} onClose={() => setOpened(null)} onChanged={onChanged} />
      <ExpenseFormModal state={recording} onClose={() => setRecording(null)} onSaved={() => onChanged?.()} />
    </Stack>
  );
};

const ClaimLink = ({ claimId, Link }: { claimId: number; Link: ReturnType<typeof useShellLink> }) => {
  const { t } = useI18n("expenses");
  const href = claimHref(claimId);
  return Link ? (
    <Anchor size="sm" renderRoot={(props) => <Link to={href} {...props} />}>
      {t("openTheTravelClaim")}
    </Anchor>
  ) : (
    <Anchor size="sm" href={href}>
      {t("openTheTravelClaim")}
    </Anchor>
  );
};

/**
 * One currency's figures. The three buckets are written out with the words
 * that say what they are **not** — a reader who cannot open the expenses
 * themselves has only this to go on — and the footer row is the published
 * `total`, never the three added up: each was rounded once on its own.
 */
const CurrencyCard = ({
  figures,
  projectCurrency,
}: {
  figures: ProjectExpensesCurrency;
  projectCurrency: string | undefined;
}) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const currency = figures.currency;
  const title = !projectCurrency
    ? currency
    : currency === projectCurrency
      ? t("projectOwnCurrency", { currency })
      : t("anotherCurrency", { currency });
  const count = (value: number) => (value === 1 ? t("oneExpense") : t("countOfExpenses", { count: value }));
  const bucket = (label: string, one: ProjectExpensesBucket) => (
    <Table.Tr>
      <Table.Td>{label}</Table.Td>
      <Table.Td ta="right">{count(one.count)}</Table.Td>
      <Table.Td ta="right">{format.money(one.cost, currency)}</Table.Td>
      <Table.Td ta="right">{format.money(one.billAmount, currency)}</Table.Td>
    </Table.Tr>
  );

  return (
    <Card withBorder padding="md" radius="md" data-testid={`project-expense-currency-${currency}`}>
      <Stack gap="sm">
        <Text fw={600} component="h4" size="sm">
          {title}
        </Text>
        <Table verticalSpacing="xs" aria-label={`${t("projectExpenseTotals")} — ${currency}`}>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("status")}</Table.Th>
              <Table.Th ta="right">{t("expenses")}</Table.Th>
              <Table.Th ta="right">{t("expenseCost")}</Table.Th>
              <Table.Th ta="right">{t("toTheCustomer")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {bucket(t("bucketApproved"), figures.approved)}
            {bucket(t("bucketSubmitted"), figures.submitted)}
            {bucket(t("bucketDraft"), figures.draft)}
          </Table.Tbody>
          <Table.Tfoot>{bucket(t("bucketTotal"), figures.total)}</Table.Tfoot>
        </Table>

        <Group gap="xl" wrap="wrap">
          <Stack gap={0}>
            <Text size="xs" c="dimmed">
              {t("readyToInvoice")}
            </Text>
            <Text fw={600} size="sm">
              {t("countAndAmount", { count: figures.readyCount, amount: format.money(figures.readyAmount, currency) })}
            </Text>
          </Stack>
          <Stack gap={0}>
            <Text size="xs" c="dimmed">
              {t("invoicedAlready")}
            </Text>
            <Text fw={600} size="sm">
              {t("countAndAmount", {
                count: figures.invoicedCount,
                amount: format.money(figures.invoicedAmount, currency),
              })}
            </Text>
          </Stack>
        </Group>

        {/* A billable line with no price is counted, never billed as zero: a
            panel that ignored it would show a project billing less than it
            will. Per diem days are never billable and are never in it. */}
        {figures.unpricedCount > 0 && (
          <Text size="sm" c="orange">
            {figures.unpricedCount === 1
              ? t("oneUnpricedExpense")
              : t("unpricedExpenses", { count: figures.unpricedCount })}
          </Text>
        )}
      </Stack>
    </Card>
  );
};
