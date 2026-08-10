import { Anchor, Badge, Card, Group, SegmentedControl, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import {
  type ConsumptionResolution,
  type CustomerMeteringPoint,
  customerConsumptionAggregateQueryOptions,
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
  const navigate = useNavigate();
  // A stable per-mount "now": recomputing it on every render would change the
  // query keys each render and refetch the consumption endpoints in a loop.
  const [now] = useState(() => new Date().toISOString());
  const from =
    startOfPeriod(item.supplyPeriods) ?? new Date(new Date(now).setMonth(new Date(now).getMonth() - 1)).toISOString();
  const to = endOfPeriod(item.supplyPeriods, now);
  const [resolution, setResolution] = useState<ConsumptionResolution>("day");
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
          <Anchor
            fw={700}
            fz="h3"
            onClick={() =>
              void navigate({
                to: "/energy/metering-points/$meteringPointId",
                params: { meteringPointId: item.meteringPoint.id },
              })
            }
          >
            {item.meteringPoint.gsrn}
          </Anchor>
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
      </Stack>
    </Card>
  );
};
