import {
  Alert,
  Anchor,
  Badge,
  Card,
  Group,
  Pagination,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { IconAlertCircle, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import {
  ContentSkeleton,
  EmptyState,
  KpiCard,
  PageHeader,
  useDebouncedListSearch,
  useI18n,
  useShellLink,
} from "@vantigo/frontend-shell";
import { type EconomyRow, economyPortfolioQueryOptions } from "../api/economy";
import { ApiValidationError } from "../api/request";
import { BudgetBar } from "../components/budget-bar";
import { CustomerPicker } from "../components/customer-picker";
import { LoggedSplit } from "../components/logged-split";
import { ProjectStatusBadge } from "../components/project-status-badge";
import "../i18n";
import { useProjectDates } from "../lib/dates";
import {
  type EconomyPortfolioSearch,
  economySortLabelKey,
  economySorts,
  isEconomySort,
  useEconomyFormat,
} from "../lib/economy";
import { isProjectStatus, projectStatuses, projectStatusLabelKey } from "../lib/status";

/**
 * The economy across the projects whose money the caller may see (design §7):
 * how much of each budget the logged work has used, what that work is worth,
 * and what is waiting to be invoiced — with every filter, the order and the
 * page held in the URL, so a portfolio worth looking at can be sent to
 * somebody else as a link.
 *
 * The API decides the rows: every project here is one the caller has financial
 * rights on, so nothing in a row is shaped away and there is no "you may not
 * see this" state to render — a caller who may see none of them gets an empty
 * page saying so.
 */
export const EconomyPortfolio = () => {
  const { t, formatters } = useI18n("projects");
  const params = useSearch({ strict: false }) as EconomyPortfolioSearch;
  const navigate = useNavigate() as (options: unknown) => void;

  const { searchInput, setSearchInput, onPageChange } = useDebouncedListSearch({
    currentSearch: params.search,
    onNavigate: (next, options) => navigate({ search: { ...params, ...next }, ...options }),
  });
  const filterBy = (next: Partial<EconomyPortfolioSearch>) => navigate({ search: { ...params, ...next, page: 1 } });

  const { data, isPending, isError, error } = useQuery(economyPortfolioQueryOptions(params));

  // Too many projects for one answer comes back as a 400 naming `status`. Its
  // message is the only thing that says what to narrow, so it is what the page
  // shows.
  const refusal = isError && error instanceof ApiValidationError ? Object.values(error.fieldErrors)[0] : undefined;

  const readyAmounts = (data?.totals.readyAmounts ?? [])
    .map((ready) => formatters.formatCurrency(ready.amount, ready.currency))
    .join(" · ");

  return (
    <Stack gap="lg">
      <PageHeader title={t("economyPortfolio")} description={t("economyPortfolioDescription")} />

      {data && (
        <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="sm" data-testid="economy-portfolio-totals">
          <KpiCard label={t("projects")} value={formatters.formatNumber(data.totals.projectCount)} />
          <KpiCard label={t("overBudget")} value={formatters.formatNumber(data.totals.overBudgetCount)} />
          <KpiCard
            label={t("readyTotal")}
            value={formatters.formatNumber(data.totals.readyCount)}
            hint={readyAmounts || undefined}
          />
        </SimpleGrid>
      )}

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group align="end" wrap="wrap">
            <TextInput
              placeholder={t("searchProjects")}
              aria-label={t("searchProjects")}
              leftSection={<IconSearch size={16} />}
              value={searchInput}
              onChange={(event) => setSearchInput(event.currentTarget.value)}
              maw={320}
            />
            <Select
              label={t("status")}
              w={170}
              allowDeselect={false}
              data={[
                { value: "all", label: t("allStatuses") },
                ...projectStatuses.map((status) => ({ value: status, label: t(projectStatusLabelKey(status)) })),
              ]}
              value={params.status}
              onChange={(value) =>
                filterBy({ status: value === "all" || (value && isProjectStatus(value)) ? value : "active" })
              }
            />
            <CustomerPicker
              label={t("customer")}
              placeholder={t("allCustomers")}
              w={220}
              value={params.customerId ?? null}
              onChange={(value) => filterBy({ customerId: typeof value === "number" ? value : undefined })}
            />
            <Select
              label={t("sortBy")}
              w={220}
              allowDeselect={false}
              data={economySorts.map((sort) => ({ value: sort, label: t(economySortLabelKey(sort)) }))}
              value={params.sort}
              onChange={(value) => filterBy({ sort: value && isEconomySort(value) ? value : "budgetUsed" })}
            />
            <Switch
              label={t("onlyOverBudget")}
              checked={params.overBudget}
              onChange={() => filterBy({ overBudget: !params.overBudget })}
            />
            <Switch
              label={t("onlyWithReadyMilestones")}
              checked={params.hasReady}
              onChange={() => filterBy({ hasReady: !params.hasReady })}
            />
          </Group>

          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadEconomyPortfolio")}>
              {refusal ?? error.message}
            </Alert>
          )}

          {isPending && <ContentSkeleton rows={6} rowHeight={52} />}

          {data && !data.timeTracking && (
            <Text size="sm" c="dimmed" data-testid="time-tracking-off">
              {t("timeTrackingOff")}
            </Text>
          )}

          {data && (
            <>
              {/* Eight empty column headers above an empty state say nothing. */}
              {data.data.length > 0 && (
                <Table.ScrollContainer minWidth={1100}>
                  <Table striped highlightOnHover aria-label={t("economyPortfolio")}>
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>{t("project")}</Table.Th>
                        <Table.Th>{t("customer")}</Table.Th>
                        <Table.Th>{t("status")}</Table.Th>
                        <Table.Th>{t("budgetUsed")}</Table.Th>
                        <Table.Th>{t("valueOfWork")}</Table.Th>
                        <Table.Th>{t("pendingHours")}</Table.Th>
                        <Table.Th>{t("nextMilestone")}</Table.Th>
                        <Table.Th>{t("readyTotal")}</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {data.data.map((project) => (
                        <PortfolioRow key={project.project.id} row={project} />
                      ))}
                    </Table.Tbody>
                  </Table>
                </Table.ScrollContainer>
              )}

              {data.data.length === 0 && <EmptyState title={t("noEconomyProjects")} />}

              {data.pagination.totalPages > 1 && (
                <Group justify="center">
                  <Pagination total={data.pagination.totalPages} value={params.page} onChange={onPageChange} />
                </Group>
              )}
            </>
          )}
        </Stack>
      </Card>
    </Stack>
  );
};

