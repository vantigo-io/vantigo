import { Badge, Card, Group, Stack, Table, Text, Title, Tooltip } from "@mantine/core";
import { useNavigate } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import type { ConnectionStatus, CustomerMeteringPoint, SupplyPeriod } from "../api/energy";
import "../i18n";

const statusColor = (status: ConnectionStatus) => ({ New: "blue", Connected: "teal", Disconnected: "red" })[status];
const periodColor = (status: SupplyPeriod["status"]) => ({ Active: "teal", Ended: "gray", Cancelled: "red" })[status];

/** The latest (by start) supply period is the customer-facing one; older periods go in a tooltip. */
const latestPeriod = (periods: SupplyPeriod[]) => [...periods].sort((a, b) => b.start.localeCompare(a.start))[0];

const SupplyPeriodCell = ({
  periods,
  formatPeriod,
  statusLabel,
  notAvailable,
}: {
  periods: SupplyPeriod[];
  formatPeriod: (period: SupplyPeriod) => string;
  statusLabel: (status: SupplyPeriod["status"]) => string;
  notAvailable: string;
}) => {
  const latest = latestPeriod(periods);
  if (!latest) return <Text c="dimmed">{notAvailable}</Text>;
  const cell = (
    <Group gap="xs" wrap="nowrap">
      <Text size="sm">{formatPeriod(latest)}</Text>
      <Badge variant="light" color={periodColor(latest.status)}>
        {statusLabel(latest.status)}
      </Badge>
    </Group>
  );
  if (periods.length <= 1) return cell;
  return (
    <Tooltip
      label={periods.map((period) => `${formatPeriod(period)} (${statusLabel(period.status)})`).join("\n")}
      multiline
      style={{ whiteSpace: "pre-line" }}
    >
      {cell}
    </Tooltip>
  );
};

export const CustomerMetersTable = ({ meters }: { meters: CustomerMeteringPoint[] }) => {
  const { t, formatters } = useI18n("energy");
  const navigate = useNavigate();
  const formatPeriod = (period: SupplyPeriod) =>
    `${formatters.formatDate(period.start, { dateStyle: "medium", timeZone: "UTC" })} – ${period.end ? formatters.formatDate(period.end, { dateStyle: "medium", timeZone: "UTC" }) : t("openEnded")}`;
  const formatKwh = (value: number | undefined | null) =>
    typeof value === "number"
      ? `${formatters.formatNumber(value, { maximumFractionDigits: 0 })} kWh`
      : t("notAvailable");
  if (meters.length === 0)
    return (
      <Card withBorder>
        <Stack align="center">
          <Title order={3}>{t("noCustomerMeteringPoints")}</Title>
          <Text c="dimmed">{t("attachToTrackConsumption")}</Text>
        </Stack>
      </Card>
    );
  return (
    <Table.ScrollContainer minWidth={860}>
      <Table striped highlightOnHover>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("meteringPointId")}</Table.Th>
            <Table.Th>{t("installationAddress")}</Table.Th>
            <Table.Th>{t("priceArea")}</Table.Th>
            <Table.Th>{t("expectedAnnualConsumption")}</Table.Th>
            <Table.Th>{t("supplyPeriods")}</Table.Th>
            <Table.Th>{t("status")}</Table.Th>
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
                <SupplyPeriodCell
                  periods={supplyPeriods}
                  formatPeriod={formatPeriod}
                  statusLabel={(status) => t(`supplyPeriodStatus${status}`)}
                  notAvailable={t("notAvailable")}
                />
              </Table.Td>
              <Table.Td>
                <Badge color={statusColor(meteringPoint.connectionStatus)}>
                  {t(`connectionStatus${meteringPoint.connectionStatus}`)}
                </Badge>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};
