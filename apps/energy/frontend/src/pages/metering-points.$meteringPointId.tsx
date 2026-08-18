import {
  Alert,
  Anchor,
  Badge,
  Breadcrumbs,
  Button,
  Card,
  Group,
  SegmentedControl,
  Stack,
  Table,
  Text,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconBolt, IconCalendar, IconPencil, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { Fragment, useState } from "react";
import { customersQueryOptions } from "../api/customers";
import {
  type ConsumptionResolution,
  consumptionAggregateQueryOptions,
  consumptionQueryOptions,
  endSupplyPeriod,
  meteringPointQueryOptions,
  metersQueryOptions,
  supplyPeriodsQueryOptions,
} from "../api/energy";
import { MARKET_TIME_ZONE, marketDayKey } from "../lib/market-time";
import { ConsumptionChart } from "./-consumption-chart";
import { ManualReadingModal } from "./-manual-reading-modal";
import { MeteringPointFormModal, type MeteringPointModalState } from "./-metering-point-form-modal";
import { ReplaceMeterModal } from "./-replace-meter-modal";
import { SupplyPeriodModal } from "./-supply-period-modal";
import "../i18n";

const defaultFrom = () => {
  const date = new Date();
  date.setMonth(date.getMonth() - 1);
  return date.toISOString().slice(0, 10);
};
const defaultTo = () => new Date().toISOString().slice(0, 10);

export const MeteringPointDetailsPage = () => {
  const { t, formatters } = useI18n("energy");
  const { meteringPointId, tenantSlug } = useParams({ strict: false }) as {
    meteringPointId: number;
    tenantSlug?: string;
  };
  const client = useQueryClient();
  const { data: point } = useSuspenseQuery(meteringPointQueryOptions(meteringPointId));
  const { data: periods } = useQuery(supplyPeriodsQueryOptions(meteringPointId));
  const { data: meters } = useQuery(metersQueryOptions(meteringPointId));
  const [from, setFrom] = useState(defaultFrom());
  const [to, setTo] = useState(defaultTo());
  const [resolution, setResolution] = useState<ConsumptionResolution>("day");
  const { data: consumption } = useQuery(consumptionQueryOptions(meteringPointId, from, to));
  const { data: aggregates } = useQuery(consumptionAggregateQueryOptions(meteringPointId, { from, to, resolution }));
  const { data: customers } = useQuery(customersQueryOptions());
  const [editState, setEditState] = useState<MeteringPointModalState | null>(null);
  const [supplyPeriodModalOpen, setSupplyPeriodModalOpen] = useState(false);
  const [readingOpen, setReadingOpen] = useState(false);
  const [replaceMeterOpen, setReplaceMeterOpen] = useState(false);
  const endMutation = useMutation({
    mutationFn: (periodId: number) => endSupplyPeriod(meteringPointId, periodId, new Date().toISOString()),
    onSuccess: () =>
      void client.invalidateQueries({ queryKey: ["energy", "metering-points", meteringPointId, "supply-periods"] }),
    onError: (error) => notifications.show({ color: "red", title: t("couldNotEndPeriod"), message: error.message }),
  });
  const customerName = (id: number) =>
    customers?.data.find((customer) => customer.id === id)?.name ?? t("customerNumber", { id });
  const supplyPeriodDate = (value: string | null | undefined) =>
    value ? formatters.formatDate(value, { dateStyle: "medium", timeZone: MARKET_TIME_ZONE }) : t("openEnded");
  const meterTimestamp = (value: string | null | undefined) =>
    value
      ? formatters.formatDate(value, { dateStyle: "medium", timeStyle: "short", timeZone: MARKET_TIME_ZONE })
      : t("openEnded");
  const timestampDate = (value: string | null | undefined) =>
    value ? formatters.formatDate(value, { dateStyle: "medium", timeZone: MARKET_TIME_ZONE }) : t("openEnded");
  const timestampTime = (value: string | null | undefined) =>
    value
      ? formatters.formatDate(value, {
          hour: "2-digit",
          minute: "2-digit",
          hour12: false,
          timeZone: MARKET_TIME_ZONE,
        })
      : t("openEnded");
  const consumptionDayHeader = (value: string) =>
    formatters.formatDate(value, { dateStyle: "long", timeZone: MARKET_TIME_ZONE });
  const hasActivePeriod = periods?.some((period) => period.status === "Active") ?? false;
  return (
    <Stack gap="lg">
      <Breadcrumbs>
        <Anchor
          component={Link}
          to={`${tenantSlug ? `/${encodeURIComponent(tenantSlug)}` : ""}/energy/metering-points` as never}
          size="sm"
        >
          {t("meteringPoints")}
        </Anchor>
        <Text size="sm">{point.gsrn}</Text>
      </Breadcrumbs>
      <PageHeader
        eyebrow={t("energy")}
        title={
          <>
            <IconBolt size={28} /> {point.gsrn}
          </>
        }
        description={`${point.address.streetAddress}, ${point.address.postalCode} ${point.address.city}`}
        actions={
          <Button
            variant="default"
            leftSection={<IconPencil size={16} />}
            onClick={() => setEditState({ mode: "edit", meteringPoint: point })}
          >
            {t("editMeteringPointAction")}
          </Button>
        }
      />
      <MeteringPointFormModal state={editState} onClose={() => setEditState(null)} />
      <Card withBorder>
        <Stack>
          <Title order={3}>{t("meteringPointDetails")}</Title>
          <Group>
            <Text>
              <b>{t("meterNumber")}:</b> {point.meterNumber ?? t("notAvailable")}
            </Text>
            <Text>
              <b>{t("priceArea")}:</b> {point.priceArea}
            </Text>
            <Text>
              <b>{t("gridArea")}:</b> {point.gridArea ?? t("notAvailable")}
            </Text>
            <Text>
              <b>{t("connection")}:</b> <Badge>{t(`connectionStatus${point.connectionStatus}`)}</Badge>
            </Text>
            <Text>
              <b>{t("expectedAnnualConsumption")}:</b>{" "}
              {point.expectedAnnualConsumptionKwh !== undefined
                ? `${formatters.formatNumber(point.expectedAnnualConsumptionKwh, { maximumFractionDigits: 0 })} kWh`
                : t("notAvailable")}
            </Text>
          </Group>
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Group justify="space-between">
            <Title order={3}>{t("meterHistory")}</Title>
            <Button onClick={() => setReplaceMeterOpen(true)}>{t("replaceMeter")}</Button>
          </Group>
          <ReplaceMeterModal
            meteringPointId={meteringPointId}
            opened={replaceMeterOpen}
            onClose={() => setReplaceMeterOpen(false)}
          />
          {meters && meters.length > 0 ? (
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("meterNumber")}</Table.Th>
                  <Table.Th>{t("installed")}</Table.Th>
                  <Table.Th>{t("removed")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {meters.map((meter) => (
                  <Table.Tr key={meter.id}>
                    <Table.Td>{meter.meterNumber}</Table.Td>
                    <Table.Td>{meterTimestamp(meter.installedAt)}</Table.Td>
                    <Table.Td>{meterTimestamp(meter.removedAt)}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          ) : (
            <Text c="dimmed">{t("noMeterHistory")}</Text>
          )}
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Group justify="space-between">
            <Title order={3}>{t("supplyPeriods")}</Title>
            <Button leftSection={<IconPlus size={16} />} onClick={() => setSupplyPeriodModalOpen(true)}>
              {hasActivePeriod ? t("switchCustomer") : t("assignCustomer")}
            </Button>
          </Group>
          <SupplyPeriodModal
            meteringPointId={meteringPointId}
            opened={supplyPeriodModalOpen}
            onClose={() => setSupplyPeriodModalOpen(false)}
            hasActivePeriod={hasActivePeriod}
          />
          {periods && periods.length > 0 ? (
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("customer")}</Table.Th>
                  <Table.Th>{t("start")}</Table.Th>
                  <Table.Th>{t("end")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {periods.map((period) => (
                  <Table.Tr key={period.id}>
                    <Table.Td>{customerName(period.customerId)}</Table.Td>
                    <Table.Td>{supplyPeriodDate(period.start)}</Table.Td>
                    <Table.Td>{supplyPeriodDate(period.end)}</Table.Td>
                    <Table.Td>
                      <Badge color={period.status === "Active" ? "teal" : "gray"}>
                        {t(`supplyPeriodStatus${period.status}`)}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      {period.status === "Active" && (
                        <Button size="compact-sm" variant="light" onClick={() => endMutation.mutate(period.id)}>
                          {t("endPeriod")}
                        </Button>
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          ) : (
            <Text c="dimmed">{t("noSupplyPeriods")}</Text>
          )}
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Group justify="space-between">
            <Title order={3}>{t("consumption")}</Title>
            <Button leftSection={<IconPlus size={16} />} onClick={() => setReadingOpen(true)}>
              {t("addManualReading")}
            </Button>
          </Group>
          <Group>
            <DateInput
              label={t("from")}
              value={from}
              valueFormat={t("dateInputFormat")}
              onChange={(value) => value && setFrom(value)}
              leftSection={<IconCalendar size={16} />}
            />
            <DateInput
              label={t("to")}
              value={to}
              valueFormat={t("dateInputFormat")}
              onChange={(value) => value && setTo(value)}
              leftSection={<IconCalendar size={16} />}
            />
          </Group>
          <SegmentedControl
            aria-label={t("consumptionResolution")}
            value={resolution}
            onChange={(value) => setResolution(value as ConsumptionResolution)}
            data={[
              { label: t("hour"), value: "hour" },
              { label: t("day"), value: "day" },
              { label: t("month"), value: "month" },
            ]}
          />
          <ConsumptionChart aggregates={aggregates ?? []} resolution={resolution} />
          {resolution === "hour" ? (
            consumption && consumption.length > 0 ? (
              <Table>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("start")}</Table.Th>
                    <Table.Th>{t("end")}</Table.Th>
                    <Table.Th>{t("quantityKwh")}</Table.Th>
                    <Table.Th>{t("quality")}</Table.Th>
                    <Table.Th>{t("source")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {consumption.map((item, index) => {
                    const previousItem = consumption[index - 1];
                    const startsNewDay =
                      resolution === "hour" &&
                      (!previousItem || marketDayKey(previousItem.start) !== marketDayKey(item.start));
                    return (
                      <Fragment key={item.id}>
                        {startsNewDay && (
                          <Table.Tr key={`${item.id}-day`}>
                            <Table.Td colSpan={5} bg="gray.0">
                              <Text size="sm" fw={600} c="dimmed">
                                {consumptionDayHeader(item.start)}
                              </Text>
                            </Table.Td>
                          </Table.Tr>
                        )}
                        <Table.Tr>
                          <Table.Td>
                            {resolution === "hour" ? timestampTime(item.start) : timestampDate(item.start)}
                          </Table.Td>
                          <Table.Td>
                            {resolution === "hour" ? timestampTime(item.end) : timestampDate(item.end)}
                          </Table.Td>
                          <Table.Td>{formatters.formatNumber(item.quantityKwh)}</Table.Td>
                          <Table.Td>{t(`quality${item.quality}`)}</Table.Td>
                          <Table.Td>{t(`source${item.source}`)}</Table.Td>
                        </Table.Tr>
                      </Fragment>
                    );
                  })}
                </Table.Tbody>
              </Table>
            ) : (
              <Alert color="gray">{t("noReadingsForRange")}</Alert>
            )
          ) : aggregates && aggregates.length > 0 ? (
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("start")}</Table.Th>
                  <Table.Th>{t("end")}</Table.Th>
                  <Table.Th>{t("quantityKwh")}</Table.Th>
                  <Table.Th>{t("intervals")}</Table.Th>
                  <Table.Th>{t("quality")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {aggregates.map((item) => {
                  const dateOptions =
                    resolution === "day"
                      ? { dateStyle: "medium" as const, timeZone: MARKET_TIME_ZONE }
                      : { year: "numeric" as const, month: "long" as const, timeZone: MARKET_TIME_ZONE };
                  const formatBucket = (value: string) => formatters.formatDate(value, dateOptions);
                  return (
                    <Table.Tr key={item.bucketStart}>
                      <Table.Td>{formatBucket(item.bucketStart)}</Table.Td>
                      <Table.Td>{formatBucket(item.bucketEnd)}</Table.Td>
                      <Table.Td>{formatters.formatNumber(item.quantityKwh)}</Table.Td>
                      <Table.Td>{formatters.formatNumber(item.intervalCount)}</Table.Td>
                      <Table.Td>
                        <Badge color={item.hasEstimated ? "yellow" : "gray"} variant="light">
                          {item.hasEstimated ? t("qualityAggregateEstimated") : t("qualityAggregateMeasured")}
                        </Badge>
                      </Table.Td>
                    </Table.Tr>
                  );
                })}
              </Table.Tbody>
            </Table>
          ) : (
            <Alert color="gray">{t("noReadingsForRange")}</Alert>
          )}
          <ManualReadingModal
            meteringPointId={meteringPointId}
            opened={readingOpen}
            onClose={() => setReadingOpen(false)}
          />
        </Stack>
      </Card>
    </Stack>
  );
};