const PortfolioRow = ({ row }: { row: EconomyRow }) => {
  const { t } = useI18n("projects");
  const dates = useProjectDates();
  const Link = useShellLink();
  const currency = row.currency ?? undefined;
  const { hours, money, percent, basisPhrase } = useEconomyFormat(currency);
  const to = `/projects/${row.project.id}/economy`;
  const milestone = row.nextMilestone;

  return (
    <Table.Tr>
      <Table.Td>
        <Stack gap={2}>
          {Link ? (
            <Anchor ff="monospace" fw={600} renderRoot={(props) => <Link to={to} {...props} />}>
              {row.project.code}
            </Anchor>
          ) : (
            <Anchor href={to} ff="monospace" fw={600}>
              {row.project.code}
            </Anchor>
          )}
          <Text size="sm">{row.project.name}</Text>
        </Stack>
      </Table.Td>
      <Table.Td>
        <Text size="sm">{row.project.customer?.name ?? t("notAvailable")}</Text>
      </Table.Td>
      <Table.Td>
        <ProjectStatusBadge status={row.project.status} />
      </Table.Td>
      <Table.Td miw={200}>
        {/* The row carries the basis but not the number behind it, so the bar
            is drawn against its own total and the basis is named in words. With
            no basis at all there is no bar — a full-width one would read as
            "all of it used" — and the buckets are written out instead. */}
        {row.budgetUsed ? (
          <Stack gap={4}>
            {row.actuals && (
              <BudgetBar size="sm" segments={row.actuals} basis={row.budgetUsed.basis} overBudget={row.overBudget} />
            )}
            <Group gap="xs" wrap="nowrap">
              <Text size="sm">
                {t("budgetUsedPercentOf", {
                  percent: percent(row.budgetUsed.percent),
                  basis: basisPhrase(row.budgetUsed.basis),
                })}
              </Text>
              {row.overBudget && (
                <Badge variant="light" color="red" size="sm">
                  {t("overBudget")}
                </Badge>
              )}
            </Group>
          </Stack>
        ) : row.actuals ? (
          <Stack gap={4}>
            <LoggedSplit segments={row.actuals} totalHours={row.actuals.totalHours} />
            <Text size="sm">{t("notAvailable")}</Text>
          </Stack>
        ) : (
          <Text size="sm">{t("notAvailable")}</Text>
        )}
      </Table.Td>
      <Table.Td>
        <Text size="sm">{money(row.actuals?.totalAmount)}</Text>
      </Table.Td>
      <Table.Td>
        <Text size="sm">{hours(row.pendingHours)}</Text>
      </Table.Td>
      <Table.Td>
        {milestone ? (
          <Stack gap={2}>
            <Text size="sm">{milestone.name}</Text>
            <Group gap="xs" wrap="nowrap">
              <Text size="xs" c="dimmed">
                {milestone.plannedDate ? dates.day(milestone.plannedDate) : t("notAvailable")}
              </Text>
              {milestone.overdue && (
                <Badge variant="light" color="red" size="sm">
                  {t("overdue")}
                </Badge>
              )}
              {milestone.status === "ready" && (
                <Badge variant="light" color="teal" size="sm">
                  {t("milestoneStatusReady")}
                </Badge>
              )}
            </Group>
            {/* What it is worth is the reason to look at the column; absent for
                a milestone nobody can price. */}
            {milestone.effectiveAmount != null && (
              <Text size="xs" c="dimmed">
                {money(milestone.effectiveAmount)}
              </Text>
            )}
          </Stack>
        ) : (
          <Text size="sm" c="dimmed">
            {t("noNextMilestone")}
          </Text>
        )}
      </Table.Td>
      <Table.Td>
        <Stack gap={2}>
          <Text size="sm">{row.readyCount}</Text>
          {row.readyAmount != null && (
            <Text size="xs" c="dimmed">
              {money(row.readyAmount)}
            </Text>
          )}
        </Stack>
      </Table.Td>
    </Table.Tr>
  );
};
