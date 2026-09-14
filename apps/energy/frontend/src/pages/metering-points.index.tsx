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
import { IconAlertCircle, IconBolt, IconPencil, IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { PageHeader, useDebouncedListSearch, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type ConnectionStatus, meteringPointsQueryOptions } from "../api/energy";
import "../i18n";
import { MeteringPointFormModal, type MeteringPointModalState } from "./-metering-point-form-modal";

const PAGE_SIZE = 25;
interface MeteringPointsSearch {
  page: number;
  search: string;
}

const statusColor = (status: ConnectionStatus) => ({ New: "blue", Connected: "teal", Disconnected: "red" })[status];

export const MeteringPointsPage = () => {
  // The $tenantSlug route param no longer exists (task 3 of the frontend
  // de-tenanting plan collapsed it); task 7 owns removing this idiom.
  const tenantSlug: string | undefined = undefined;
  const { t } = useI18n("energy");
  const { page, search } = useSearch({ strict: false }) as MeteringPointsSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const { searchInput, setSearchInput, onPageChange } = useDebouncedListSearch({
    currentSearch: search,
    onNavigate: (next, options) => void navigate({ search: next, ...options }),
  });
  const [modalState, setModalState] = useState<MeteringPointModalState | null>(null);
  const { data, isPending, isError, error } = useQuery(
    meteringPointsQueryOptions({ page, pageSize: PAGE_SIZE, search: search || undefined }),
  );

  return (
    <Stack gap="lg">
      <PageHeader
        eyebrow={t("energy")}
        title={
          <>
            <IconBolt size={28} /> {t("meteringPoints")}{" "}
            {data && (
              <Badge variant="light" size="lg">
                {t("total", { count: data.pagination.totalCount })}
              </Badge>
            )}
          </>
        }
        description={t("meteringPointsDescription")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
            {t("newMeteringPoint")}
          </Button>
        }
      />
      <MeteringPointFormModal state={modalState} onClose={() => setModalState(null)} />
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <TextInput
            placeholder={t("searchMeteringPoints")}
            leftSection={<IconSearch size={16} />}
            value={searchInput}
            onChange={(event) => setSearchInput(event.currentTarget.value)}
            maw={420}
          />
          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMeteringPoints")}>
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
                      <Table.Th>{t("meterNumber")}</Table.Th>
                      <Table.Th>{t("address")}</Table.Th>
                      <Table.Th>{t("priceArea")}</Table.Th>
                      <Table.Th>{t("status")}</Table.Th>
                      <Table.Th w={48} aria-label={t("actions")} />
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.data.map((point) => (
                      <Table.Tr
                        key={point.id}
                        style={{ cursor: "pointer" }}
                        onClick={() =>
                          void navigate({
                            href: `${tenantSlug ? `/${encodeURIComponent(tenantSlug)}` : ""}/energy/metering-points/${point.id}`,
                          })
                        }
                      >
                        <Table.Td>{point.gsrn}</Table.Td>
                        <Table.Td>{point.meterNumber ?? t("notAvailable")}</Table.Td>
                        <Table.Td>
                          {point.address.streetAddress}, {point.address.postalCode} {point.address.city}
                        </Table.Td>
                        <Table.Td>{point.priceArea}</Table.Td>
                        <Table.Td>
                          <Badge color={statusColor(point.connectionStatus)}>
                            {t(
                              `connectionStatus${point.connectionStatus}` as
                                | "connectionStatusNew"
                                | "connectionStatusConnected"
                                | "connectionStatusDisconnected",
                            )}
                          </Badge>
                        </Table.Td>
                        <Table.Td onClick={(event) => event.stopPropagation()}>
                          <ActionIcon
                            variant="subtle"
                            color="gray"
                            aria-label={t("editMeteringPoint", { gsrn: point.gsrn })}
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
                  <Text c="dimmed">{t("noMeteringPointsFound")}</Text>
                </Center>
              )}
              {data.pagination.totalPages > 1 && (
                <Group justify="center">
                  <Pagination total={data.pagination.totalPages} value={page} onChange={onPageChange} />
                </Group>
              )}
            </>
          )}
        </Stack>
      </Card>
    </Stack>
  );
};
