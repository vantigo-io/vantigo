import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Pagination,
  Stack,
  Switch,
  Table,
  TextInput,
} from "@mantine/core";
import { IconAlertCircle, IconBolt, IconPencil, IconPlus, IconSearch, IconUpload } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useDebouncedListSearch, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type ConnectionStatus, meteringPointsQueryOptions } from "../api/energy";
import "../i18n";
import { MeteringPointFormModal, type MeteringPointModalState } from "./-metering-point-form-modal";

const PAGE_SIZE = 25;
interface MeteringPointsSearch {
  page: number;
  search: string;
  /** Present only when true: the points with no customer now (energy import design D9). */
  withoutCustomer?: boolean;
}

const statusColor = (status: ConnectionStatus) => ({ New: "blue", Connected: "teal", Disconnected: "red" })[status];

/**
 * The metering-point list. `canImport` is the host's answer to whether the
 * caller holds `energy:import` and `energy:metering-points-manage` (energy
 * import design D9); it puts **Import** in the header, a link to the import
 * page. **Without customer** keeps the points with no customer now — the list
 * to attach the points an import left without one.
 */
export const MeteringPointsPage = ({
  canImport = false,
  canViewTariffs = false,
}: {
  canImport?: boolean;
  /** `energy:tariffs-view`: the new point's grid tariff is chosen from the active ones. */
  canViewTariffs?: boolean;
}) => {
  const { t } = useI18n("energy");
  const { page, search, withoutCustomer } = useSearch({ strict: false }) as MeteringPointsSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const filter = withoutCustomer ? { withoutCustomer: true } : {};
  const { searchInput, setSearchInput, onPageChange } = useDebouncedListSearch({
    currentSearch: search,
    onNavigate: (next, options) => void navigate({ search: { ...next, ...filter }, ...options }),
  });
  const [modalState, setModalState] = useState<MeteringPointModalState | null>(null);
  const { data, isPending, isError, error } = useQuery(
    meteringPointsQueryOptions({
      page,
      pageSize: PAGE_SIZE,
      search: search || undefined,
      withoutCustomer: withoutCustomer || undefined,
    }),
  );

  return (
    <Stack gap="lg">
      <PageHeader
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
          <Group gap="sm">
            {canImport && (
              <Button
                component={Link}
                to="/energy/metering-points/import"
                variant="default"
                leftSection={<IconUpload size={16} />}
              >
                {t("importMeteringPoints")}
              </Button>
            )}
            <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
              {t("newMeteringPoint")}
            </Button>
          </Group>
        }
      />
      <MeteringPointFormModal state={modalState} onClose={() => setModalState(null)} canViewTariffs={canViewTariffs} />
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group gap="md" align="center">
            <TextInput
              placeholder={t("searchMeteringPoints")}
              leftSection={<IconSearch size={16} />}
              value={searchInput}
              onChange={(event) => setSearchInput(event.currentTarget.value)}
              maw={420}
              style={{ flex: 1 }}
            />
            <Switch
              label={t("withoutCustomer")}
              checked={!!withoutCustomer}
              onChange={(event) =>
                void navigate({
                  search: { page: 1, search, ...(event.currentTarget.checked ? { withoutCustomer: true } : {}) },
                })
              }
            />
          </Group>
          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadMeteringPoints")}>
              {error.message}
            </Alert>
          )}
          {isPending && <ContentSkeleton rows={6} rowHeight={52} />}
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
                            href: `/energy/metering-points/${point.id}`,
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
              {data.data.length === 0 && <EmptyState title={t("noMeteringPointsFound")} />}
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
