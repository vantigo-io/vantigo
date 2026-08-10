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
  Select,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconAlertCircle, IconCategory, IconPencil, IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { buildCategoryTree, categoriesQueryOptions } from "../api/categories";
import { type ProductResponse, type ProductStatus, productsQueryOptions } from "../api/products";
import { ProductFormModal, type ProductModalState } from "./-product-form-modal";

const PAGE_SIZE = 25;
interface ProductsSearch {
  page: number;
  search: string;
  status: ProductStatus | "";
  categoryId: number | "";
}

const statusColor = (status: ProductStatus) => ({ Draft: "gray", Active: "teal", Discontinued: "red" })[status];
const nokPrice = (product: ProductResponse) => product.effectivePrices.find((price) => price.currency === "NOK");
const formatPrice = (amount: number | undefined) =>
  amount === undefined ? "—" : new Intl.NumberFormat("nb-NO", { style: "currency", currency: "NOK" }).format(amount);

export const ProductsPage = () => {
  const { page, search, status, categoryId } = Route.useSearch();
  const navigate = Route.useNavigate();
  const [searchInput, setSearchInput] = useState(search);
  const [debouncedSearch] = useDebouncedValue(searchInput, 300);
  const [modalState, setModalState] = useState<ProductModalState | null>(null);
  useEffect(() => {
    if (debouncedSearch !== search)
      void navigate({ search: { page: 1, search: debouncedSearch, status, categoryId }, replace: true });
  }, [debouncedSearch, search, status, categoryId, navigate]);
  const { data, isPending, isError, error } = useQuery(
    productsQueryOptions({
      page,
      pageSize: PAGE_SIZE,
      search: search || undefined,
      status: status || undefined,
      categoryId: categoryId || undefined,
    }),
  );
  const { data: categories } = useQuery(categoriesQueryOptions());
  const categoryOptions = buildCategoryTree(categories ?? []).map(({ category, depth }) => ({
    value: String(category.id),
    label: `${"\u00A0".repeat(depth * 3)}${category.name}`,
  }));

  return (
    <Stack gap="lg">
      <Group justify="space-between">
        <Group gap="sm">
          <Title order={2}>Products</Title>
          {data && (
            <Badge variant="light" size="lg">
              {data.pagination.totalCount} total
            </Badge>
          )}
        </Group>
        <Group>
          <Button component={Link} to="/products/categories" variant="default" leftSection={<IconCategory size={16} />}>
            Categories
          </Button>
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
            New product
          </Button>
        </Group>
      </Group>
      <ProductFormModal state={modalState} onClose={() => setModalState(null)} />
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group align="end">
            <TextInput
              placeholder="Search by name or SKU..."
              leftSection={<IconSearch size={16} />}
              value={searchInput}
              onChange={(event) => setSearchInput(event.currentTarget.value)}
              maw={400}
            />
            <Select
              label="Status"
              placeholder="All statuses"
              clearable
              data={["Draft", "Active", "Discontinued"]}
              value={status || null}
              onChange={(value) =>
                void navigate({
                  search: { page: 1, search, status: (value as ProductStatus | null) ?? "", categoryId },
                })
              }
            />
            <Select
              label="Category"
              placeholder="All categories"
              clearable
              searchable
              data={categoryOptions}
              value={categoryId ? String(categoryId) : null}
              onChange={(value) =>
                void navigate({ search: { page: 1, search, status, categoryId: value ? Number(value) : "" } })
              }
            />
          </Group>
          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title="Failed to load products">
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
              <Table.ScrollContainer minWidth={760}>
                <Table striped highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>Name</Table.Th>
                      <Table.Th>SKU</Table.Th>
                      <Table.Th>Type</Table.Th>
                      <Table.Th>Category</Table.Th>
                      <Table.Th>Status</Table.Th>
                      <Table.Th>Unit</Table.Th>
                      <Table.Th>Current NOK price</Table.Th>
                      <Table.Th w={48} aria-label="Actions" />
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.data.map((product) => (
                      <Table.Tr
                        key={product.id}
                        style={{ cursor: "pointer" }}
                        onClick={() => void navigate({ to: "/products/$productId", params: { productId: product.id } })}
                      >
                        <Table.Td>{product.name}</Table.Td>
                        <Table.Td>{product.sku}</Table.Td>
                        <Table.Td>{product.type}</Table.Td>
                        <Table.Td>{product.category?.name ?? "—"}</Table.Td>
                        <Table.Td>
                          <Badge color={statusColor(product.status)}>{product.status}</Badge>
                        </Table.Td>
                        <Table.Td>{product.unit || "—"}</Table.Td>
                        <Table.Td>{formatPrice(nokPrice(product)?.amount)}</Table.Td>
                        <Table.Td onClick={(event) => event.stopPropagation()}>
                          <ActionIcon
                            variant="subtle"
                            color="gray"
                            aria-label={`Edit ${product.name}`}
                            onClick={() => setModalState({ mode: "edit", product })}
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
                  <Text c="dimmed">No products found.</Text>
                </Center>
              )}
              {data.pagination.totalPages > 1 && (
                <Group justify="center">
                  <Pagination
                    total={data.pagination.totalPages}
                    value={page}
                    onChange={(newPage) => void navigate({ search: { page: newPage, search, status, categoryId } })}
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

export const Route = createFileRoute("/products/")({
  validateSearch: (search: Record<string, unknown>): ProductsSearch => ({
    page: Math.max(1, Number(search.page) || 1),
    search: typeof search.search === "string" ? search.search : "",
    status: ["Draft", "Active", "Discontinued"].includes(String(search.status))
      ? (String(search.status) as ProductStatus)
      : "",
    categoryId:
      Number.isInteger(Number(search.categoryId)) && Number(search.categoryId) > 0 ? Number(search.categoryId) : "",
  }),
  loaderDeps: ({ search }) => search,
  loader: ({ context: { queryClient }, deps: { page, search, status, categoryId } }) =>
    queryClient.ensureQueryData(
      productsQueryOptions({
        page,
        pageSize: PAGE_SIZE,
        search: search || undefined,
        status: status || undefined,
        categoryId: categoryId || undefined,
      }),
    ),
  component: ProductsPage,
});
