import { Anchor, Badge, Button, Card, Group, SegmentedControl, Stack, Table, Text, Title } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconCalendar, IconFileImport, IconPencil, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { Fragment, useState } from "react";
import {
  billingFacts,
  type ConsumptionResolution,
  consumptionAggregateQueryOptions,
  consumptionQueryOptions,
  endSupplyPeriod,
  meteringPointQueryOptions,
  metersQueryOptions,
  type SupplyPeriod,
  supplyPeriodsQueryOptions,
} from "../api/energy";
import { cadenceKey, taxClassKey, taxReasonKey } from "../lib/billing";
import { MARKET_TIME_ZONE, marketDayKey } from "../lib/market-time";
import { ConsumptionChart } from "./-consumption-chart";
import { ImportReadingsModal } from "./-import-readings-modal";
import { ManualReadingModal } from "./-manual-reading-modal";
import { MeteringPointFormModal, type MeteringPointModalState } from "./-metering-point-form-modal";
import { ReplaceMeterModal } from "./-replace-meter-modal";
import { SettlementsCard } from "./-settlements-card";
import { SupplyPeriodModal } from "./-supply-period-modal";
import { SupplyTermsModal } from "./-supply-terms";
import "../i18n";

const defaultFrom = () => {
  const date = new Date();
  date.setMonth(date.getMonth() - 1);
  return date.toISOString().slice(0, 10);
};
const defaultTo = () => new Date().toISOString().slice(0, 10);

/**
 * One metering point: its details, meters, supply periods and consumption.
 * `canImportReadings` — `energy:import`, `energy:consumption-manage` and
 * `energy:consumption-view` together, which the host reads — puts **Import
 * readings** in the Consumption card (energy import design D8).
 * `canViewTariffs` — `energy:tariffs-view` — lets the edit form and the
 * supply-period dialogs list the tariffs to choose from; without it they show
 * the tariff held, read-only. `canViewSettlements` —
 * `energy:supply-periods-view` and `energy:consumption-view` together — shows
 * the Settlements card. `canOpenCustomers` — `customers:view` — makes each
 * supply period's customer a link to the customer.
 */
