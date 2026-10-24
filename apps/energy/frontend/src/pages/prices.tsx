import { Alert, Badge, Button, Card, Group, SegmentedControl, Select, Stack, Table, Text, Title } from "@mantine/core";
import { DateInput, MonthPickerInput } from "@mantine/dates";
import { IconAlertCircle, IconCalendar, IconChartLine, IconFileImport } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type SpotPriceCoverageArea,
  type SpotPriceFetchStatus,
  spotPriceCoverageQueryOptions,
  spotPricesQueryOptions,
} from "../api/spot-prices";
import { addMarketDays, MARKET_TIME_ZONE, marketDayKey, marketDayStart, nextMarketMonth } from "../lib/market-time";
import "../i18n";
import { ImportPricesModal } from "./-import-prices-modal";
import { PriceChart } from "./-price-chart";

type Period = "day" | "month";

const fetchColor = (status: SpotPriceFetchStatus) =>
  ({ ok: "teal", pending: "blue", missing: "yellow", failed: "red" })[status];

/** A week or more without prices is red: the feed fetches only the last seven days on its own. */
const missingColor = (days: number) => (days === 0 ? "teal" : days < 7 ? "yellow" : "red");

/** The window a period asks for: the civil day, or its whole month, as instants on the market's clock. */
const periodRange = (day: string, period: Period) => {
  const first = period === "day" ? day : `${day.slice(0, 7)}-01`;
  const next = period === "day" ? addMarketDays(day, 1) : nextMarketMonth(day);
  return { from: marketDayStart(first), to: marketDayStart(next) };
};

/**
 * **Prices** (energy consumption billing design D11): what the installation
 * holds of the day-ahead spot prices per price area, and one area's prices
 * for a day or a month. `canImport` is the host's answer to whether the
 * caller holds `energy:prices-manage`; it puts **Import prices** in the
 * header.
 */
export const PricesPage = ({ canImport = false }: { canImport?: boolean }) => {
  const { t, formatters } = useI18n("energy");
  const coverage = useQuery(spotPriceCoverageQueryOptions());
  const areas = coverage.data?.areas ?? [];
  const [chosenArea, setChosenArea] = useState<string | null>(null);
  const area = areas.some((item) => item.priceArea === chosenArea) ? chosenArea : (areas[0]?.priceArea ?? null);
  const [period, setPeriod] = useState<Period>("day");
  const [day, setDay] = useState(() => marketDayKey(new Date().toISOString()));
  const range = periodRange(day, period);
  const intervals = useQuery({ ...spotPricesQueryOptions(area ?? "", range.from, range.to), enabled: !!area });
  const [importOpen, setImportOpen] = useState(false);

  const instant = (value: string | undefined) =>
    value
      ? formatters.formatDate(value, { dateStyle: "medium", timeStyle: "short", timeZone: MARKET_TIME_ZONE })
      : t("notAvailable");
  // A fetch's day is a civil date: formatted where it is that date.
  const civilDay = (value: string) => formatters.formatDate(value, { dateStyle: "medium", timeZone: "UTC" });

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
          <Title order={3}>{t("pricesCoverage")}</Title>
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
              <Table.ScrollContainer minWidth={860}>
                <Table striped>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("priceArea")}</Table.Th>
                      <Table.Th>{t("pricesFirst")}</Table.Th>
                      <Table.Th>{t("pricesLast")}</Table.Th>
                      <Table.Th>{t("pricesIntervals")}</Table.Th>
                      <Table.Th>{t("pricesDaysMissing")}</Table.Th>
                      <Table.Th>{t("pricesLastFetch")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {areas.map((item) => (
                      <Table.Tr key={item.priceArea}>
                        <Table.Td>{item.priceArea}</Table.Td>
                        <Table.Td>{instant(item.firstStart)}</Table.Td>
                        <Table.Td>{instant(item.lastEnd)}</Table.Td>
                        <Table.Td>{formatters.formatNumber(item.intervalCount)}</Table.Td>
                        <Table.Td>
                          <Badge color={missingColor(item.daysMissingLast30)} variant="light">
                            {item.daysMissingLast30 === 0
                              ? t("pricesComplete")
                              : t("pricesDaysMissingCount", { count: item.daysMissingLast30 })}
                          </Badge>
                        </Table.Td>
                        <Table.Td>{lastFetch(item)}</Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            ))}
        </Stack>
      </Card>
      {area && (
        <Card withBorder padding="lg" radius="md">
          <Stack gap="md">
            <Title order={3}>{t("spotPrices")}</Title>
            <Group align="flex-end">
              <Select
                label={t("priceArea")}
                data={areas.map((item) => item.priceArea)}
                value={area}
                onChange={(value) => value && setChosenArea(value)}
                allowDeselect={false}
                w={140}
              />
              <SegmentedControl
                aria-label={t("pricesPeriod")}
                value={period}
                onChange={(value) => setPeriod(value as Period)}
                data={[
                  { label: t("day"), value: "day" },
                  { label: t("month"), value: "month" },
                ]}
              />
              {period === "day" ? (
                <DateInput
                  label={t("day")}
                  value={day}
                  valueFormat={t("dateInputFormat")}
                  onChange={(value) => value && setDay(value)}
                  leftSection={<IconCalendar size={16} />}
                />
              ) : (
                <MonthPickerInput
                  label={t("month")}
                  value={`${day.slice(0, 7)}-01`}
                  onChange={(value) => value && setDay(value)}
                  leftSection={<IconCalendar size={16} />}
                  allowDeselect={false}
                />
              )}
            </Group>
            {intervals.isError && (
              <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadPrices")}>
                {intervals.error.message}
              </Alert>
            )}
            {intervals.isPending && <ContentSkeleton rows={1} rowHeight={280} />}
            {intervals.data &&
              (intervals.data.length === 0 ? (
                <EmptyState size="sm" title={t("noPricesForPeriod")} />
              ) : (
                <PriceChart intervals={intervals.data} period={period} />
              ))}
          </Stack>
        </Card>
      )}
    </Stack>
  );
};
