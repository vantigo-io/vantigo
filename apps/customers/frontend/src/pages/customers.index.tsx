import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Pagination,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { IconAlertCircle, IconPencil, IconPlus, IconSearch } from "@tabler/icons-react";
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
import { useState } from "react";

import { customerStatsQueryOptions, customersQueryOptions } from "../api/customers";
import "../i18n";
import { CustomerFormModal, type CustomerModalState } from "./-customer-form-modal";

const PAGE_SIZE = 25;

interface CustomersSearch {
  page: number;
  search: string;
  /** Arrive with the create form open (Spotlight's quick action). Only ever present when true. */
  create?: boolean;
}

export const CustomersPage = () => {
  const { t, formatters } = useI18n("customers");
  const { page, search, create } = useSearch({ strict: false }) as CustomersSearch;
  const navigate = useNavigate() as (options: unknown) => void;

  const { searchInput, setSearchInput, onPageChange } = useDebouncedListSearch({
    currentSearch: search,
    onNavigate: (next, options) => navigate({ search: next, ...options }),
  });
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
    if (create) navigate({ search: { page, search }, replace: true });
  };

  const { data, isPending, isError, error } = useQuery(
    customersQueryOptions({
      page,
      pageSize: PAGE_SIZE,
      search: search || undefined,
    }),
  );
  const { data: stats } = useQuery(customerStatsQueryOptions());

  // Identity-derived figures are null when this account lacks the legal-identity
  // view permission; hide the related cards and table columns entirely in that case.
  const showIdentity = stats?.businessCount != null;
  const formatCount = (value: number | null | undefined) => (value == null ? "—" : formatters.formatNumber(value));

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
          <TextInput
            placeholder={t("searchCustomers")}
            leftSection={<IconSearch size={16} />}
            value={searchInput}
            onChange={(event) => setSearchInput(event.currentTarget.value)}
            maw={400}
          />

          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedLoadCustomers")}>
              {error.message}
            </Alert>
          )}

          {isPending && <ContentSkeleton rows={6} rowHeight={52} />}

          {data && (
            <>
              <Table.ScrollContainer minWidth={showIdentity ? 920 : 720}>
                <Table striped highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("id")}</Table.Th>
                      <Table.Th>{t("name")}</Table.Th>
                      <Table.Th>{t("status")}</Table.Th>
                      <Table.Th>{t("customerType")}</Table.Th>
                      {showIdentity && (
                        <>
                          <Table.Th>{t("countryColumn")}</Table.Th>
                          <Table.Th>{t("legalId")}</Table.Th>
                        </>
                      )}
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
                        <Table.Td>{customer.id}</Table.Td>
                        <Table.Td>{customer.name}</Table.Td>
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
