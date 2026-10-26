import {
  Alert,
  Badge,
  Button,
  Card,
  Group,
  MultiSelect,
  SegmentedControl,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Title,
} from "@mantine/core";
import { DateInput, MonthPickerInput, YearPickerInput } from "@mantine/dates";
import { IconAlertCircle, IconCalendar, IconChartLine, IconFileImport } from "@tabler/icons-react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type SpotPriceCoverageArea,
  type SpotPriceFetchStatus,
  spotPriceCoverageQueryOptions,
  spotPriceSummaryQueryOptions,
  spotPricesQueryOptions,
} from "../api/spot-prices";
import {
  addMarketDays,
  addMarketMonths,
  MARKET_TIME_ZONE,
  marketDayKey,
  marketDayStart,
  marketWeekStart,
} from "../lib/market-time";
import { formatPrice, type SummaryBucket, useBucketLabel } from "../lib/spot-price-view";
import "../i18n";
import { ImportPricesModal } from "./-import-prices-modal";
import { PriceChart } from "./-price-chart";
import { PriceSummaryChart } from "./-price-summary-chart";

const PRICE_PERIODS = ["day", "week", "month", "year", "range"] as const;
export type PricePeriod = (typeof PRICE_PERIODS)[number];

/** The most areas the chart compares. */
const MAX_COMPARED_AREAS = 5;

/**
 * What the Prices page shows, as the host keeps it in the URL: the areas
 * compared (the first is the one the figures are for), the period, the day
 * it holds (`YYYY-MM-DD`; Day, Week, Month, Year) and the months of a range
 * (`YYYY-MM`, both inclusive). Absent means the default.
 */
export interface PricesSearch {
  areas?: string[];
  period?: PricePeriod;
  date?: string;
  from?: string;
  to?: string;
}

/** A range of up to two years is charted per week, a longer one per month. */
const WEEKLY_RANGE_MONTHS = 24;
/** The summary read answers at most 10 980 days per month: 360 months stay inside it. */
const MAX_RANGE_MONTHS = 360;

type PriceWindow =
  | { read: "intervals"; period: "day" | "week"; from: string; to: string }
  | { read: "summary"; bucket: SummaryBucket; from: string; to: string };

const monthsBetween = (first: string, last: string) =>
  (Number(last.slice(0, 4)) - Number(first.slice(0, 4))) * 12 + Number(last.slice(5, 7)) - Number(first.slice(5, 7));

/** The civil days a period covers, [first, next), and the read that answers it. */
const periodWindow = (period: PricePeriod, day: string, fromMonth: string, toMonth: string): PriceWindow => {
  const instants = (first: string, next: string) => ({ from: marketDayStart(first), to: marketDayStart(next) });
  switch (period) {
    case "day":
      return { read: "intervals", period, ...instants(day, addMarketDays(day, 1)) };
    case "week": {
      const monday = marketWeekStart(day);
      return { read: "intervals", period, ...instants(monday, addMarketDays(monday, 7)) };
    }
    case "month":
      return { read: "summary", bucket: "day", ...instants(`${day.slice(0, 7)}-01`, addMarketMonths(day, 1)) };
    case "year": {
      const year = Number(day.slice(0, 4));
      return { read: "summary", bucket: "day", ...instants(`${year}-01-01`, `${year + 1}-01-01`) };
    }
    case "range": {
      let first = `${fromMonth}-01`;
      let last = `${toMonth}-01`;
      if (first > last) [first, last] = [last, first];
      if (monthsBetween(first, last) + 1 > MAX_RANGE_MONTHS) first = addMarketMonths(last, 1 - MAX_RANGE_MONTHS);
      const next = addMarketMonths(last, 1);
      if (monthsBetween(first, last) + 1 > WEEKLY_RANGE_MONTHS) {
        return { read: "summary", bucket: "month", ...instants(first, next) };
      }
      // Whole ISO weeks: from the Monday on or before the first day to the one on or after the end.
      const lastMonday = marketWeekStart(next);
      return {
        read: "summary",
        bucket: "week",
        ...instants(marketWeekStart(first), lastMonday === next ? next : addMarketDays(lastMonday, 7)),
      };
    }
  }
};

