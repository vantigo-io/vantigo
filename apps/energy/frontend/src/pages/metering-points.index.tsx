import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Center,
  Group,
  Loader,
  Pagination,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconAlertCircle, IconBolt, IconPencil, IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { PageHeader } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import { type ConnectionStatus, meteringPointsQueryOptions } from "../api/energy";
import { MeteringPointFormModal, type MeteringPointModalState } from "./-metering-point-form-modal";

const PAGE_SIZE = 25;
interface MeteringPointsSearch {
  page: number;
  search: string;
}

const statusColor = (status: ConnectionStatus) => ({ New: "blue", Connected: "teal", Disconnected: "red" })[status];

export const MeteringPointsPage = () => {
  const { page, search } = useSearch({ strict: false }) as MeteringPointsSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const [searchInput, setSearchInput] = useState(search);
  const [debouncedSearch] = useDebouncedValue(searchInput, 300);
  const [modalState, setModalState] = useState<MeteringPointModalState | null>(null);
  useEffect(() => {
    if (debouncedSearch !== search) void navigate({ search: { page: 1, search: debouncedSearch }, replace: true });
  }, [debouncedSearch, search, navigate]);
  const { data, isPending, isError, error } = useQuery(
    meteringPointsQueryOptions({ page, pageSize: PAGE_SIZE, search: search || undefined }),
  );

  return (
    <Stack gap="lg">
      <PageHeader
        eyebrow="Energy"
        title={
          <>
            <IconBolt size={28} /> Metering points{" "}
            {data && (
              <Badge variant="light" size="lg">
                {data.pagination.totalCount} total
              </Badge>
            )}
          </>
        }
        description="Meters, grid connections, supply periods, and consumption readings."
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
            New metering point
          </Button>
        }
      />
      <MeteringPointFormModal state={modalState} onClose={() => setModalState(null)} />
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <TextInput
            placeholder="Search by GSRN, meter number or address..."
            leftSection={<IconSearch size={16} />}
            value={searchInput}
            onChange={(event) => setSearchInput(event.currentTarget.value)}
            maw={420}
          />
          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title="Failed to load metering points">
              {error.message}
            </Alert>
          )}
          {isPending && (
            <Center py="xl">
              <Loader />
            </Center>
          )}
          {data && (
            <>
              <Table.ScrollContainer minWidth={860}>
                <Table striped highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>GSRN</Table.Th>
                      <Table.Th>Meter number</Table.Th>
                      <Table.Th>Address</Table.Th>
                      <Table.Th>Price area</Table.Th>
                      <Table.Th>Status</Table.Th>
                      <Table.Th w={48} aria-label="Actions" />
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.data.map((point) => (
                      <Table.Tr
                        key={point.id}
                        style={{ cursor: "pointer" }}
                        onClick={() =>
                          void navigate({
                            to: "/energy/metering-points/$meteringPointId",
                            params: { meteringPointId: point.id },
                          })
                        }
                      >
                        <Table.Td>{point.gsrn}</Table.Td>
                        <Table.Td>{point.meterNumber ?? "—"}</Table.Td>
                        <Table.Td>
                          {point.address.streetAddress}, {point.address.postalCode} {point.address.city}
                        </Table.Td>
                        <Table.Td>{point.priceArea}</Table.Td>
                        <Table.Td>
                          <Badge color={statusColor(point.connectionStatus)}>{point.connectionStatus}</Badge>
                        </Table.Td>
                        <Table.Td onClick={(event) => event.stopPropagation()}>
                          <ActionIcon
                            variant="subtle"
                            color="gray"
                            aria-label={`Edit ${point.gsrn}`}
                            onClick={() => setModalState({ mode: "edit", meteringPoint: point })}
                          >
                            <IconPencil size={16} />
                          </ActionIcon>
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
              {data.data.length === 0 && (
                <Center py="xl">
                  <Text c="dimmed">No metering points found.</Text>
                </Center>
              )}
              {data.pagination.totalPages > 1 && (
                <Group justify="center">
                  <Pagination
                    total={data.pagination.totalPages}
                    value={page}
                    onChange={(value) => void navigate({ search: { page: value, search } })}
                  />
                </Group>
              )}
            </>
          )}
        </Stack>
      </Card>
    </Stack>
  );
};
