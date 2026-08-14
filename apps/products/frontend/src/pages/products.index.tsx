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
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import "../i18n";
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
export const ProductsPage = () => {
  const { t, formatters } = useI18n("products");
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
    { value: UNCATEGORIZED, label: t("products.uncategorised") },
    ...buildCategoryTree(categories ?? []).map(({ category, depth }) => ({
      value: String(category.id),
      label: `${"\u00A0".repeat(depth * 3)}${category.name}`,
    })),
  ];

  return (
    <Stack gap="lg">
      <PageHeader
        eyebrow={t("navigation.products")}
        title={
          <>
            {t("products.heading")}
            {data && (
              <Badge variant="light" size="lg">
                {t("products.total", { count: data.pagination.totalCount })}
              </Badge>
            )}
          </>
        }
        description={t("products.description")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
            {t("products.newProduct")}
          </Button>
        }
      />
      <ProductFormModal state={modalState} onClose={() => setModalState(null)} />
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group align="end">
            <TextInput
              placeholder={t("products.searchPlaceholder")}
              leftSection={<IconSearch size={16} />}
              value={searchInput}
              onChange={(event) => setSearchInput(event.currentTarget.value)}
              maw={400}
            />
            <Select
              label={t("common.status")}
              placeholder={t("products.allStatuses")}
              clearable
              data={["Draft", "Active", "Discontinued"].map((item) => ({ value: item, label: t(`status.${item}`) }))}
              value={status || null}
              onChange={(value) =>
                void navigate({
                  search: { page: 1, search, status: (value as ProductStatus | null) ?? "", categoryId },
                })
              }
            />
            <Select
              label={t("common.category")}
              placeholder={t("products.allCategories")}
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
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("products.failedToLoad")}>
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
                      <Table.Th>{t("common.name")}</Table.Th>
                      <Table.Th>{t("products.skuVariants")}</Table.Th>
                      <Table.Th>{t("common.type")}</Table.Th>
                      <Table.Th>{t("common.category")}</Table.Th>
                      <Table.Th>{t("common.status")}</Table.Th>
                      <Table.Th>{t("common.unit")}</Table.Th>
                      <Table.Th>{t("common.taxCategory")}</Table.Th>
                      <Table.Th>{t("products.currentNokPrice", { currency: "NOK" })}</Table.Th>
                      <Table.Th w={48} aria-label={t("common.actions")} />
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.data.map((product) => {
                      const price = nokPrice(product);
                      return (
                        <Table.Tr
                          key={product.id}
                          style={{ cursor: "pointer" }}
                          onClick={() =>
                            void navigate({ to: "/products/$productId", params: { productId: product.id } })
                          }
                        >
                          <Table.Td>{product.name}</Table.Td>
                          <Table.Td>
                            {(product.variants ?? []).length > 1
                              ? t("products.variantsCount", { count: product.variants.length })
                              : (product.variants?.[0]?.sku ?? product.sku ?? t("common.noValue"))}
                          </Table.Td>
                          <Table.Td>{t(`type.${product.type}`)}</Table.Td>
                          <Table.Td>{product.category?.name ?? t("common.noValue")}</Table.Td>
                          <Table.Td>
                            <Badge color={statusColor(product.status)}>{t(`status.${product.status}`)}</Badge>
                          </Table.Td>
                          <Table.Td>{product.unit || t("common.noValue")}</Table.Td>
                          <Table.Td>
                            {product.taxCategory
                              ? `${product.taxCategory.name} (${formatters.formatNumber(product.taxCategory.rate * 100, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}%)`
                              : t("common.noValue")}
                          </Table.Td>
                          <Table.Td>
                            {price === undefined ? t("common.noValue") : formatters.formatCurrency(price.amount, "NOK")}
                          </Table.Td>
                          <Table.Td onClick={(event) => event.stopPropagation()}>
                            <ActionIcon
                              variant="subtle"
                              color="gray"
                              aria-label={t("common.editNamed", { name: product.name })}
                              onClick={() => setModalState({ mode: "edit", product })}
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
              {data.data.length === 0 && (
                <Center py="xl">
                  <Text c="dimmed">{t("products.noProducts")}</Text>
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