/** A price and when it held: an interval, or a bucket's name. */
interface Extreme {
  value: number;
  at: string;
}

interface Figures {
  average: number;
  lowest: Extreme;
  highest: Extreme;
}

const fetchColor = (status: SpotPriceFetchStatus) =>
  ({ ok: "teal", pending: "blue", missing: "yellow", failed: "red" })[status];

/** A week or more without prices is red: the feed fetches only the last seven days on its own. */
const missingColor = (days: number) => (days === 0 ? "teal" : days < 7 ? "yellow" : "red");

/**
 * **Prices** (energy consumption billing design D11, spot-price service
 * design S8): what the installation holds of the day-ahead spot prices per
 * price area, and up to five areas' prices over a day, a week, a month, a
 * year or a range of months, with the first area's average, lowest and
 * highest price. `canImport` is the host's answer to whether the caller
 * holds `energy:prices-manage`; it puts **Import prices** in the header.
 * `search` and `onSearchChange` let the host keep the view in the URL;
 * without them the page keeps it itself.
 */
export const PricesPage = ({
  canImport = false,
  search,
  onSearchChange,
}: {
  canImport?: boolean;
  search?: PricesSearch;
  onSearchChange?: (search: PricesSearch) => void;
}) => {
  const { t, formatters } = useI18n("energy");
  const bucketLabel = useBucketLabel();
  const price = formatPrice(formatters.formatNumber);
  const coverage = useQuery(spotPriceCoverageQueryOptions());
  const areas = coverage.data?.areas ?? [];
  const showHistory = coverage.data?.source === "service";

  const [ownSearch, setOwnSearch] = useState<PricesSearch>({});
  const current = search ?? ownSearch;
  const update = (patch: PricesSearch) => {
    const next = { ...current, ...patch };
    if (!search) setOwnSearch(next);
    onSearchChange?.(next);
  };

  const known = new Set(areas.map((item) => item.priceArea));
  const chosen = (current.areas ?? []).filter((item) => known.has(item)).slice(0, MAX_COMPARED_AREAS);
  const compared = chosen.length > 0 ? chosen : areas.slice(0, 1).map((item) => item.priceArea);
  const period = current.period ?? "day";
  const [today] = useState(() => marketDayKey(new Date().toISOString()));
  const day = current.date ?? today;
  const toMonth = current.to ?? today.slice(0, 7);
  const fromMonth = current.from ?? addMarketMonths(`${toMonth}-01`, -11).slice(0, 7);
  const view = periodWindow(period, day, fromMonth, toMonth);

  const intervalReads = useQueries({
    queries: view.read === "intervals" ? compared.map((area) => spotPricesQueryOptions(area, view.from, view.to)) : [],
  });
  const summaryReads = useQueries({
    queries:
      view.read === "summary"
        ? compared.map((area) => spotPriceSummaryQueryOptions(area, view.bucket, view.from, view.to))
        : [],
  });
  const reads = view.read === "intervals" ? intervalReads : summaryReads;
  const failed = reads.find((read) => read.isError);
  const pending = reads.some((read) => read.isPending);
  const intervalAreas = compared.map((area, index) => ({ area, intervals: intervalReads[index]?.data ?? [] }));
  const summaryAreas = compared.map((area, index) => ({ area, buckets: summaryReads[index]?.data?.buckets ?? [] }));
  const empty =
    view.read === "intervals"
      ? intervalAreas.every((item) => item.intervals.length === 0)
      : summaryAreas.every((item) => item.buckets.length === 0);
  const preliminary =
    view.read === "intervals"
      ? intervalAreas.some((item) => item.intervals.some((interval) => interval.state === "preliminary"))
      : summaryAreas.some((item) => item.buckets.some((bucket) => bucket.preliminary));

  const intervalTime = (start: string, end: string) => {
    const clock = { hour: "2-digit", minute: "2-digit", timeZone: MARKET_TIME_ZONE } as const;
    const from = formatters.formatDate(
      start,
      view.read === "intervals" && view.period === "week"
        ? { weekday: "short", day: "numeric", month: "short", ...clock }
        : clock,
    );
    return `${from}–${formatters.formatDate(end, clock)}`;
  };

  // The first area's figures. An average weighs each price by how long it held.
  const figures = ((): Figures | null => {
    if (view.read === "intervals") {
      const intervals = intervalAreas[0]?.intervals ?? [];
      if (intervals.length === 0) return null;
      let weighted = 0;
      let length = 0;
      let lowest = intervals[0];
      let highest = intervals[0];
      for (const item of intervals) {
        const minutes = (Date.parse(item.end) - Date.parse(item.start)) / 60_000;
        weighted += item.nokPerKwh * minutes;
        length += minutes;
        if (item.nokPerKwh < lowest.nokPerKwh) lowest = item;
        if (item.nokPerKwh > highest.nokPerKwh) highest = item;
      }
      return {
        average: weighted / length,
        lowest: { value: lowest.nokPerKwh, at: intervalTime(lowest.start, lowest.end) },
        highest: { value: highest.nokPerKwh, at: intervalTime(highest.start, highest.end) },
      };
    }
    const buckets = summaryAreas[0]?.buckets ?? [];
    if (buckets.length === 0) return null;
    let weighted = 0;
    let covered = 0;
    let lowest = buckets[0];
    let highest = buckets[0];
    for (const item of buckets) {
      weighted += item.avgNokPerKwh * item.coveredMinutes;
      covered += item.coveredMinutes;
      if (item.minNokPerKwh < lowest.minNokPerKwh) lowest = item;
      if (item.maxNokPerKwh > highest.maxNokPerKwh) highest = item;
    }
    return {
      average: covered > 0 ? weighted / covered : buckets[0].avgNokPerKwh,
      lowest: { value: lowest.minNokPerKwh, at: bucketLabel(lowest.start, view.bucket, true) },
      highest: { value: highest.maxNokPerKwh, at: bucketLabel(highest.start, view.bucket, true) },
    };
  })();
  const [importOpen, setImportOpen] = useState(false);

  const instant = (value: string | undefined) =>
    value
      ? formatters.formatDate(value, { dateStyle: "medium", timeStyle: "short", timeZone: MARKET_TIME_ZONE })
      : t("notAvailable");
  // A fetch's day is a civil date: formatted where it is that date.
  const civilDay = (value: string) => formatters.formatDate(value, { dateStyle: "medium", timeZone: "UTC" });

  const history = (item: SpotPriceCoverageArea) => {
    const { history: copy, followedByService } = item;
    if (!copy && followedByService !== false) return t("notAvailable");
    return (
      <Stack gap={2}>
        {followedByService === false && (
          <Badge color="yellow" variant="light">
            {t("pricesNotFollowed")}
          </Badge>
        )}
        {copy && (
          <Text size="sm">
            {copy.complete
              ? copy.floorDay && t("pricesHistoryComplete", { date: civilDay(copy.floorDay) })
              : copy.cursorDay
                ? t("pricesHistoryCopying", { date: civilDay(copy.cursorDay) })
                : t("pricesHistoryWaiting")}
          </Text>
        )}
        {copy && copy.daysSkipped > 0 && (
          <Text size="xs" c="dimmed">
            {t("pricesHistorySkipped", { count: copy.daysSkipped })}
          </Text>
        )}
        {copy?.error && (
          <Text size="xs" c="dimmed">
            {copy.error}
          </Text>
        )}
      </Stack>
    );
  };

  const resolution = (value: string | undefined) =>
    value === "PT15M" || value === "PT60M" ? t(`pricesResolution_${value}`) : (value ?? t("notAvailable"));

  const lastFetch = (item: SpotPriceCoverageArea) => {
    if (!item.lastFetch) return <Text size="sm">{t("pricesNeverFetched")}</Text>;
    const { status, day: fetchDay, error } = item.lastFetch;
    return (
      <Stack gap={2}>
        <Group gap="xs">
          <Badge color={fetchColor(status)} variant="light">
            {t(`pricesFetch_${status}`)}
          </Badge>
          <Text size="sm">{civilDay(fetchDay)}</Text>
        </Group>
        {error && (
          <Text size="xs" c="dimmed">
            {error}
          </Text>
        )}
      </Stack>
    );
  };

  return (
    <Stack gap="lg">
      <PageHeader
        title={
          <>
            <IconChartLine size={28} /> {t("prices")}
          </>
        }
        description={t("pricesDescription")}
        actions={
          canImport && (
            <Button variant="default" leftSection={<IconFileImport size={16} />} onClick={() => setImportOpen(true)}>
              {t("importPrices")}
            </Button>
          )
        }
      />
      {canImport && <ImportPricesModal opened={importOpen} onClose={() => setImportOpen(false)} />}
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group gap="sm">
            <Title order={3}>{t("pricesCoverage")}</Title>
            {coverage.data && (
              <Badge variant="light" color={coverage.data.source === "service" ? "teal" : "gray"}>
                {t(`pricesSource_${coverage.data.source}`)}
              </Badge>
            )}
          </Group>
          {coverage.isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadPriceCoverage")}>
              {coverage.error.message}
            </Alert>
          )}
          {coverage.isPending && <ContentSkeleton rows={3} rowHeight={44} />}
          {coverage.data &&
            (areas.length === 0 ? (
              <EmptyState size="sm" title={t("noPriceAreas")} description={t("noPriceAreasDescription")} />
            ) : (
              <Table.ScrollContainer minWidth={showHistory ? 1100 : 860}>
                <Table striped>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("priceArea")}</Table.Th>
                      <Table.Th>{t("pricesFirst")}</Table.Th>
                      <Table.Th>{t("pricesLast")}</Table.Th>
                      <Table.Th>{t("pricesResolution")}</Table.Th>
                      <Table.Th>{t("pricesIntervals")}</Table.Th>
                      <Table.Th>{t("pricesDaysMissing")}</Table.Th>
                      {showHistory && <Table.Th>{t("pricesHistory")}</Table.Th>}
                      <Table.Th>{t("pricesLastFetch")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {areas.map((item) => (
                      <Table.Tr key={item.priceArea}>
                        <Table.Td>{item.priceArea}</Table.Td>
                        <Table.Td>{instant(item.firstStart)}</Table.Td>
                        <Table.Td>{instant(item.lastEnd)}</Table.Td>
                        <Table.Td>{resolution(item.resolution)}</Table.Td>
                        <Table.Td>{formatters.formatNumber(item.intervalCount)}</Table.Td>
                        <Table.Td>
                          <Badge color={missingColor(item.daysMissingLast30)} variant="light">
                            {item.daysMissingLast30 === 0
                              ? t("pricesComplete")
                              : t("pricesDaysMissingCount", { count: item.daysMissingLast30 })}
                          </Badge>
                        </Table.Td>
                        {showHistory && <Table.Td>{history(item)}</Table.Td>}
                        <Table.Td>{lastFetch(item)}</Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            ))}
        </Stack>
      </Card>
      {compared.length > 0 && (
        <Card withBorder padding="lg" radius="md">
          <Stack gap="md">
            <Group gap="sm">
              <Title order={3}>{t("spotPrices")}</Title>
              {preliminary && (
                <Badge color="yellow" variant="light">
                  {t("pricesPreliminary")}
                </Badge>
              )}
            </Group>
            <Group align="flex-end">
              <MultiSelect
                label={t("pricesCompareAreas")}
                data={areas.map((item) => item.priceArea)}
                value={compared}
                onChange={(value) => value.length > 0 && update({ areas: value })}
                maxValues={MAX_COMPARED_AREAS}
                miw={180}
              />
              <SegmentedControl
                aria-label={t("pricesPeriod")}
                value={period}
                onChange={(value) => update({ period: value as PricePeriod })}
                data={PRICE_PERIODS.map((value) => ({ label: t(`pricesPeriod_${value}`), value }))}
              />
              {(period === "day" || period === "week") && (
                <DateInput
                  label={t("day")}
                  value={day}
                  valueFormat={t("dateInputFormat")}
                  onChange={(value) => value && update({ date: value })}
                  leftSection={<IconCalendar size={16} />}
                />
              )}
              {period === "month" && (
                <MonthPickerInput
                  label={t("month")}
                  value={`${day.slice(0, 7)}-01`}
                  onChange={(value) => value && update({ date: value })}
                  leftSection={<IconCalendar size={16} />}
                  allowDeselect={false}
                />
              )}
              {period === "year" && (
                <YearPickerInput
                  label={t("pricesPeriod_year")}
                  value={`${day.slice(0, 4)}-01-01`}
                  onChange={(value) => value && update({ date: value })}
                  leftSection={<IconCalendar size={16} />}
                  allowDeselect={false}
                />
              )}
              {period === "range" && (
                <>
                  <MonthPickerInput
                    label={t("pricesFromMonth")}
                    value={`${fromMonth}-01`}
                    onChange={(value) => value && update({ from: value.slice(0, 7) })}
                    leftSection={<IconCalendar size={16} />}
                    allowDeselect={false}
                  />
                  <MonthPickerInput
                    label={t("pricesToMonth")}
                    value={`${toMonth}-01`}
                    onChange={(value) => value && update({ to: value.slice(0, 7) })}
                    leftSection={<IconCalendar size={16} />}
                    allowDeselect={false}
                  />
                </>
              )}
            </Group>
            {failed?.error && (
              <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadPrices")}>
                {failed.error.message}
              </Alert>
            )}
            {!failed && pending && <ContentSkeleton rows={1} rowHeight={280} />}
            {!failed &&
              !pending &&
              (empty ? (
                <EmptyState size="sm" title={t("noPricesForPeriod")} />
              ) : (
                <>
                  {figures && (
                    <SimpleGrid cols={{ base: 1, sm: 3 }} aria-label={t("pricesFiguresFor", { area: compared[0] })}>
                      {(
                        [
                          ["pricesAverage", figures.average, undefined],
                          ["pricesLowest", figures.lowest.value, figures.lowest.at],
                          ["pricesHighest", figures.highest.value, figures.highest.at],
                        ] as const
                      ).map(([label, value, at]) => (
                        <Stack key={label} gap={0} data-testid={`price-figure-${label}`}>
                          <Text size="xs" c="dimmed">
                            {compared.length > 1 ? `${t(label)} · ${compared[0]}` : t(label)}
                          </Text>
                          <Text size="lg" fw={600}>
                            {price(value)} {t("chartNokPerKwh")}
                          </Text>
                          {at && (
                            <Text size="xs" c="dimmed">
                              {at}
                            </Text>
                          )}
                        </Stack>
                      ))}
                    </SimpleGrid>
                  )}
                  {view.read === "intervals" ? (
                    <PriceChart areas={intervalAreas} period={view.period} />
                  ) : (
                    <PriceSummaryChart areas={summaryAreas} bucket={view.bucket} />
                  )}
                </>
              ))}
          </Stack>
        </Card>
      )}
    </Stack>
  );
};
