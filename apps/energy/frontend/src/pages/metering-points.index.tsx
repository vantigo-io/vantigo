import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  Group,
  Pagination,
  Select,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
  UnstyledButton,
} from "@mantine/core";
import {
  IconAlertCircle,
  IconBolt,
  IconChevronDown,
  IconChevronUp,
  IconPencil,
  IconPlus,
  IconSearch,
  IconSelector,
  IconUpload,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useDebouncedListSearch, useI18n } from "@vantigo/frontend-shell";
import { type MouseEvent, useState } from "react";
import { customersQueryOptions } from "../api/customers";
import {
  CONNECTION_STATUSES,
  type ConnectionStatus,
  type MeteringPointQueryParams,
  type MeteringPointSortBy,
  meteringPointsQueryOptions,
  PRICE_AREAS,
  type PriceArea,
} from "../api/energy";
import "../i18n";
import { MeteringPointFormModal, type MeteringPointModalState } from "./-metering-point-form-modal";

const PAGE_SIZE = 25;
// The Selects' own "no filter" entry: the URL carries no filter as `undefined`,
// but a Mantine Select needs a real string among its `data` to offer "All".
const ALL = "";

/** The list's URL state: every filter and the sort survive a refresh and a pasted link. */
export interface MeteringPointsSearch {
  page: number;
  search: string;
  /** Present only when true: the points with no customer now (energy import design D9). */
  withoutCustomer?: boolean;
  customerId?: number;
  priceArea?: PriceArea;
  connectionStatus?: ConnectionStatus;
  sortBy?: MeteringPointSortBy;
  sortDirection?: "asc" | "desc";
}

const statusColor = (status: ConnectionStatus) => ({ New: "blue", Connected: "teal", Disconnected: "red" })[status];

interface SortableHeaderProps {
  column: MeteringPointSortBy;
  label: string;
  sortBy?: MeteringPointSortBy;
  sortDirection?: "asc" | "desc";
  onSort: (column: MeteringPointSortBy) => void;
}

/**
 * A column header that is also the control for sorting by it, as the
 * customers list's: `aria-sort` on the `<th>`, the chevron showing the
 * direction to a sighted user.
 */
const SortableHeader = ({ column, label, sortBy, sortDirection, onSort }: SortableHeaderProps) => {
  const active = sortBy === column;
  const ariaSort = !active ? "none" : sortDirection === "desc" ? "descending" : "ascending";
  const Icon = !active ? IconSelector : sortDirection === "desc" ? IconChevronDown : IconChevronUp;
  return (
    <Table.Th aria-sort={ariaSort}>
      <UnstyledButton
        type="button"
        onClick={() => onSort(column)}
        style={{ display: "flex", alignItems: "center", gap: 4 }}
      >
        <Text fw={700} size="sm">
          {label}
        </Text>
        <Icon size={14} />
      </UnstyledButton>
    </Table.Th>
  );
};

/**
 * The metering-point list: sortable by its columns and filtered by price area,
 * connection status and customer, all on the server — the list is paged.
 * `canImport` is the host's answer to whether the caller holds `energy:import`
 * and `energy:metering-points-manage` (energy import design D9); it puts
 * **Import** in the header, a link to the import page. **Without customer**
 * keeps the points with no customer now — the list to attach the points an
 * import left without one. `canViewCustomers` — `energy:supply-periods-view`,
 * since who a point supplies is a supply period's fact — puts the Customer
 * column first and the Customer filter beside the others; `canOpenCustomers`
 * — `customers:view` — makes each name a link to the customer.
 */
