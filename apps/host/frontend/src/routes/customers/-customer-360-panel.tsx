import { Anchor, Badge, SimpleGrid, Stack, Table } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { KpiCard, WidgetCard } from "@vantigo/frontend-shell/ui";
import { type CustomerOverview, customerOverviewQueryOptions, type OverviewAmount } from "../../api/customer-overview";
import "../../i18n";

type LastActivitySource = "timeline" | "work" | "expense";

const lastActivityKeys = {
  timeline: "customer.overview360.lastActivityTimeline",
  work: "customer.overview360.lastActivityWork",
  expense: "customer.overview360.lastActivityExpense",
} as const;

/**
 * The latest of the three dates and which it was. They are YYYY-MM-DD, which
 * compares as text exactly as it does as dates, and the strict `>` keeps the
 * first-listed source on a tie — the customer's own timeline, then work, then
 * expenses.
 */
export const latestActivity = (
  activity: CustomerOverview["lastActivity"],
): { on: string; source: LastActivitySource } | null => {
  const candidates: [string | null, LastActivitySource][] = [
    [activity.timelineOn, "timeline"],
    [activity.workOn, "work"],
    [activity.expenseOn, "expense"],
  ];
  let latest: { on: string; source: LastActivitySource } | null = null;
  for (const [on, source] of candidates) {
    if (on !== null && (latest === null || on > latest.on)) latest = { on, source };
  }
  return latest;
};

/**
 * One grid for the loaded tiles and the skeleton alike, sized by the tiles it
 * holds: a lone tile is full width at every breakpoint rather than half at `xs`
 * and whole at `md`. Every KpiCard is the same minimum height, so the grid
 * growing from the skeleton's one card to the loaded tiles does not move what
 * is below it.
 */
const tileColumns = (tiles: number) => ({ base: 1, xs: Math.min(2, tiles), md: tiles });

/**
 * The Customer 360 panel (customer 360 design D3): what is going on with this
 * customer across the modules that know, at the top of the Overview tab. It is
 * the host's rather than the customers package's because it links across
 * modules — a project row leads to the project — which the dashboard already
 * shows is the host's business.
 *
 * It reads nothing but the overview response. A tile whose section is absent
 * is not rendered, and the panel never says why (the dashboard's own rule): the
 * server has already decided what this caller may see, and absent means "not
 * installed" as often as "not for you". Loading is the dashboard's skeleton
 * card; an error with nothing to show hides the panel rather than the page,
 * because every card below it still works — and a failed background refetch
 * keeps what the panel already showed rather than taking good figures away.
 */
export const Customer360Panel = ({ customerId }: { customerId: number }) => {
  const { t, formatters } = useI18n("host");
  const overview = useQuery(customerOverviewQueryOptions(customerId));
  const title = t("customer.overview360.title");

  if (overview.data === undefined) {
    if (overview.isError) return null;
    return (
      <section aria-label={title} aria-busy="true">
        <SimpleGrid cols={tileColumns(1)} spacing="md">
          <KpiCard label={t("customer.overview360.lastActivity")} value="—" loading />
        </SimpleGrid>
      </section>
    );
  }

  const { projects, work, expenses, lastActivity } = overview.data;
  const count = (value: number) => formatters.formatNumber(value);
  // One currency per entry, never a sum across them (D2): the tile's second line.
  const amounts = (list: OverviewAmount[] | null) =>
    list && list.length > 0
      ? list.map((entry) => formatters.formatCurrency(entry.amount, entry.currency)).join(" · ")
      : undefined;
  // Calendar dates from the server, shown as the calendar date they are.
  const date = (on: string) => formatters.formatDate(on, { dateStyle: "medium", timeZone: "UTC" });
  const latest = latestActivity(lastActivity);
  const tiles = 1 + [projects, work, expenses].filter((section) => section !== null).length;

  return (
    <section aria-label={title}>
      <Stack gap="md">
        <SimpleGrid cols={tileColumns(tiles)} spacing="md">
          {projects && (
            <KpiCard
              label={t("customer.overview360.openProjects")}
              value={count(projects.openCount)}
              // At the cap the counts are over the customer's first 2000
              // projects, but totalCount is only the part of those this caller
              // may see, so the hint names no number rather than the wrong one.
              // The counts go in formatted, as text, which i18next does not
              // pluralise — hence the host's own singular key.
              hint={
                projects.truncated
                  ? t("customer.overview360.openProjectsTruncatedHint")
                  : t(
                      projects.totalCount === 1
                        ? "customer.overview360.openProjectsHintSingular"
                        : "customer.overview360.openProjectsHint",
                      { count: count(projects.totalCount) },
                    )
              }
            />
          )}
          {work && (
            <KpiCard
              label={t("customer.overview360.unbilledHours")}
              value={t("customer.overview360.hoursValue", {
                hours: formatters.formatNumber(work.unbilledHoursHundredths / 100, { maximumFractionDigits: 2 }),
              })}
              hint={amounts(work.unbilledAmounts)}
            />
          )}
          {expenses && (
            <KpiCard
              label={t("customer.overview360.expensesReady")}
              value={count(expenses.readyCount)}
              hint={amounts(expenses.readyAmounts)}
            />
          )}
          <KpiCard
            label={t("customer.overview360.lastActivity")}
            value={latest ? date(latest.on) : "—"}
            hint={latest ? t(lastActivityKeys[latest.source]) : t("customer.overview360.noActivity")}
          />
        </SimpleGrid>
        {projects && projects.open.length > 0 && (
          <WidgetCard title={t("customer.overview360.openProjects")}>
            <Table.ScrollContainer minWidth={480}>
              <Table>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("customer.overview360.code")}</Table.Th>
                    <Table.Th>{t("customer.overview360.name")}</Table.Th>
                    <Table.Th>{t("customer.overview360.status")}</Table.Th>
                    <Table.Th>{t("customer.overview360.lastWork")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {projects.open.map((project) => (
                    <Table.Tr key={project.id}>
                      <Table.Td>{project.code}</Table.Td>
                      <Table.Td>
                        {/* The name is the link: a screen reader's list of links then names the project. */}
                        <Anchor
                          renderRoot={(props) => (
                            <Link to="/projects/$projectId" params={{ projectId: project.id }} {...props} />
                          )}
                        >
                          {project.name}
                        </Anchor>
                      </Table.Td>
                      <Table.Td>
                        {/* Open rows are active by definition today; any other status is shown as sent. */}
                        <Badge variant="light">
                          {project.status === "active" ? t("customer.overview360.statusActive") : project.status}
                        </Badge>
                      </Table.Td>
                      <Table.Td>{project.lastWorkOn ? date(project.lastWorkOn) : "—"}</Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
            {/* The rows are the first few; the Projects tab has them all. The
                section is only here when that tab is too (Projects on and
                projects:access), so the link never leads nowhere. */}
            {projects.openCount > projects.open.length && (
              <Anchor
                size="sm"
                mt="sm"
                display="block"
                renderRoot={(props) => <Link to="/customers/$customerId/projects" params={{ customerId }} {...props} />}
              >
                {t("customer.overview360.seeAllOpenProjects", { count: count(projects.openCount) })}
              </Anchor>
            )}
          </WidgetCard>
        )}
      </Stack>
    </section>
  );
};
