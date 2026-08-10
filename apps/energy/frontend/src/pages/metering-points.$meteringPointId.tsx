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
import { PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
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
import { ConsumptionChart } from "./-consumption-chart";
import { ManualReadingModal } from "./-manual-reading-modal";
import { MeteringPointFormModal, type MeteringPointModalState } from "./-metering-point-form-modal";
import { ReplaceMeterModal } from "./-replace-meter-modal";
import { SupplyPeriodModal } from "./-supply-period-modal";

const dateOnly = (value: string | null | undefined) => (value ? new Date(value).toLocaleDateString() : "Open-ended");
const defaultFrom = () => {
  const date = new Date();
  date.setMonth(date.getMonth() - 1);
  return date.toISOString().slice(0, 10);
};
const defaultTo = () => new Date().toISOString().slice(0, 10);

export const MeteringPointDetailsPage = () => {
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
  const { data: customers } = useQuery(customersQueryOptions());
  const [editState, setEditState] = useState<MeteringPointModalState | null>(null);
  const [supplyPeriodModalOpen, setSupplyPeriodModalOpen] = useState(false);
  const [readingOpen, setReadingOpen] = useState(false);
  const [replaceMeterOpen, setReplaceMeterOpen] = useState(false);
  const endMutation = useMutation({
    mutationFn: (periodId: number) => endSupplyPeriod(meteringPointId, periodId, new Date().toISOString()),
    onSuccess: () =>
      void client.invalidateQueries({ queryKey: ["energy", "metering-points", meteringPointId, "supply-periods"] }),
    onError: (error) => notifications.show({ color: "red", title: "Could not end period", message: error.message }),
  });
  const customerName = (id: number) =>
    customers?.data.find((customer) => customer.id === id)?.name ?? `Customer #${id}`;
  const hasActivePeriod = periods?.some((period) => period.status === "Active") ?? false;
  return (
    <Stack gap="lg">
      <Breadcrumbs>
        <Anchor component={Link} to="/energy/metering-points" size="sm">
          Metering points
        </Anchor>
        <Text size="sm">{point.gsrn}</Text>
      </Breadcrumbs>
      <PageHeader
        eyebrow="Energy"
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
            Edit metering point
          </Button>
        }
      />
      <MeteringPointFormModal state={editState} onClose={() => setEditState(null)} />
      <Card withBorder>
        <Stack>
          <Title order={3}>Metering point details</Title>
          <Group>
            <Text>
              <b>Meter number:</b> {point.meterNumber ?? "—"}
            </Text>
            <Text>
              <b>Price area:</b> {point.priceArea}
            </Text>
            <Text>
              <b>Grid area:</b> {point.gridArea ?? "—"}
            </Text>
            <Text>
              <b>Connection:</b> <Badge>{point.connectionStatus}</Badge>
            </Text>
            <Text>
              <b>Expected annual consumption:</b>{" "}
              {point.expectedAnnualConsumptionKwh ? `${point.expectedAnnualConsumptionKwh} kWh` : "—"}
            </Text>
          </Group>
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Group justify="space-between">
            <Title order={3}>Meter history</Title>
            <Button onClick={() => setReplaceMeterOpen(true)}>Replace meter</Button>
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
                  <Table.Th>Meter number</Table.Th>
                  <Table.Th>Installed</Table.Th>
                  <Table.Th>Removed</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {meters.map((meter) => (
                  <Table.Tr key={meter.id}>
                    <Table.Td>{meter.meterNumber}</Table.Td>
                    <Table.Td>{dateOnly(meter.installedAt)}</Table.Td>
                    <Table.Td>{dateOnly(meter.removedAt)}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          ) : (
            <Text c="dimmed">No meter history found.</Text>
          )}
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Group justify="space-between">
            <Title order={3}>Supply periods</Title>
            <Button leftSection={<IconPlus size={16} />} onClick={() => setSupplyPeriodModalOpen(true)}>
              {hasActivePeriod ? "Switch customer" : "Assign customer"}
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
                  <Table.Th>Customer</Table.Th>
                  <Table.Th>Start</Table.Th>
                  <Table.Th>End</Table.Th>
                  <Table.Th>Status</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {periods.map((period) => (
                  <Table.Tr key={period.id}>
                    <Table.Td>{customerName(period.customerId)}</Table.Td>
                    <Table.Td>{dateOnly(period.start)}</Table.Td>
                    <Table.Td>{dateOnly(period.end)}</Table.Td>
                    <Table.Td>
                      <Badge color={period.status === "Active" ? "teal" : "gray"}>{period.status}</Badge>
                    </Table.Td>
                    <Table.Td>
                      {period.status === "Active" && (
                        <Button size="compact-sm" variant="light" onClick={() => endMutation.mutate(period.id)}>
                          End period
                        </Button>
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          ) : (
            <Text c="dimmed">No supply periods assigned.</Text>
          )}
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Group justify="space-between">
            <Title order={3}>Consumption</Title>
            <Button leftSection={<IconPlus size={16} />} onClick={() => setReadingOpen(true)}>
              Add manual reading
            </Button>
          </Group>
          <Group>
            <DateInput
              label="From"
              value={from}
              valueFormat="YYYY-MM-DD"
              onChange={(value) => value && setFrom(value)}
              leftSection={<IconCalendar size={16} />}
            />
            <DateInput
              label="To"
              value={to}
              valueFormat="YYYY-MM-DD"
              onChange={(value) => value && setTo(value)}
              leftSection={<IconCalendar size={16} />}
            />
          </Group>
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
          <ConsumptionChart aggregates={aggregates ?? []} resolution={resolution} />
          {consumption && consumption.length > 0 ? (
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Start</Table.Th>
                  <Table.Th>End</Table.Th>
                  <Table.Th>Quantity (kWh)</Table.Th>
                  <Table.Th>Quality</Table.Th>
                  <Table.Th>Source</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {consumption.map((item) => (
                  <Table.Tr key={item.id}>
                    <Table.Td>{dateOnly(item.start)}</Table.Td>
                    <Table.Td>{dateOnly(item.end)}</Table.Td>
                    <Table.Td>{item.quantityKwh}</Table.Td>
                    <Table.Td>{item.quality}</Table.Td>
                    <Table.Td>{item.source}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          ) : (
            <Alert color="gray">No readings found for the selected date range.</Alert>
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
