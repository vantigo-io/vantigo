import { Card, SimpleGrid, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { ConsumptionAggregate, CustomerMeteringPoint } from "../api/energy";
import { summarizeCustomerEnergy } from "../lib/customer-energy";
import "../i18n";

const StatCard = ({ label, value }: { label: string; value: string }) => (
  <Card withBorder>
    <Text size="sm" c="dimmed">
      {label}
    </Text>
    <Text fz="h2" fw={700}>
      {value}
    </Text>
  </Card>
);

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
      <StatCard label={t("meteringPointsMetric")} value={formatters.formatNumber(stats.meteringPoints)} />
      <StatCard label={t("expectedAnnualConsumptionMetric")} value={formatKwh(stats.expectedAnnualKwh)} />
      <StatCard label={t("consumptionLastTwelveMonths")} value={formatKwh(stats.lastYearKwh)} />
      <StatCard label={t("activeSupplyPeriods")} value={formatters.formatNumber(stats.activeSupplyPeriods)} />
    </SimpleGrid>
  );
};
