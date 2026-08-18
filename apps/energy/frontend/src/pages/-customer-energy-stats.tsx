import { SimpleGrid } from "@mantine/core";
import { KpiCard, useI18n } from "@vantigo/frontend-shell";
import type { ConsumptionAggregate, CustomerMeteringPoint } from "../api/energy";
import { summarizeCustomerEnergy } from "../lib/customer-energy";
import "../i18n";

export const CustomerEnergyStats = ({
  meters,
  aggregates,
}: {
  meters: CustomerMeteringPoint[];
  aggregates: ConsumptionAggregate[];
}) => {
  const { t, formatters } = useI18n("energy");
  const stats = summarizeCustomerEnergy(meters, aggregates);
  const formatKwh = (value: number | null) =>
    value === null ? t("notAvailable") : `${formatters.formatNumber(value, { maximumFractionDigits: 0 })} kWh`;
  return (
    <SimpleGrid cols={{ base: 1, xs: 2, md: 4 }}>
      <KpiCard label={t("meteringPointsMetric")} value={formatters.formatNumber(stats.meteringPoints)} />
      <KpiCard label={t("expectedAnnualConsumptionMetric")} value={formatKwh(stats.expectedAnnualKwh)} />
      <KpiCard label={t("consumptionLastTwelveMonths")} value={formatKwh(stats.lastYearKwh)} />
      <KpiCard label={t("activeSupplyPeriods")} value={formatters.formatNumber(stats.activeSupplyPeriods)} />
    </SimpleGrid>
  );
};
