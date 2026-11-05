import { BarChart } from "@mantine/charts";
import { Alert, Group, SegmentedControl, Select, SimpleGrid, Stack, Table, Tabs, Text, Title } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { IconAlertCircle, IconDownload } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import {
  type AnalysisPeriod,
  backlogCsvUrl,
  backlogQueryOptions,
  hitRateCsvUrl,
  hitRateQueryOptions,
  intakeCsvUrl,
  intakeQueryOptions,
} from "../api/analysis";
import { salespeopleQueryOptions } from "../api/salespeople";
import type { HitRateRow, IntakeGroupBy } from "../api/types";
import { PdfButton } from "../components/pdf-button";
import "../i18n";
import { noOrdersAccess, type OrdersAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { addDays, today, useOrdersFormat } from "../lib/format";

/** The one hue every analysis chart draws in: one series a chart, so no legend and no second colour. */
const SERIES_COLOR = "blue.6";

const groupings: IntakeGroupBy[] = ["month", "salesperson", "customer", "product", "category"];

/** The most groups a chart draws; the table holds them all. */
const CHART_GROUPS = 12;

/** The currencies of some rows, in order, and the one picked among them. */
const useCurrency = (currencies: readonly string[]) => {
  const [picked, setPicked] = useState<string | null>(null);
  const current = picked && currencies.includes(picked) ? picked : (currencies[0] ?? null);
  return { current, setPicked };
};

/**
 * The analysis of quotes and orders (O35), under orders:analysis-view: the
 * quote hit rate, the order intake and the backlog, each a table with its
 * CSV, and one chart in one currency — amounts in different currencies are
 * never added together.
 */
export const AnalysisPage = ({ access = noOrdersAccess }: { access?: OrdersAccess }) => {
  const { t } = useOrdersFormat();
  const [tab, setTab] = useState<string | null>("hit-rate");
  const [period, setPeriod] = useState<AnalysisPeriod>(() => ({ from: addDays(today(), -365), to: today() }));
  if (!access.canViewAnalysis)
    return (
      <Stack gap="lg">
        <PageHeader title={t("analysisTitle")} description={t("analysisDescription")} />
        <Alert color="gray">{t("analysisNeedsKey")}</Alert>
      </Stack>
    );
  const periodPicker = (
    <Group gap="sm" align="flex-end" data-testid="analysis-period">
      <DateInput
        label={t("periodFrom")}
        valueFormat="YYYY-MM-DD"
        value={period.from}
        maxDate={period.to}
        onChange={(value) => value && setPeriod((p) => ({ ...p, from: String(value).slice(0, 10) }))}
        size="xs"
      />
      <DateInput
        label={t("periodTo")}
        valueFormat="YYYY-MM-DD"
        value={period.to}
        minDate={period.from}
        onChange={(value) => value && setPeriod((p) => ({ ...p, to: String(value).slice(0, 10) }))}
        size="xs"
      />
    </Group>
  );
  return (
    <Stack gap="lg">
      <PageHeader title={t("analysisTitle")} description={t("analysisDescription")} />
      <Tabs value={tab} onChange={setTab} keepMounted={false}>
        <Tabs.List data-assist="orders.analysis.views">
          <Tabs.Tab value="hit-rate">{t("hitRateTitle")}</Tabs.Tab>
          <Tabs.Tab value="intake">{t("intakeTitle")}</Tabs.Tab>
          <Tabs.Tab value="backlog">{t("backlogTitle")}</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="hit-rate" pt="md">
          <HitRateView period={period} periodPicker={periodPicker} />
        </Tabs.Panel>
        <Tabs.Panel value="intake" pt="md">
          <IntakeView period={period} periodPicker={periodPicker} />
        </Tabs.Panel>
        <Tabs.Panel value="backlog" pt="md">
          <BacklogView />
        </Tabs.Panel>
      </Tabs>
    </Stack>
  );
};

/** A view's frame: its filters and CSV in one row above, then the chart and the table. */
const ViewCard = ({
  filters,
  csv,
  loading,
  error,
  children,
}: {
  filters?: ReactNode;
  csv: { url: string; fileLabel: string };
  loading: boolean;
  error: unknown;
  children: ReactNode;
}) => {
  const { t } = useOrdersFormat();
  return (
    <PageSection>
      <Stack gap="md">
        <Group justify="space-between" align="flex-end">
          <Group gap="sm" align="flex-end">
            {filters}
          </Group>
          <PdfButton
            url={csv.url}
            mode="download"
            size="xs"
            variant="light"
            failure={t("couldNotDownloadCsv")}
            leftSection={<IconDownload size={14} />}
            data-assist="orders.analysis.csv"
          >
            {csv.fileLabel}
          </PdfButton>
        </Group>
        {loading && <ContentSkeleton rows={4} rowHeight={32} />}
        {error ? (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {refusalMessage(error, t)}
          </Alert>
        ) : null}
        {children}
      </Stack>
    </PageSection>
  );
};

/** The currency a chart draws in, offered when the rows hold more than one. */
const CurrencyPicker = ({
  currencies,
  value,
  onChange,
}: {
  currencies: readonly string[];
  value: string | null;
  onChange: (currency: string) => void;
}) => {
  const { t } = useOrdersFormat();
  if (currencies.length < 2) return null;
  return (
    <SegmentedControl
      size="xs"
      aria-label={t("chartCurrency")}
      data={[...currencies]}
      value={value ?? currencies[0]}
      onChange={onChange}
    />
  );
};

const HitRateView = ({ period, periodPicker }: { period: AnalysisPeriod; periodPicker: ReactNode }) => {
  const { t, money, percent } = useOrdersFormat();
  const [salespersonId, setSalespersonId] = useState<string | null>(null);
  const salespeople = useQuery(salespeopleQueryOptions());
  const data = useQuery(hitRateQueryOptions(period, salespersonId ?? undefined));
  const totals = data.data?.totals ?? [];
  const { current, setPicked } = useCurrency(totals.map((r) => r.currency));
  const total = totals.find((r) => r.currency === current);
  const statuses = ["accepted", "declined", "expired", "cancelled", "open"] as const;
  const rate = (row: HitRateRow) => (row.sent.count > 0 ? percent((row.accepted.count / row.sent.count) * 100) : "—");
  const cell = (c: { count: number; amount: number }, currency: string) => (
    <>
      <Text size="sm">{c.count}</Text>
      <Text size="xs" c="dimmed">
        {money(c.amount, currency)}
      </Text>
    </>
  );
  return (
    <ViewCard
      filters={
        <>
          {periodPicker}
          <Select
            size="xs"
            label={t("salesperson")}
            placeholder={t("allSalespeople")}
            clearable
            data={(salespeople.data ?? []).map((p) => ({ value: p.id, label: p.displayName }))}
            value={salespersonId}
            onChange={setSalespersonId}
          />
        </>
      }
      csv={{ url: hitRateCsvUrl(period, salespersonId ?? undefined), fileLabel: t("downloadCsv") }}
      loading={data.isPending}
      error={data.error}
    >
      {data.data && data.data.totals.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noQuotesSent")}
        </Text>
      )}
      {total && (
        <Stack gap="xs">
          <Group justify="space-between">
            <Title order={5}>{t("hitRateChart", { currency: total.currency })}</Title>
            <CurrencyPicker currencies={totals.map((r) => r.currency)} value={current} onChange={setPicked} />
          </Group>
          <BarChart
            h={220}
            data={statuses.map((status) => ({ status: t(`hitRateStatus.${status}`), count: total[status].count }))}
            dataKey="status"
            series={[{ name: "count", label: t("quotesCount"), color: SERIES_COLOR }]}
            barProps={{ radius: [4, 4, 0, 0] }}
            withTooltip
            tickLine="none"
            gridAxis="y"
          />
        </Stack>
      )}
      {data.data && data.data.totals.length > 0 && (
        <Table.ScrollContainer minWidth={760}>
          <Table data-testid="hit-rate-table">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("salesperson")}</Table.Th>
                <Table.Th>{t("currency")}</Table.Th>
                <Table.Th>{t("hitRateStatus.sent")}</Table.Th>
                {statuses.map((s) => (
                  <Table.Th key={s}>{t(`hitRateStatus.${s}`)}</Table.Th>
                ))}
                <Table.Th>{t("hitRate")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {[...data.data.rows, ...data.data.totals].map((row, index) => (
                <Table.Tr
                  key={`${row.salesperson?.userId ?? row.salesperson?.displayName ?? "total"}-${row.currency}-${index}`}
                >
                  <Table.Td fw={row.salesperson ? undefined : 700}>
                    {index >= data.data.rows.length ? t("total") : (row.salesperson?.displayName ?? t("noSalesperson"))}
                  </Table.Td>
                  <Table.Td>{row.currency}</Table.Td>
                  <Table.Td>{cell(row.sent, row.currency)}</Table.Td>
                  {statuses.map((s) => (
                    <Table.Td key={s}>{cell(row[s], row.currency)}</Table.Td>
                  ))}
                  <Table.Td>{rate(row)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
    </ViewCard>
  );
};

const IntakeView = ({ period, periodPicker }: { period: AnalysisPeriod; periodPicker: ReactNode }) => {
  const { t, money } = useOrdersFormat();
  const [groupBy, setGroupBy] = useState<IntakeGroupBy>("month");
  const data = useQuery(intakeQueryOptions(period, groupBy));
  const totals = data.data?.totals ?? [];
  const { current, setPicked } = useCurrency(totals.map((r) => r.currency));
  const label = (key: string, text: string) => text || (key === "" ? t(`intakeNone.${groupBy}`) : `#${key}`);
  const inCurrency = (data.data?.rows ?? []).filter((r) => r.currency === current);
  const charted =
    groupBy === "month" ? inCurrency : [...inCurrency].sort((a, b) => b.amount - a.amount).slice(0, CHART_GROUPS);
  return (
    <ViewCard
      filters={
        <>
          {periodPicker}
          <Select
            size="xs"
            label={t("groupBy")}
            allowDeselect={false}
            data={groupings.map((g) => ({ value: g, label: t(`intakeGroup.${g}`) }))}
            value={groupBy}
            onChange={(value) => value && setGroupBy(value as IntakeGroupBy)}
          />
        </>
      }
      csv={{ url: intakeCsvUrl(period, groupBy), fileLabel: t("downloadCsv") }}
      loading={data.isPending}
      error={data.error}
    >
      {data.data && data.data.rows.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noOrdersConfirmed")}
        </Text>
      )}
      {current && charted.length > 0 && (
        <Stack gap="xs">
          <Group justify="space-between">
            <Title order={5}>{t("intakeChart", { currency: current })}</Title>
            <CurrencyPicker currencies={totals.map((r) => r.currency)} value={current} onChange={setPicked} />
          </Group>
          <BarChart
            h={240}
            data={charted.map((r) => ({ group: label(r.key, r.label), amount: r.amount }))}
            dataKey="group"
            series={[{ name: "amount", label: t("netAmount"), color: SERIES_COLOR }]}
            valueFormatter={(value) => money(value, current)}
            barProps={{ radius: [4, 4, 0, 0] }}
            withTooltip
            tickLine="none"
            gridAxis="y"
          />
        </Stack>
      )}
      {data.data && data.data.rows.length > 0 && (
        <Table data-testid="intake-table">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t(`intakeGroup.${groupBy}`)}</Table.Th>
              <Table.Th>{t("currency")}</Table.Th>
              <Table.Th ta="right">{t("ordersCount")}</Table.Th>
              <Table.Th ta="right">{t("netAmount")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {data.data.rows.map((r) => (
              <Table.Tr key={`${r.key}-${r.currency}`}>
                <Table.Td>{label(r.key, r.label)}</Table.Td>
                <Table.Td>{r.currency}</Table.Td>
                <Table.Td ta="right">{r.count}</Table.Td>
                <Table.Td ta="right">{money(r.amount, r.currency)}</Table.Td>
              </Table.Tr>
            ))}
            {data.data.totals.map((r) => (
              <Table.Tr key={`total-${r.currency}`}>
                <Table.Td fw={700}>{t("total")}</Table.Td>
                <Table.Td>{r.currency}</Table.Td>
                <Table.Td ta="right">{r.count}</Table.Td>
                <Table.Td ta="right" fw={700}>
                  {money(r.amount, r.currency)}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </ViewCard>
  );
};

const BacklogView = () => {
  const { t, money, date } = useOrdersFormat();
  const data = useQuery(backlogQueryOptions());
  const aged = data.data?.deliveredNotInvoiced ?? [];
  const { current, setPicked } = useCurrency(aged.map((r) => r.currency));
  const chosen = aged.find((r) => r.currency === current);
  return (
    <ViewCard
      filters={
        data.data && (
          <Text size="sm" c="dimmed">
            {t("backlogAsOf", { date: date(data.data.asOf) })}
          </Text>
        )
      }
      csv={{ url: backlogCsvUrl(), fileLabel: t("downloadCsv") }}
      loading={data.isPending}
      error={data.error}
    >
      {data.data && (
        <SimpleGrid cols={{ base: 1, md: 2 }}>
          <Stack gap="xs">
            <Title order={5}>{t("orderedNotDelivered")}</Title>
            {data.data.orderedNotDelivered.length === 0 ? (
              <Text size="sm" c="dimmed">
                {t("nothingOpen")}
              </Text>
            ) : (
              <Table data-testid="backlog-ordered">
                <Table.Tbody>
                  {data.data.orderedNotDelivered.map((r) => (
                    <Table.Tr key={r.currency}>
                      <Table.Td>{t("ordersCountOf", { count: r.count })}</Table.Td>
                      <Table.Td ta="right" fw={600}>
                        {money(r.amount, r.currency)}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            )}
          </Stack>
          <Stack gap="xs">
            <Title order={5}>{t("deliveredNotInvoiced")}</Title>
            {aged.length === 0 ? (
              <Text size="sm" c="dimmed">
                {t("nothingOpen")}
              </Text>
            ) : (
              <Table data-testid="backlog-delivered">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("currency")}</Table.Th>
                    <Table.Th ta="right">{t("age.days0To7")}</Table.Th>
                    <Table.Th ta="right">{t("age.days8To30")}</Table.Th>
                    <Table.Th ta="right">{t("age.over30Days")}</Table.Th>
                    <Table.Th ta="right">{t("total")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {aged.map((r) => (
                    <Table.Tr key={r.currency}>
                      <Table.Td>{r.currency}</Table.Td>
                      <Table.Td ta="right">{money(r.days0To7, r.currency)}</Table.Td>
                      <Table.Td ta="right">{money(r.days8To30, r.currency)}</Table.Td>
                      <Table.Td ta="right" c={r.over30Days > 0 ? "red" : undefined}>
                        {money(r.over30Days, r.currency)}
                      </Table.Td>
                      <Table.Td ta="right" fw={600}>
                        {money(r.amount, r.currency)}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            )}
          </Stack>
        </SimpleGrid>
      )}
      {chosen && (
        <Stack gap="xs">
          <Group justify="space-between">
            <Title order={5}>{t("backlogChart", { currency: chosen.currency })}</Title>
            <CurrencyPicker currencies={aged.map((r) => r.currency)} value={current} onChange={setPicked} />
          </Group>
          <BarChart
            h={200}
            data={[
              { age: t("age.days0To7"), amount: chosen.days0To7 },
              { age: t("age.days8To30"), amount: chosen.days8To30 },
              { age: t("age.over30Days"), amount: chosen.over30Days },
            ]}
            dataKey="age"
            series={[{ name: "amount", label: t("netAmount"), color: SERIES_COLOR }]}
            valueFormatter={(value) => money(value, chosen.currency)}
            barProps={{ radius: [4, 4, 0, 0] }}
            withTooltip
            tickLine="none"
            gridAxis="y"
          />
        </Stack>
      )}
    </ViewCard>
  );
};
