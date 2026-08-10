import { Badge, Card, Group, SegmentedControl, Stack, Table, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import {
  type ConsumptionResolution,
  type CustomerMeteringPoint,
  customerConsumptionAggregateQueryOptions,
  customerConsumptionQueryOptions,
  type SupplyPeriod,
} from "../api/energy";
import { ConsumptionChart } from "./-consumption-chart";

const formatDate = (value: string | null | undefined) => (value ? new Date(value).toLocaleDateString() : "Open-ended");
const startOfPeriod = (periods: SupplyPeriod[]) =>
  periods.filter((period) => period.status === "Active").sort((a, b) => a.start.localeCompare(b.start))[0]?.start ??
  periods[0]?.start;
const endOfPeriod = (periods: SupplyPeriod[], fallback: string) =>
  periods
    .filter((period) => period.status === "Active")
    .sort((a, b) => (a.end ?? "9999").localeCompare(b.end ?? "9999"))[0]?.end ?? fallback;

export const CustomerMeterCard = ({ item, customerId }: { item: CustomerMeteringPoint; customerId: number }) => {
  // A stable per-mount "now": recomputing it on every render would change the
  // query keys each render and refetch the consumption endpoints in a loop.
  const [now] = useState(() => new Date().toISOString());
  const from =
    startOfPeriod(item.supplyPeriods) ?? new Date(new Date(now).setMonth(new Date(now).getMonth() - 1)).toISOString();
  const to = endOfPeriod(item.supplyPeriods, now);
  const [resolution, setResolution] = useState<ConsumptionResolution>("day");
  const { data: consumption } = useQuery(customerConsumptionQueryOptions(customerId, item.meteringPoint.id, from, to));
  const { data: aggregates } = useQuery(
    customerConsumptionAggregateQueryOptions(customerId, {
      meteringPointId: item.meteringPoint.id,
      from,
      to,
      resolution,
    }),
  );
  return (
    <Card withBorder>
      <Stack>
        <Group justify="space-between">
          <Title order={3}>{item.meteringPoint.gsrn}</Title>
          <Badge>{item.meteringPoint.connectionStatus}</Badge>
        </Group>
        <Text>
          {item.meteringPoint.meterNumber ?? "—"} · {item.meteringPoint.address.streetAddress},{" "}
          {item.meteringPoint.address.city}
        </Text>
        <Text size="sm" c="dimmed">
          Supply period: {formatDate(from)} – {formatDate(to)}
        </Text>
        <SegmentedControl
          aria-label="Consumption resolution"
          value={resolution}
          onChange={(value) => setResolution(value as ConsumptionResolution)}
          data={[
            { label: "Hour", value: "hour" },
            { label: "Day", value: "day" },
            { label: "Month", value: "month" },
          ]}
        />
        <ConsumptionChart aggregates={aggregates ?? []} resolution={resolution} color="blue.6" />
        {consumption && consumption.length > 0 && (
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Start</Table.Th>
                <Table.Th>End</Table.Th>
                <Table.Th>Quantity (kWh)</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {consumption.map((interval) => (
                <Table.Tr key={interval.id}>
                  <Table.Td>{formatDate(interval.start)}</Table.Td>
                  <Table.Td>{formatDate(interval.end)}</Table.Td>
                  <Table.Td>{interval.quantityKwh}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Stack>
    </Card>
  );
};