export const MeteringPointsPage = ({
  canImport = false,
  canViewTariffs = false,
  canViewCustomers = false,
  canOpenCustomers = false,
}: {
  canImport?: boolean;
  /** `energy:tariffs-view`: the new point's grid tariff is chosen from the active ones. */
  canViewTariffs?: boolean;
  canViewCustomers?: boolean;
  canOpenCustomers?: boolean;
}) => {
  const { t } = useI18n("energy");
  const { page, search, withoutCustomer, customerId, priceArea, connectionStatus, sortBy, sortDirection } = useSearch({
    strict: false,
  }) as MeteringPointsSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  // Everything but the page and the search box, which useDebouncedListSearch
  // owns: typing or paging keeps every filter and the sort.
  const rest = {
    ...(withoutCustomer ? { withoutCustomer: true } : {}),
    customerId: canViewCustomers ? customerId : undefined,
    priceArea,
    connectionStatus,
    sortBy,
    sortDirection,
  };
  const { searchInput, setSearchInput, onPageChange } = useDebouncedListSearch({
    currentSearch: search,
    onNavigate: (next, options) => void navigate({ search: { ...rest, ...next }, ...options }),
  });
  const refine = (next: Partial<MeteringPointsSearch>) =>
    void navigate({ search: { search, ...rest, ...next, page: 1 } });
  const toggleSort = (column: MeteringPointSortBy) =>
    refine(
      sortBy !== column
        ? { sortBy: column, sortDirection: "asc" }
        : sortDirection === "asc"
          ? { sortBy: column, sortDirection: "desc" }
          : { sortBy: undefined, sortDirection: undefined },
    );
  const [modalState, setModalState] = useState<MeteringPointModalState | null>(null);
  const [customerSearch, setCustomerSearch] = useState("");
  const { data: customers } = useQuery({ ...customersQueryOptions(customerSearch), enabled: canViewCustomers });
  const params: MeteringPointQueryParams = {
    page,
    pageSize: PAGE_SIZE,
    search: search || undefined,
    withoutCustomer: withoutCustomer || undefined,
    customerId: rest.customerId,
    priceArea,
    connectionStatus,
    sortBy,
    sortDirection,
  };
  const { data, isPending, isError, error } = useQuery(meteringPointsQueryOptions(params));
  const statusLabel = (status: ConnectionStatus) =>
    t(
      `connectionStatus${status}` as
        | "connectionStatusNew"
        | "connectionStatusConnected"
        | "connectionStatusDisconnected",
    );
  // The filtered customer stays among the options while the search box looks
  // for another, or the Select would go blank over a filtered list.
  const filteredCustomer = data?.data.find((point) => point.currentCustomer?.id === customerId)?.currentCustomer;
  const customerOptions = [
    { value: ALL, label: t("allCustomers") },
    ...(filteredCustomer && !customers?.data.some((customer) => customer.id === filteredCustomer.id)
      ? [
          {
            value: String(filteredCustomer.id),
            label: filteredCustomer.name ?? t("customerNumber", { id: filteredCustomer.id }),
          },
        ]
      : []),
    ...(customers?.data ?? []).map((customer) => ({ value: String(customer.id), label: customer.name })),
  ];
  const sortable = (column: MeteringPointSortBy, label: string) => (
    <SortableHeader column={column} label={label} sortBy={sortBy} sortDirection={sortDirection} onSort={toggleSort} />
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
            <Button
              data-assist="energy.metering-points.new"
              leftSection={<IconPlus size={16} />}
              onClick={() => setModalState({ mode: "create" })}
            >
              {t("newMeteringPoint")}
            </Button>
          </Group>
        }
      />
      <MeteringPointFormModal state={modalState} onClose={() => setModalState(null)} canViewTariffs={canViewTariffs} />
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group gap="md" align="end" wrap="wrap">
            <TextInput
              placeholder={t("searchMeteringPoints")}
              leftSection={<IconSearch size={16} />}
              value={searchInput}
              onChange={(event) => setSearchInput(event.currentTarget.value)}
              maw={420}
              style={{ flex: 1 }}
            />
            {canViewCustomers && (
              <Select
                label={t("customer")}
                w={220}
                searchable
                allowDeselect={false}
                data={customerOptions}
                value={customerId ? String(customerId) : ALL}
                onSearchChange={setCustomerSearch}
                filter={({ options }) => options}
                onChange={(value) => refine({ customerId: value ? Number(value) : undefined })}
              />
            )}
            <Select
              label={t("priceArea")}
              w={160}
              allowDeselect={false}
              data={[
                { value: ALL, label: t("allPriceAreas") },
                ...PRICE_AREAS.map((area) => ({ value: area, label: area })),
              ]}
              value={priceArea ?? ALL}
              onChange={(value) => refine({ priceArea: (value || undefined) as PriceArea | undefined })}
            />
            <Select
              label={t("status")}
              w={160}
              allowDeselect={false}
              data={[
                { value: ALL, label: t("allStatuses") },
                ...CONNECTION_STATUSES.map((status) => ({ value: status, label: statusLabel(status) })),
              ]}
              value={connectionStatus ?? ALL}
              onChange={(value) => refine({ connectionStatus: (value || undefined) as ConnectionStatus | undefined })}
            />
            <Switch
              label={t("withoutCustomer")}
              checked={!!withoutCustomer}
              onChange={(event) => refine({ withoutCustomer: event.currentTarget.checked || undefined })}
              mb={8}
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
              <Table.ScrollContainer minWidth={canViewCustomers ? 1060 : 860}>
                <Table striped highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      {canViewCustomers && <Table.Th>{t("customer")}</Table.Th>}
                      {sortable("gsrn", t("gsrn"))}
                      {sortable("meterNumber", t("meterNumber"))}
                      {sortable("address", t("address"))}
                      {sortable("priceArea", t("priceArea"))}
                      {sortable("connectionStatus", t("status"))}
                      <Table.Th w={48} aria-label={t("actions")} />
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.data.map((point) => {
                      const customer = point.currentCustomer;
                      const customerName = customer && (customer.name ?? t("customerNumber", { id: customer.id }));
                      return (
                        <Table.Tr
                          key={point.id}
                          style={{ cursor: "pointer" }}
                          onClick={() =>
                            void navigate({
                              href: `/energy/metering-points/${point.id}`,
                            })
                          }
                        >
                          {canViewCustomers && (
                            <Table.Td>
                              {!customer ? (
                                <Text c="dimmed" component="span" size="sm">
                                  {t("noCustomer")}
                                </Text>
                              ) : canOpenCustomers ? (
                                <Anchor
                                  component={Link}
                                  to={`/customers/${customer.id}`}
                                  onClick={(event: MouseEvent) => event.stopPropagation()}
                                >
                                  {customerName}
                                </Anchor>
                              ) : (
                                customerName
                              )}
                            </Table.Td>
                          )}
                          <Table.Td>{point.gsrn}</Table.Td>
                          <Table.Td>{point.meterNumber ?? t("notAvailable")}</Table.Td>
                          <Table.Td>
                            {point.address.streetAddress}, {point.address.postalCode} {point.address.city}
                          </Table.Td>
                          <Table.Td>{point.priceArea}</Table.Td>
                          <Table.Td>
                            <Badge color={statusColor(point.connectionStatus)}>
                              {statusLabel(point.connectionStatus)}
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
                      );
                    })}
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
