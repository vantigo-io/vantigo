import {
  Alert,
  Badge,
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
import { IconAlertCircle, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";

import { customersQueryOptions } from "../api/customers";

const PAGE_SIZE = 25;

interface CustomersSearch {
  page: number;
  search: string;
}

const CustomersPage = () => {
  const { page, search } = Route.useSearch();
  const navigate = Route.useNavigate();

  const [searchInput, setSearchInput] = useState(search);
  const [debouncedSearch] = useDebouncedValue(searchInput, 300);

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
        <Title order={2}>Customers</Title>
        {data && (
          <Badge variant="light" size="lg">
            {data.pagination.totalCount} total
          </Badge>
        )}
      </Group>

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
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.data.map((customer) => (
                      <Table.Tr key={customer.id}>
                        <Table.Td>{customer.id}</Table.Td>
                        <Table.Td>{customer.name}</Table.Td>
                        <Table.Td>{customer.identity?.name ?? "—"}</Table.Td>
                        <Table.Td>{customer.identity?.id ?? "—"}</Table.Td>
                        <Table.Td>
                          {customer.identity ? (
                            <Badge variant="light" size="sm">
                              {customer.identity.country}
                            </Badge>
                          ) : (
                            "—"
                          )}
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

export const Route = createFileRoute("/customers")({
  validateSearch: (search: Record<string, unknown>): CustomersSearch => ({
    page: Math.max(1, Number(search.page) || 1),
    search: typeof search.search === "string" ? search.search : "",
  }),
  loaderDeps: ({ search }) => search,
  loader: ({ context: { queryClient }, deps: { page, search } }) =>
    queryClient.ensureQueryData(customersQueryOptions({ page, pageSize: PAGE_SIZE, search: search || undefined })),
  component: CustomersPage,
});
