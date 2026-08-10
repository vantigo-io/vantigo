import { Badge, Card, Group, Stack, Table, Text, Title, Tooltip } from "@mantine/core";
import { useNavigate } from "@tanstack/react-router";
import type { ConnectionStatus, CustomerMeteringPoint, SupplyPeriod } from "../api/energy";

const statusColor = (status: ConnectionStatus) => ({ New: "blue", Connected: "teal", Disconnected: "red" })[status];
const periodColor = (status: SupplyPeriod["status"]) => ({ Active: "teal", Ended: "gray", Cancelled: "red" })[status];

const formatDate = (value: string) => new Date(value).toLocaleDateString();
const formatPeriod = (period: SupplyPeriod) =>
  `${formatDate(period.start)} – ${period.end ? formatDate(period.end) : "Open-ended"}`;
const formatKwh = (value: number | undefined | null) =>
  typeof value === "number" ? `${new Intl.NumberFormat("nb-NO", { maximumFractionDigits: 0 }).format(value)} kWh` : "—";

/** The latest (by start) supply period is the customer-facing one; older periods go in a tooltip. */
const latestPeriod = (periods: SupplyPeriod[]) => [...periods].sort((a, b) => b.start.localeCompare(a.start))[0];

const SupplyPeriodCell = ({ periods }: { periods: SupplyPeriod[] }) => {
  const latest = latestPeriod(periods);
  if (!latest) return <Text c="dimmed">—</Text>;
  const cell = (
    <Group gap="xs" wrap="nowrap">
      <Text size="sm">{formatPeriod(latest)}</Text>
      <Badge variant="light" color={periodColor(latest.status)}>
        {latest.status}
      </Badge>
    </Group>
  );
  if (periods.length <= 1) return cell;
  return (
    <Tooltip
      label={periods.map((period) => `${formatPeriod(period)} (${period.status})`).join("\n")}
      multiline
      style={{ whiteSpace: "pre-line" }}
    >
      {cell}
    </Tooltip>
  );
};

export const CustomerMetersTable = ({ meters }: { meters: CustomerMeteringPoint[] }) => {
  const navigate = useNavigate();
  if (meters.length === 0)
    return (
      <Card withBorder>
        <Stack align="center">
          <Title order={3}>No metering points</Title>
          <Text c="dimmed">Attach a metering point to start tracking consumption.</Text>
        </Stack>
      </Card>
    );
  return (
    <Table.ScrollContainer minWidth={860}>
      <Table striped highlightOnHover>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Metering point ID</Table.Th>
            <Table.Th>Installation address</Table.Th>
            <Table.Th>Price area</Table.Th>
            <Table.Th>Expected annual consumption</Table.Th>
            <Table.Th>Supply period</Table.Th>
            <Table.Th>Status</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {meters.map(({ meteringPoint, supplyPeriods }) => (
            <Table.Tr
              key={meteringPoint.id}
              style={{ cursor: "pointer" }}
              onClick={() =>
                void navigate({
                  to: "/energy/metering-points/$meteringPointId",
                  params: { meteringPointId: meteringPoint.id },
                })
              }
            >
              <Table.Td>{meteringPoint.gsrn}</Table.Td>
              <Table.Td>
                {meteringPoint.address.streetAddress}, {meteringPoint.address.postalCode} {meteringPoint.address.city}
              </Table.Td>
              <Table.Td>{meteringPoint.priceArea}</Table.Td>
              <Table.Td>{formatKwh(meteringPoint.expectedAnnualConsumptionKwh)}</Table.Td>
              <Table.Td>
                <SupplyPeriodCell periods={supplyPeriods} />
              </Table.Td>
              <Table.Td>
                <Badge color={statusColor(meteringPoint.connectionStatus)}>{meteringPoint.connectionStatus}</Badge>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};
