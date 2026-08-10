import { Card, SimpleGrid, Text } from "@mantine/core";
import type { ConsumptionAggregate, CustomerMeteringPoint } from "../api/energy";
import { formatKwh, summarizeCustomerEnergy } from "../lib/customer-energy";

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
  const stats = summarizeCustomerEnergy(meters, aggregates);
  return (
    <SimpleGrid cols={{ base: 1, xs: 2, md: 4 }}>
      <StatCard label="Metering points" value={String(stats.meteringPoints)} />
      <StatCard label="Expected annual consumption" value={formatKwh(stats.expectedAnnualKwh)} />
      <StatCard label="Consumption last 12 months" value={formatKwh(stats.lastYearKwh)} />
      <StatCard label="Active supply periods" value={String(stats.activeSupplyPeriods)} />
    </SimpleGrid>
  );
};
