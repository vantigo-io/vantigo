import {
  Alert,
  Anchor,
  Badge,
  Breadcrumbs,
  Button,
  Card,
  Center,
  Group,
  Modal,
  NumberInput,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { DateTimePicker } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconArchive, IconPackage, IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link, notFound } from "@tanstack/react-router";
import { useState } from "react";
import {
  addProductPrice,
  archiveProduct,
  deleteProductPrice,
  type PriceInput,
  type PriceRow,
  type ProductStatus,
  productPricesQueryOptions,
  productQueryOptions,
  updateProductPrice,
} from "../api/products";
import { NotFoundError } from "../api/request";
import { toIsoTimestamp, toPickerValue } from "../lib/dates";
import { ProductFormModal, type ProductModalState } from "./-product-form-modal";

const formatDateTime = (value: string | null) => (value ? new Date(value).toLocaleString() : "Open-ended");
const statusColor = (status: ProductStatus) => ({ Draft: "gray", Active: "teal", Discontinued: "red" })[status];

const formatPrice = (price: { amount: number; currency: string }) => `${price.amount.toFixed(2)} ${price.currency}`;

type PriceModalState = { mode: "add" } | { mode: "edit"; price: PriceRow };

interface PriceFormValues {
  currency: string;
  amount: number | string;
  validFrom: string | null;
  validTo: string | null;
}

