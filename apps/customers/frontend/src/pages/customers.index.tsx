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
  Title,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconAlertCircle, IconPencil, IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useEffect, useState } from "react";

import { customersQueryOptions } from "../api/customers";
import { LegalCountryBadge, LegalValueBadge, NoValue } from "../components/legal-badges";
import { CustomerFormModal, type CustomerModalState } from "./-customer-form-modal";

const PAGE_SIZE = 25;

interface CustomersSearch {
  page: number;
  search: string;
}

export const CustomersPage = () => {
  const { page, search } = useSearch({ strict: false }) as CustomersSearch;
  const navigate = useNavigate() as (options: unknown) => void;

  const [searchInput, setSearchInput] = useState(search);
  const [debouncedSearch] = useDebouncedValue(searchInput, 300);
  const [modalState, setModalState] = useState<CustomerModalState | null>(null);

  useEffect(() => {
    if (debouncedSearch !== search) {
      navigate({
        search: { page: 1, search: debouncedSearch },
        replace: true,
      });
    }
  }, [debouncedSearch, search, navigate]);

  const { data, isPending, isError, error } = useQuery(
    customersQueryOptions({
      page,
      pageSize: PAGE_SIZE,
      search: search || undefined,
    }),
  );

  return (
    <Stack gap="lg">
      <Group justify="space-between">
        <Group gap="sm">
          <Title order={2}>Customers</Title>
          {data && (
            <Badge variant="light" size="lg">
              {data.pagination.totalCount} total
            </Badge>
          )}
        </Group>
        <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
          Create new customer
        </Button>
      </Group>

      <CustomerFormModal state={modalState} onClose={() => setModalState(null)} />

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <TextInput
            placeholder="Search by name, legal name or legal id..."
            leftSection={<IconSearch size={16} />}
            value={searchInput}
            onChange={(event) => setSearchInput(event.currentTarget.value)}
            maw={400}
          />

          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title="Failed to load customers">
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
              <Table.ScrollContainer minWidth={640}>
                <Table striped highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>Id</Table.Th>
                      <Table.Th>Name</Table.Th>
                      <Table.Th>Legal name</Table.Th>
                      <Table.Th>Legal id</Table.Th>
                      <Table.Th>Country</Table.Th>
                      <Table.Th w={48} aria-label="Actions" />
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.data.map((customer) => (
                      <Table.Tr
                        key={customer.id}
                        style={{ cursor: "pointer" }}
                        onClick={() =>
                          navigate({
                            to: "/customers/$customerId",
                            params: { customerId: customer.id },
                          })
                        }
                      >
                        <Table.Td>{customer.id}</Table.Td>
                        <Table.Td>{customer.name}</Table.Td>
                        <Table.Td>
                          {customer.identity ? (
                            <LegalValueBadge type={customer.identity.type}>{customer.identity.name}</LegalValueBadge>
                          ) : (
                            <NoValue />
                          )}
                        </Table.Td>
                        <Table.Td>
                          {customer.identity ? (
                            <LegalValueBadge type={customer.identity.type}>{customer.identity.id}</LegalValueBadge>
                          ) : (
                            <NoValue />
                          )}
                        </Table.Td>
                        <Table.Td>
                          {customer.identity ? <LegalCountryBadge country={customer.identity.country} /> : <NoValue />}
                        </Table.Td>
                        <Table.Td onClick={(event) => event.stopPropagation()}>
                          <ActionIcon
                            variant="subtle"
                            color="gray"
                            aria-label={`Edit ${customer.name}`}
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

              {data.data.length === 0 && (
                <Center py="xl">
                  <Text c="dimmed">No customers found.</Text>
                </Center>
              )}

              {data.pagination.totalPages > 1 && (
                <Group justify="center">
                  <Pagination
                    total={data.pagination.totalPages}
                    value={page}
                    onChange={(newPage) => navigate({ search: { page: newPage, search } })}
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
