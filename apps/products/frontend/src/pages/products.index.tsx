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
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconAlertCircle, IconPencil, IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { PageHeader } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import { buildCategoryTree, categoriesQueryOptions } from "../api/categories";
import { type ProductResponse, type ProductStatus, productsQueryOptions } from "../api/products";
import { ProductFormModal, type ProductModalState } from "./-product-form-modal";

const PAGE_SIZE = 25;
const UNCATEGORIZED = "uncategorized";
interface ProductsSearch {
  page: number;
  search: string;
  status: ProductStatus | "";
  categoryId: number | typeof UNCATEGORIZED | "";
}

const statusColor = (status: ProductStatus) => ({ Draft: "gray", Active: "teal", Discontinued: "red" })[status];
const nokPrice = (product: ProductResponse) => product.effectivePrices.find((price) => price.currency === "NOK");
const formatPrice = (amount: number | undefined) =>
  amount === undefined ? "—" : new Intl.NumberFormat("nb-NO", { style: "currency", currency: "NOK" }).format(amount);

export const ProductsPage = () => {
  const { page, search, status, categoryId } = useSearch({ strict: false }) as ProductsSearch;
  const navigate = useNavigate() as (options: unknown) => void;
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
      categoryId: typeof categoryId === "number" ? categoryId : undefined,
      uncategorized: categoryId === UNCATEGORIZED || undefined,
    }),
  );
  const { data: categories } = useQuery(categoriesQueryOptions());
  const categoryOptions = [
    { value: UNCATEGORIZED, label: "Uncategorised" },
    ...buildCategoryTree(categories ?? []).map(({ category, depth }) => ({
      value: String(category.id),
      label: `${"\u00A0".repeat(depth * 3)}${category.name}`,
    })),
  ];

  return (
    <Stack gap="lg">
      <PageHeader
        eyebrow="Products"
        title={
          <>
            Products
            {data && (
              <Badge variant="light" size="lg">
                {data.pagination.totalCount} total
              </Badge>
            )}
          </>
        }
        description="Everything you sell, with variants, pricing, and lifecycle status."
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
            New product
          </Button>
        }
      />
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
                void navigate({
                  search: {
                    page: 1,
                    search,
                    status,
                    categoryId: value === UNCATEGORIZED ? UNCATEGORIZED : value ? Number(value) : "",
                  },
                })
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
                      <Table.Th>SKU / variants</Table.Th>
                      <Table.Th>Type</Table.Th>
                      <Table.Th>Category</Table.Th>
                      <Table.Th>Status</Table.Th>
                      <Table.Th>Unit</Table.Th>
                      <Table.Th>Tax category</Table.Th>
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
                        <Table.Td>
                          {(product.variants ?? []).length > 1
                            ? `${product.variants.length} variants`
                            : (product.variants?.[0]?.sku ?? product.sku ?? "—")}
                        </Table.Td>
                        <Table.Td>{product.type}</Table.Td>
                        <Table.Td>{product.category?.name ?? "—"}</Table.Td>
                        <Table.Td>
                          <Badge color={statusColor(product.status)}>{product.status}</Badge>
                        </Table.Td>
                        <Table.Td>{product.unit || "—"}</Table.Td>
                        <Table.Td>
                          {product.taxCategory
                            ? `${product.taxCategory.name} (${(product.taxCategory.rate * 100).toFixed(2)}%)`
                            : "—"}
                        </Table.Td>
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
