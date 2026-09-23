import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Pagination,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
  UnstyledButton,
} from "@mantine/core";
import {
  IconAlertCircle,
  IconChevronDown,
  IconChevronUp,
  IconPencil,
  IconPlus,
  IconSearch,
  IconSelector,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import {
  ContentSkeleton,
  EmptyState,
  KpiCard,
  PageHeader,
  useDebouncedListSearch,
  useI18n,
} from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";

import {
  type CustomerOwnerFilter,
  type CustomerStatusFilter,
  type CustomersListSearch,
  type CustomerType,
  customerStatsQueryOptions,
  customersListParams,
  customersQueryOptions,
} from "../api/customers";
import { customerGroupsQueryOptions } from "../api/groups";
import { customerTagsQueryOptions } from "../api/tags";
import { TagBadge } from "../components/tag-badge";
import "../i18n";
import { CustomerFormModal, type CustomerModalState } from "./-customer-form-modal";
import { ManageGroupsModal } from "./-manage-groups-modal";
import { ManageTagsModal } from "./-manage-tags-modal";

type SortColumn = NonNullable<CustomersListSearch["sortBy"]>;
type SortDirection = NonNullable<CustomersListSearch["sortDirection"]>;

interface CustomersSearch extends CustomersListSearch {
  /** Arrive with the create form open (Spotlight's quick action). Only ever present when true. */
  create?: boolean;
}

// Sentinel for the Select's own "no filter" entry: the search state itself
// carries no filter as `undefined`, but a Mantine Select needs a real string
// among its `data` to show "All open"/"All" as a selectable row rather than
// only reachable through the small clear button.
const ALL_STATUSES = "";
const ALL_TYPES = "";
const ALL_OWNERS = "";
const ALL_TAGS = "";
const ALL_GROUPS = "";

const STATUS_FILTERS: CustomerStatusFilter[] = ["active", "disabled", "archived"];
const TYPE_FILTERS: CustomerType[] = ["business", "person"];
const OWNER_FILTERS: CustomerOwnerFilter[] = ["me", "none"];

const isStatusFilter = (value: string): value is CustomerStatusFilter => (STATUS_FILTERS as string[]).includes(value);
const isTypeFilter = (value: string): value is CustomerType => (TYPE_FILTERS as string[]).includes(value);
const isOwnerFilter = (value: string): value is CustomerOwnerFilter => (OWNER_FILTERS as string[]).includes(value);

interface SortableHeaderProps {
  column: SortColumn;
  label: string;
  sortBy?: SortColumn;
  sortDirection?: SortDirection;
  onSort: (column: SortColumn) => void;
}

/**
 * A column header that is also the control for sorting by it: `aria-sort`
 * lives on the `<th>` itself (the WAI-ARIA-documented place for it), and the
 * chevron inside the button shows the direction to a sighted user. No sibling
 * page had one of these yet, so this is the pattern for the next one.
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

export const CustomersPage = ({ canEdit }: { canEdit?: boolean }) => {
  const { t, formatters } = useI18n("customers");
  const { page, search, status, type, ownerId, tagId, groupId, sortBy, sortDirection, create } = useSearch({
    strict: false,
  }) as CustomersSearch;
  const navigate = useNavigate() as (options: unknown) => void;

  // The filter/sort half of the URL, kept apart from `create`: typing in the
  // search box or turning a page must never resurrect a consumed create
  // intent, but must never drop a filter either (see useDebouncedListSearch
  // below and the Local vs CI note on losing `status` while typing).
  const listSearch: CustomersListSearch = {
    page,
    search,
    status,
    type,
    ownerId,
    tagId,
    groupId,
    sortBy,
    sortDirection,
  };

  const { searchInput, setSearchInput, onPageChange } = useDebouncedListSearch({
    currentSearch: search,
    onNavigate: (next, options) => navigate({ search: { ...listSearch, ...next }, ...options }),
  });
  const filterBy = (next: Partial<Pick<CustomersListSearch, "status" | "type" | "ownerId" | "tagId" | "groupId">>) =>
    navigate({ search: { ...listSearch, ...next, page: 1 } });
  const [manageTagsOpened, setManageTagsOpened] = useState(false);
  const { data: tags } = useQuery(customerTagsQueryOptions());
  // A `tagId` no tag answers narrows the list to nothing while the Tag filter
  // sits blank, so the page shows an empty table and claims to be filtering by
  // nothing — a link that outlived the tag it names, or a tag deleted in another
  // tab. Letting go of it is the same repair `onTagDeleted` makes below, for the
  // deletions this page did not see. Gated on the vocabulary having ACTUALLY
  // loaded: while it is in flight (or has failed) every id is one it does not
  // know, and firing then would throw a live filter away on a slow request.
  useEffect(() => {
    if (!tagId || tags === undefined) return;
    if (!tags.some((tag) => tag.id === tagId)) filterBy({ tagId: undefined });
    // `filterBy` closes over this render's search and is new every render; the
    // URL state it reads is `tagId`, which is in the list.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tagId, tags]);
  const [manageGroupsOpened, setManageGroupsOpened] = useState(false);
  const { data: groups } = useQuery(customerGroupsQueryOptions());
  // Deliberately NO effect dropping a `groupId` the vocabulary does not know,
  // unlike the Tag filter's above: `none` (the customers in no group) is a
  // legitimate value the vocabulary will never contain, so that repair would
  // throw the No-group filter away on every render. A deleted group cannot
  // still have members to filter by, and `onGroupDeleted` below lets go of the
  // one deletion this page does see.
  const toggleSort = (column: SortColumn) => {
    const next: Pick<CustomersListSearch, "sortBy" | "sortDirection"> =
      sortBy !== column
        ? { sortBy: column, sortDirection: "asc" }
        : sortDirection === "asc"
          ? { sortBy: column, sortDirection: "desc" }
          : { sortBy: undefined, sortDirection: undefined };
    navigate({ search: { ...listSearch, ...next, page: 1 } });
  };

  const [modalState, setModalState] = useState<CustomerModalState | null>(null);
  // Open the create form when `create` arrives in the URL, once per arrival:
  // state adjusted during render from the previous render's value, the way
  // React documents, rather than an effect that would flash the closed form.
  const [createSeen, setCreateSeen] = useState(false);
  if (create && !createSeen) {
    setCreateSeen(true);
    setModalState({ mode: "create" });
  }
  if (!create && createSeen) setCreateSeen(false);
  const closeModal = () => {
    setModalState(null);
    // The intent is consumed: closing the form must not reopen it on refresh or back.
    if (create) navigate({ search: listSearch, replace: true });
  };

  const { data, isPending, isError, error } = useQuery(customersQueryOptions(customersListParams(listSearch)));
  const { data: stats } = useQuery(customerStatsQueryOptions());

  // Identity-derived figures are null when this account lacks the legal-identity
  // view permission; hide the related cards and table columns entirely in that case.
  const showIdentity = stats?.businessCount != null;
  const formatCount = (value: number | null | undefined) => (value == null ? "—" : formatters.formatNumber(value));
  const statusLabel = (value: CustomerStatusFilter) =>
    value === "active" ? t("statusActive") : value === "disabled" ? t("statusDisabled") : t("statusArchived");
  const typeLabel = (value: CustomerType) =>
    value === "business" ? t("customerTypeBusiness") : t("customerTypePerson");

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("customers")}
        description={t("customersDescription")}
        actions={
          <Group gap="sm">
            {data && (
              <Badge variant="light" size="lg">
                {t("total", { count: formatters.formatNumber(data.pagination.totalCount) })}
              </Badge>
            )}
            <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
              {t("createNewCustomer")}
            </Button>
          </Group>
        }
      />

      <CustomerFormModal state={modalState} onClose={closeModal} />
      <ManageTagsModal
        opened={manageTagsOpened}
        onClose={() => setManageTagsOpened(false)}
        // A tag the list is filtered by can be deleted from inside that very
        // modal: the URL has to let go of it, or the next fetch narrows the
        // list by an id nothing carries while the Tag filter sits blank.
        onTagDeleted={(deleted) => {
          if (deleted === tagId) filterBy({ tagId: undefined });
        }}
      />
      <ManageGroupsModal
        opened={manageGroupsOpened}
        onClose={() => setManageGroupsOpened(false)}
        // The same repair as the tags': a group the list is filtered by can be
        // deleted from inside that very modal, and the URL has to let go of it.
        onGroupDeleted={(deleted) => {
          if (deleted === groupId) filterBy({ groupId: undefined });
        }}
      />

      {stats && (
        <SimpleGrid cols={{ base: 2, sm: 3, lg: showIdentity ? 7 : 3 }} spacing="sm">
          <KpiCard label={t("statTotalCustomers")} value={formatCount(stats.totalCount)} />
          <KpiCard label={t("statActiveCustomers")} value={formatCount(stats.activeCount)} />
          <KpiCard label={t("statNewLast30Days")} value={formatCount(stats.newLast30DaysCount)} />
          {showIdentity && (
            <>
              <KpiCard label={t("statBusinessCustomers")} value={formatCount(stats.businessCount)} />
              <KpiCard label={t("statPrivateCustomers")} value={formatCount(stats.personCount)} />
              <KpiCard label={t("statMissingIdentity")} value={formatCount(stats.missingIdentityCount)} />
              <KpiCard label={t("statCountries")} value={formatCount(stats.distinctCountryCount)} />
            </>
          )}
        </SimpleGrid>
      )}

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group align="end" wrap="wrap">
            <TextInput
              placeholder={t("searchCustomers")}
              leftSection={<IconSearch size={16} />}
              value={searchInput}
              onChange={(event) => setSearchInput(event.currentTarget.value)}
              maw={400}
            />
            <Select
              label={t("status")}
              w={160}
              allowDeselect={false}
              data={[
                { value: ALL_STATUSES, label: t("statusAllOpen") },
                ...STATUS_FILTERS.map((value) => ({ value, label: statusLabel(value) })),
              ]}
              value={status ?? ALL_STATUSES}
              onChange={(value) => filterBy({ status: value && isStatusFilter(value) ? value : undefined })}
            />
            <Select
              label={t("customerType")}
              w={160}
              allowDeselect={false}
              data={[
                { value: ALL_TYPES, label: t("all") },
                ...TYPE_FILTERS.map((value) => ({ value, label: typeLabel(value) })),
              ]}
              value={type ?? ALL_TYPES}
              onChange={(value) => filterBy({ type: value && isTypeFilter(value) ? value : undefined })}
            />
            <Select
              label={t("owner")}
              w={160}
              allowDeselect={false}
              data={[
                { value: ALL_OWNERS, label: t("all") },
                { value: "me", label: t("ownerMine") },
                { value: "none", label: t("ownerUnassigned") },
              ]}
              value={ownerId ?? ALL_OWNERS}
              onChange={(value) => filterBy({ ownerId: value && isOwnerFilter(value) ? value : undefined })}
            />
            <Group gap="xs" align="end">
              <Select
                label={t("tag")}
                w={160}
                allowDeselect={false}
                data={[
                  { value: ALL_TAGS, label: t("all") },
                  ...(tags ?? []).map((tag) => ({ value: tag.id, label: tag.name })),
                ]}
                value={tagId ?? ALL_TAGS}
                onChange={(value) => filterBy({ tagId: value || undefined })}
              />
              {canEdit && (
                <Button variant="subtle" size="sm" onClick={() => setManageTagsOpened(true)}>
                  {t("manageTags")}
                </Button>
              )}
            </Group>
            <Group gap="xs" align="end">
              <Select
                label={t("group")}
                w={160}
                allowDeselect={false}
                data={[
                  { value: ALL_GROUPS, label: t("all") },
                  { value: "none", label: t("noGroup") },
                  ...(groups ?? []).map((group) => ({ value: group.id, label: group.name })),
                ]}
                value={groupId ?? ALL_GROUPS}
                onChange={(value) => filterBy({ groupId: value || undefined })}
              />
              {canEdit && (
                <Button variant="subtle" size="sm" onClick={() => setManageGroupsOpened(true)}>
                  {t("manageGroups")}
                </Button>
              )}
            </Group>
          </Group>

          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedLoadCustomers")}>
              {error.message}
            </Alert>
          )}

          {isPending && <ContentSkeleton rows={6} rowHeight={52} />}

          {data && (
            <>
              <Table.ScrollContainer minWidth={showIdentity ? 1200 : 1000}>
                <Table striped highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      <SortableHeader
                        column="customerNumber"
                        label={t("customerNumberColumn")}
                        sortBy={sortBy}
                        sortDirection={sortDirection}
                        onSort={toggleSort}
                      />
                      <SortableHeader
                        column="name"
                        label={t("name")}
                        sortBy={sortBy}
                        sortDirection={sortDirection}
                        onSort={toggleSort}
                      />
                      <Table.Th>{t("status")}</Table.Th>
                      <Table.Th>{t("customerType")}</Table.Th>
                      <Table.Th>{t("owner")}</Table.Th>
                      {showIdentity && (
                        <>
                          <Table.Th>{t("countryColumn")}</Table.Th>
                          <Table.Th>{t("legalId")}</Table.Th>
                        </>
                      )}
                      <SortableHeader
                        column="createdAt"
                        label={t("createdColumn")}
                        sortBy={sortBy}
                        sortDirection={sortDirection}
                        onSort={toggleSort}
                      />
                      <Table.Th w={48} aria-label={t("actions")} />
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.data.map((customer) => (
                      <Table.Tr
                        key={customer.id}
                        style={{ cursor: "pointer" }}
                        onClick={() =>
                          navigate({
                            href: `/customers/${customer.id}`,
                          })
                        }
                      >
                        <Table.Td>{customer.customerNumber}</Table.Td>
                        <Table.Td>
                          <Group gap="xs" wrap="wrap">
                            <Text component="span">{customer.name}</Text>
                            {customer.tags.map((tag) => (
                              <TagBadge key={tag.id} tag={tag} />
                            ))}
                          </Group>
                        </Table.Td>
                        <Table.Td>
                          <Badge variant="light" color={customer.status === "active" ? "teal" : "gray"}>
                            {customer.status === "active"
                              ? t("statusActive")
                              : customer.status === "archived"
                                ? t("statusArchived")
                                : t("statusDisabled")}
                          </Badge>
                        </Table.Td>
                        <Table.Td>
                          <Badge variant="light" color={customer.type === "business" ? "indigo" : "grape"}>
                            {customer.type === "business" ? t("customerTypeBusiness") : t("customerTypePerson")}
                          </Badge>
                        </Table.Td>
                        <Table.Td>
                          {customer.owner ? (
                            <Text
                              size="sm"
                              component="span"
                              c={customer.owner.active ? undefined : "dimmed"}
                              title={customer.owner.active ? undefined : t("ownerInactiveHint")}
                            >
                              {customer.owner.displayName}
                            </Text>
                          ) : (
                            <Text c="dimmed" component="span">
                              —
                            </Text>
                          )}
                        </Table.Td>
                        {showIdentity && (
                          <>
                            <Table.Td>
                              {customer.identity ? (
                                customer.identity.country.toUpperCase()
                              ) : (
                                <Text c="dimmed" component="span">
                                  —
                                </Text>
                              )}
                            </Table.Td>
                            <Table.Td>
                              {customer.identity ? (
                                customer.identity.id
                              ) : (
                                <Text c="dimmed" component="span">
                                  —
                                </Text>
                              )}
                            </Table.Td>
                          </>
                        )}
                        <Table.Td>{formatters.formatDate(customer.createdAt)}</Table.Td>
                        <Table.Td onClick={(event) => event.stopPropagation()}>
                          <ActionIcon
                            variant="subtle"
                            color="gray"
                            aria-label={t("editNamed", { name: customer.name })}
                            onClick={() => setModalState({ mode: "edit", customer })}
                          >
                            <IconPencil size={16} />
                          </ActionIcon>
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>

              {data.data.length === 0 && <EmptyState title={t("noCustomersFound")} />}

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