export const MeteringPointDetailsPage = ({
  canImportReadings = false,
  canViewTariffs = false,
  canViewSettlements = false,
  canOpenCustomers = false,
}: {
  canImportReadings?: boolean;
  canViewTariffs?: boolean;
  canViewSettlements?: boolean;
  canOpenCustomers?: boolean;
}) => {
  const { t, formatters } = useI18n("energy");
  const { meteringPointId } = useParams({ strict: false }) as { meteringPointId: number };
  const client = useQueryClient();
  const { data: point } = useSuspenseQuery(meteringPointQueryOptions(meteringPointId));
  const { data: periods } = useQuery(supplyPeriodsQueryOptions(meteringPointId));
  const { data: meters } = useQuery(metersQueryOptions(meteringPointId));
  const [from, setFrom] = useState(defaultFrom());
  const [to, setTo] = useState(defaultTo());
  const [resolution, setResolution] = useState<ConsumptionResolution>("day");
  const { data: consumption } = useQuery(consumptionQueryOptions(meteringPointId, from, to));
  const { data: aggregates } = useQuery(consumptionAggregateQueryOptions(meteringPointId, { from, to, resolution }));
  const [editState, setEditState] = useState<MeteringPointModalState | null>(null);
  const [supplyPeriodModalOpen, setSupplyPeriodModalOpen] = useState(false);
  const [readingOpen, setReadingOpen] = useState(false);
  const [importReadingsOpen, setImportReadingsOpen] = useState(false);
  const [replaceMeterOpen, setReplaceMeterOpen] = useState(false);
  const [termsPeriod, setTermsPeriod] = useState<SupplyPeriod | null>(null);
  const facts = billingFacts(point);
  const suggestion = point.electricityTaxSuggestion;
  const endMutation = useMutation({
    mutationFn: (periodId: number) => endSupplyPeriod(meteringPointId, periodId, new Date().toISOString()),
    onSuccess: () =>
      void client.invalidateQueries({ queryKey: ["energy", "metering-points", meteringPointId, "supply-periods"] }),
    onError: (error) => notifications.show({ color: "red", title: t("couldNotEndPeriod"), message: error.message }),
  });
  const customerName = (period: SupplyPeriod) => period.customerName ?? t("customerNumber", { id: period.customerId });
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
      <PageHeader
        breadcrumbs={[{ label: t("meteringPoints"), to: "/energy/metering-points" }, { label: point.gsrn }]}
        title={
          <>
            {point.gsrn}{" "}
            <Badge variant="light" size="lg" title={t("gsrnLabel")}>
              {t("gsrn")}
            </Badge>
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
      <MeteringPointFormModal state={editState} onClose={() => setEditState(null)} canViewTariffs={canViewTariffs} />
      <Card withBorder>
        <Stack>
          <Title order={3}>{t("meteringPointDetails")}</Title>
          <Group>
            <Text>
              <b>{t("gsrnLabel")}:</b> {point.gsrn}
            </Text>
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
          <Title order={3}>{t("billing")}</Title>
          <Group>
            <Text>
              <b>{t("gridTariff")}:</b> {facts.gridTariffName ?? t("noGridTariff")}
            </Text>
            <Text>
              <b>{t("householdUse")}:</b> {facts.household ? t("yes") : t("no")}
            </Text>
            <Text>
              <b>{t("electricityTaxClass")}:</b> {t(taxClassKey(facts.electricityTaxClass))}
            </Text>
            <Text>
              <b>{t("vatTreatment")}:</b>{" "}
              <Badge variant="light" color={facts.vatTreatment === "standard" ? "gray" : "teal"}>
                {facts.vatTreatment === "standard" ? t("vatTreatmentStandard") : t("vatTreatmentExemptHouseholdNorth")}
              </Badge>
            </Text>
          </Group>
          <Text size="xs" c="dimmed">
            {t("vatTreatmentHint")}
          </Text>
          {suggestion && (
            <Stack gap={4}>
              <Group gap="xs">
                <Text size="sm">
                  <b>
                    {suggestion.class
                      ? t("electricityTaxSuggested", { class: t(taxClassKey(suggestion.class)) })
                      : t("electricityTaxNoSuggestion")}
                  </b>
                </Text>
                {!suggestion.matches && (
                  <Badge variant="light" color="yellow">
                    {t("electricityTaxDiffers")}
                  </Badge>
                )}
                {suggestion.uncertain && (
                  <Badge variant="light" color="gray">
                    {t("electricityTaxCheck")}
                  </Badge>
                )}
              </Group>
              <Text size="sm" c="dimmed">
                {t(taxReasonKey(suggestion.reason))}
              </Text>
              {!suggestion.matches && <Text size="sm">{t("electricityTaxDiffersHint")}</Text>}
            </Stack>
          )}
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
            <EmptyState size="sm" title={t("noMeterHistory")} />
          )}
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Group justify="space-between">
            <Title order={3}>{t("supplyPeriods")}</Title>
            <Button
              data-assist="energy.metering-point.assign-customer"
              leftSection={<IconPlus size={16} />}
              onClick={() => setSupplyPeriodModalOpen(true)}
            >
              {hasActivePeriod ? t("switchCustomer") : t("assignCustomer")}
            </Button>
          </Group>
          <SupplyPeriodModal
            meteringPointId={meteringPointId}
            opened={supplyPeriodModalOpen}
            onClose={() => setSupplyPeriodModalOpen(false)}
            hasActivePeriod={hasActivePeriod}
            canViewTariffs={canViewTariffs}
          />
          <SupplyTermsModal
            meteringPointId={meteringPointId}
            period={termsPeriod}
            onClose={() => setTermsPeriod(null)}
            canViewTariffs={canViewTariffs}
          />
          {periods && periods.length > 0 ? (
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("customer")}</Table.Th>
                  <Table.Th>{t("start")}</Table.Th>
                  <Table.Th>{t("end")}</Table.Th>
                  <Table.Th>{t("powerTariff")}</Table.Th>
                  <Table.Th>{t("billingCadence")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {periods.map((period) => (
                  <Table.Tr key={period.id}>
                    <Table.Td>
                      {canOpenCustomers ? (
                        <Anchor component={Link} to={`/customers/${period.customerId}`}>
                          {customerName(period)}
                        </Anchor>
                      ) : (
                        customerName(period)
                      )}
                    </Table.Td>
                    <Table.Td>{supplyPeriodDate(period.start)}</Table.Td>
                    <Table.Td>{supplyPeriodDate(period.end)}</Table.Td>
                    <Table.Td>{period.tariffName ?? t("noPowerTariff")}</Table.Td>
                    <Table.Td>{t(cadenceKey(period.cadence ?? "monthly"))}</Table.Td>
                    <Table.Td>
                      <Badge color={period.status === "Active" ? "teal" : "gray"}>
                        {t(`supplyPeriodStatus${period.status}`)}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Group gap="xs" wrap="nowrap" justify="flex-end">
                        {period.status !== "Cancelled" && (
                          <Button size="compact-sm" variant="default" onClick={() => setTermsPeriod(period)}>
                            {t("changeSupplyTerms")}
                          </Button>
                        )}
                        {period.status === "Active" && (
                          <Button size="compact-sm" variant="light" onClick={() => endMutation.mutate(period.id)}>
                            {t("endPeriod")}
                          </Button>
                        )}
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          ) : (
            <EmptyState size="sm" title={t("noSupplyPeriods")} />
          )}
        </Stack>
      </Card>
      {canViewSettlements && <SettlementsCard meteringPointId={meteringPointId} />}
      <Card withBorder>
        <Stack>
          <Group justify="space-between">
            <Title order={3}>{t("consumption")}</Title>
            <Group gap="xs">
              {canImportReadings && (
                <Button
                  variant="default"
                  leftSection={<IconFileImport size={16} />}
                  onClick={() => setImportReadingsOpen(true)}
                >
                  {t("importReadings")}
                </Button>
              )}
              <Button leftSection={<IconPlus size={16} />} onClick={() => setReadingOpen(true)}>
                {t("addManualReading")}
              </Button>
            </Group>
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
              <EmptyState size="sm" title={t("noReadingsForRange")} />
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
            <EmptyState size="sm" title={t("noReadingsForRange")} />
          )}
          <ManualReadingModal
            meteringPointId={meteringPointId}
            opened={readingOpen}
            onClose={() => setReadingOpen(false)}
          />
          {canImportReadings && (
            <ImportReadingsModal
              meteringPointId={meteringPointId}
              opened={importReadingsOpen}
              onClose={() => setImportReadingsOpen(false)}
            />
          )}
        </Stack>
      </Card>
    </Stack>
  );
};