const ProductDetailsPage = () => {
  const { productId } = Route.useParams();
  const queryClient = useQueryClient();
  const { data: product } = useSuspenseQuery(productQueryOptions(productId));
  const { data: prices } = useSuspenseQuery(productPricesQueryOptions(productId));
  const [modalState, setModalState] = useState<ProductModalState | null>(null);
  const [priceModal, setPriceModal] = useState<PriceModalState | null>(null);
  const priceForm = useForm<PriceFormValues>({
    initialValues: { currency: "NOK", amount: "", validFrom: null, validTo: null },
    validate: {
      currency: (value) => (/^[A-Za-z]{3}$/.test(value) ? null : "Enter a 3-letter currency code"),
      amount: (value) => (Number(value) >= 0 ? null : "Amount must be zero or greater"),
    },
  });

  const invalidatePrices = () => {
    void queryClient.invalidateQueries({ queryKey: ["products", product.id, "prices"] });
    void queryClient.invalidateQueries({ queryKey: ["products", product.id] });
  };

  const archive = useMutation({
    mutationFn: () => archiveProduct(product.id),
    onSuccess: () => {
      notifications.show({ color: "teal", title: "Product archived", message: `"${product.name}" was discontinued.` });
      void queryClient.invalidateQueries({ queryKey: ["products"] });
    },
  });
  const priceMutation = useMutation({
    mutationFn: (input: PriceInput) =>
      priceModal?.mode === "edit"
        ? updateProductPrice(product.id, priceModal.price.id, input)
        : addProductPrice(product.id, input),
    onSuccess: () => {
      invalidatePrices();
      setPriceModal(null);
      priceForm.reset();
    },
    onError: (error) => notifications.show({ color: "red", title: "Failed to save price", message: error.message }),
  });
  const removal = useMutation({
    mutationFn: (priceId: number) => deleteProductPrice(product.id, priceId),
    onSuccess: invalidatePrices,
  });

  const openAddPrice = () => {
    priceForm.setValues({ currency: "NOK", amount: "", validFrom: null, validTo: null });
    setPriceModal({ mode: "add" });
  };

  const openEditPrice = (price: PriceRow) => {
    priceForm.setValues({
      currency: price.currency,
      amount: price.amount,
      validFrom: toPickerValue(price.validFrom),
      validTo: toPickerValue(price.validTo),
    });
    setPriceModal({ mode: "edit", price });
  };

  const confirmArchive = () =>
    modals.openConfirmModal({
      title: "Archive product",
      centered: true,
      children: (
        <Text size="sm">
          Archive <b>{product.name}</b>? The product is marked as discontinued and can no longer be sold, but it is kept
          for historical references.
        </Text>
      ),
      labels: { confirm: "Archive", cancel: "Cancel" },
      confirmProps: { color: "red" },
      onConfirm: () => archive.mutate(),
    });

  const confirmDeletePrice = (price: PriceRow) =>
    modals.openConfirmModal({
      title: "Delete price",
      centered: true,
      children: (
        <Text size="sm">Delete the {formatPrice(price)} price row? This also removes it from the price history.</Text>
      ),
      labels: { confirm: "Delete", cancel: "Cancel" },
      confirmProps: { color: "red" },
      onConfirm: () => removal.mutate(price.id),
    });

  const submitPrice = priceForm.onSubmit((values) =>
    priceMutation.mutate({
      currency: values.currency.trim().toUpperCase(),
      amount: Number(values.amount),
      validFrom: toIsoTimestamp(values.validFrom),
      validTo: toIsoTimestamp(values.validTo),
    }),
  );

  return (
    <Stack gap="lg">
      <Breadcrumbs>
        <Anchor component={Link} to="/products" size="sm">
          Products
        </Anchor>
        <Text size="sm">{product.name}</Text>
      </Breadcrumbs>
      <Group justify="space-between">
        <Group gap="sm">
          <IconPackage size={28} />
          <Title order={2}>{product.name}</Title>
          <Badge color={statusColor(product.status)}>{product.status}</Badge>
        </Group>
        <Group>
          <Button
            variant="default"
            leftSection={<IconPencil size={16} />}
            onClick={() => setModalState({ mode: "edit", product })}
          >
            Edit product
          </Button>
          {product.status !== "Discontinued" && (
            <Button color="red" variant="light" leftSection={<IconArchive size={16} />} onClick={confirmArchive}>
              Archive
            </Button>
          )}
        </Group>
      </Group>
      <ProductFormModal state={modalState} onClose={() => setModalState(null)} />
      <Card withBorder>
        <Stack>
          <Title order={3}>Product details</Title>
          <Group>
            <Text>
              <b>SKU:</b> {product.sku}
            </Text>
            <Text>
              <b>Type:</b> {product.type}
            </Text>
            <Text>
              <b>Unit:</b> {product.unit || "—"}
            </Text>
            <Text>
              <b>Standard cost:</b> {product.standardCost ?? "—"}
            </Text>
            <Text>
              <b>VAT:</b> {product.vatRate * 100}%
            </Text>
            <Text>
              <b>Current price:</b>{" "}
              {product.effectivePrices.length > 0 ? product.effectivePrices.map(formatPrice).join(" · ") : "—"}
            </Text>
          </Group>
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Group justify="space-between">
            <Title order={3}>Prices</Title>
            <Button leftSection={<IconPlus size={16} />} onClick={openAddPrice}>
              Add price
            </Button>
          </Group>
          <Text size="sm" c="dimmed">
            Prices are excluding VAT. Bounded rows are campaign prices. Times are shown in your local timezone.
          </Text>
          <Table.ScrollContainer minWidth={640}>
            <Table striped>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Currency</Table.Th>
                  <Table.Th>Amount</Table.Th>
                  <Table.Th>Valid from</Table.Th>
                  <Table.Th>Valid to</Table.Th>
                  <Table.Th w={96} aria-label="Actions" />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {prices.map((price) => (
                  <Table.Tr key={price.id}>
                    <Table.Td>{price.currency}</Table.Td>
                    <Table.Td>
                      <Group gap="xs" wrap="nowrap">
                        {price.amount.toFixed(2)}
                        {product.effectivePrices.some((effective) => effective.id === price.id) && (
                          <Badge size="sm" color="teal" variant="light">
                            Current
                          </Badge>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td>{formatDateTime(price.validFrom)}</Table.Td>
                    <Table.Td>{formatDateTime(price.validTo)}</Table.Td>
                    <Table.Td>
                      <Group gap={4} wrap="nowrap" justify="flex-end">
                        <Button
                          variant="subtle"
                          size="compact-sm"
                          aria-label={`Edit ${price.currency} price`}
                          onClick={() => openEditPrice(price)}
                        >
                          <IconPencil size={16} />
                        </Button>
                        <Button
                          variant="subtle"
                          color="red"
                          size="compact-sm"
                          aria-label={`Delete ${price.currency} price`}
                          loading={removal.isPending && removal.variables === price.id}
                          onClick={() => confirmDeletePrice(price)}
                        >
                          <IconTrash size={16} />
                        </Button>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
          {prices.length === 0 && <Text c="dimmed">No prices configured.</Text>}
        </Stack>
      </Card>
      <Modal
        opened={priceModal !== null}
        onClose={() => setPriceModal(null)}
        title={priceModal?.mode === "edit" ? "Edit price" : "Add price"}
        centered
      >
        <form onSubmit={submitPrice}>
          <Stack>
            {priceModal?.mode === "edit" && (
              <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title="Editing rewrites this price row">
                Recorded orders are unaffected — they snapshot prices — but anything re-reading this row will see the
                new values. For a planned price change or campaign, prefer adding a new price row with a validity window
                instead.
              </Alert>
            )}
            <TextInput
              label="Currency"
              description="ISO 4217 code"
              withAsterisk
              {...priceForm.getInputProps("currency")}
            />
            <NumberInput
              label="Amount (ex VAT)"
              min={0}
              decimalScale={2}
              withAsterisk
              {...priceForm.getInputProps("amount")}
            />
            <DateTimePicker
              label="Valid from"
              description="Local time; leave empty for an open-ended base price"
              clearable
              valueFormat="YYYY-MM-DD HH:mm"
              {...priceForm.getInputProps("validFrom")}
            />
            <DateTimePicker
              label="Valid to"
              description="Local time, exclusive"
              clearable
              valueFormat="YYYY-MM-DD HH:mm"
              {...priceForm.getInputProps("validTo")}
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setPriceModal(null)}>
                Cancel
              </Button>
              <Button type="submit" loading={priceMutation.isPending}>
                {priceModal?.mode === "edit" ? "Save price" : "Add price"}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
};

const ProductNotFound = () => (
  <Center py="xl">
    <Stack align="center">
      <Title order={3}>Product not found</Title>
      <Text c="dimmed">The product you are looking for does not exist.</Text>
      <Button component={Link} to="/products" variant="light">
        Back to products
      </Button>
    </Stack>
  </Center>
);

export const Route = createFileRoute("/products/$productId")({
  params: {
    parse: ({ productId }) => {
      const id = Number(productId);
      if (!Number.isInteger(id) || id < 1) throw new Error(`Invalid product id: ${productId}`);
      return { productId: id };
    },
    stringify: ({ productId }) => ({ productId: String(productId) }),
  },
  loader: async ({ context: { queryClient }, params: { productId } }) => {
    try {
      await Promise.all([
        queryClient.ensureQueryData(productQueryOptions(productId)),
        queryClient.ensureQueryData(productPricesQueryOptions(productId)),
      ]);
    } catch (error) {
      if (error instanceof NotFoundError) throw notFound();
      throw error;
    }
  },
  notFoundComponent: ProductNotFound,
  component: ProductDetailsPage,
});
