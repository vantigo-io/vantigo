import {
  Alert,
  Anchor,
  Badge,
  Breadcrumbs,
  Button,
  Card,
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
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import "../i18n";
import { categoriesQueryOptions, categoryPath } from "../api/categories";
import {
  addProductPrice,
  addProductVariant,
  archiveProduct,
  deleteProductPrice,
  deleteProductVariant,
  type PriceInput,
  type PriceRow,
  type ProductStatus,
  productQueryOptions,
  updateProductPrice,
  updateProductVariant,
  type VariantInput,
  type VariantResponse,
} from "../api/products";
import { toIsoTimestamp, toPickerValue } from "../lib/dates";
import { ProductFormModal, type ProductModalState } from "./-product-form-modal";

const statusColor = (status: ProductStatus) => ({ Draft: "gray", Active: "teal", Discontinued: "red" })[status];

type PriceModalState = { mode: "add" } | { mode: "edit"; price: PriceRow };
type VariantModalState = { mode: "add" } | { mode: "edit"; variant: VariantResponse };

interface PriceFormValues {
  currency: string;
  amount: number | string;
  validFrom: string | null;
  validTo: string | null;
}

export const ProductDetailsPage = () => {
  const { t, formatters } = useI18n("products");
  const { productId } = useParams({ strict: false }) as { productId: number };
  const queryClient = useQueryClient();
  const { data: product } = useSuspenseQuery(productQueryOptions(productId));
  const variant = product.variants?.[0];
  const prices = variant?.effectivePrices ?? product.effectivePrices;
  const { data: categories } = useQuery({ ...categoriesQueryOptions(), enabled: product.category !== null });
  const [modalState, setModalState] = useState<ProductModalState | null>(null);
  const [priceModal, setPriceModal] = useState<PriceModalState | null>(null);
  const [variantModal, setVariantModal] = useState<VariantModalState | null>(null);
  const variantForm = useForm<{ sku: string; unit: string; standardCost: number | string; optionValues: string }>({
    initialValues: { sku: "", unit: "pcs", standardCost: "", optionValues: "" },
  });
  const priceForm = useForm<PriceFormValues>({
    initialValues: { currency: "NOK", amount: "", validFrom: null, validTo: null },
    validate: {
      currency: (value) => (/^[A-Za-z]{3}$/.test(value) ? null : t("prices.validCurrency")),
      amount: (value) => (Number(value) >= 0 ? null : t("prices.validAmount")),
    },
  });

  const invalidatePrices = () => {
    void queryClient.invalidateQueries({ queryKey: ["products", product.id, "prices"] });
    void queryClient.invalidateQueries({ queryKey: ["products", product.id] });
  };

  const archive = useMutation({
    mutationFn: () => archiveProduct(product.id),
    onSuccess: () => {
      notifications.show({
        color: "teal",
        title: t("prices.archived"),
        message: t("prices.archivedMessage", { name: product.name }),
      });
      void queryClient.invalidateQueries({ queryKey: ["products"] });
    },
  });
  const priceMutation = useMutation({
    mutationFn: (input: PriceInput) =>
      priceModal?.mode === "edit"
        ? updateProductPrice(product.id, variant.id, priceModal.price.id, input)
        : addProductPrice(product.id, variant.id, input),
    onSuccess: () => {
      invalidatePrices();
      setPriceModal(null);
      priceForm.reset();
    },
    onError: (error) => notifications.show({ color: "red", title: t("prices.failedToSave"), message: error.message }),
  });
  const removal = useMutation({
    mutationFn: (priceId: number) => deleteProductPrice(product.id, variant.id, priceId),
    onSuccess: invalidatePrices,
  });
  const variantMutation = useMutation({
    mutationFn: (input: VariantInput) =>
      variantModal?.mode === "edit"
        ? updateProductVariant(product.id, variantModal.variant.id, input)
        : addProductVariant(product.id, input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["products", product.id] });
      setVariantModal(null);
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("prices.couldNotSaveVariant"), message: error.message }),
  });
  const variantRemoval = useMutation({
    mutationFn: (id: number) => deleteProductVariant(product.id, id),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["products", product.id] }),
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("prices.couldNotDeleteVariant"),
        message: (error as { status?: number }).status === 409 ? t("prices.lastVariant") : error.message,
      }),
  });

  const openAddPrice = () => {
    priceForm.setValues({ currency: "NOK", amount: "", validFrom: null, validTo: null });
    setPriceModal({ mode: "add" });
  };
  const openVariant = (item?: VariantResponse) => {
    variantForm.setValues(
      item
        ? {
            sku: item.sku,
            unit: item.unit,
            standardCost: item.standardCost ?? "",
            optionValues: Object.entries(item.optionValues)
              .map(([key, value]) => `${key}=${value}`)
              .join(", "),
          }
        : { sku: "", unit: "pcs", standardCost: "", optionValues: "" },
    );
    setVariantModal(item ? { mode: "edit", variant: item } : { mode: "add" });
  };
  const submitVariant = variantForm.onSubmit((values) =>
    variantMutation.mutate({
      sku: values.sku.trim(),
      unit: values.unit.trim(),
      standardCost: values.standardCost === "" ? undefined : Number(values.standardCost),
      optionValues: Object.fromEntries(
        values.optionValues
          .split(",")
          .map((entry) => entry.split("=").map((part) => part.trim()))
          .filter(([key, value]) => key && value),
      ),
    }),
  );

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
      title: t("prices.archiveTitle"),
      centered: true,
      children: <Text size="sm">{t("prices.archiveConfirm", { name: product.name })}</Text>,
      labels: { confirm: t("prices.archive"), cancel: t("common.cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => archive.mutate(),
    });

  const confirmDeletePrice = (price: PriceRow) =>
    modals.openConfirmModal({
      title: t("prices.deleteTitle"),
      centered: true,
      children: (
        <Text size="sm">
          {t("prices.deleteConfirm", { price: formatters.formatCurrency(price.amount, price.currency) })}
        </Text>
      ),
      labels: { confirm: t("common.delete"), cancel: t("common.cancel") },
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
          {t("navigation.products")}
        </Anchor>
        <Text size="sm">{product.name}</Text>
      </Breadcrumbs>
      <PageHeader
        eyebrow={t("navigation.products")}
        title={
          <>
            <IconPackage size={28} /> {product.name}
            <Badge color={statusColor(product.status)}>{t(`status.${product.status}`)}</Badge>
          </>
        }
        description={t("prices.detailsDescription")}
        actions={
          <Group>
            <Button
              variant="default"
              leftSection={<IconPencil size={16} />}
              onClick={() => setModalState({ mode: "edit", product })}
            >
              {t("productForm.editTitle")}
            </Button>
            {product.status !== "Discontinued" && (
              <Button color="red" variant="light" leftSection={<IconArchive size={16} />} onClick={confirmArchive}>
                {t("prices.archive")}
              </Button>
            )}
          </Group>
        }
      />
      <ProductFormModal state={modalState} onClose={() => setModalState(null)} />
      <Card withBorder>
        <Stack>
          <Title order={3}>{t("prices.productDetails")}</Title>
          <Group>
            <Text>
              <b>{t("common.sku")}:</b> {product.sku}
            </Text>
            <Text>
              <b>{t("common.type")}:</b> {t(`type.${product.type}`)}
            </Text>
            <Text>
              <b>{t("common.category")}:</b>{" "}
              {product.category
                ? (categories && categoryPath(categories, product.category.id)) || product.category.name
                : t("common.noValue")}
            </Text>
            <Text>
              <b>{t("prices.barcode")}:</b> {product.barcode ?? t("common.noValue")}
            </Text>
            <Text>
              <b>{t("common.unit")}:</b> {product.unit || t("common.noValue")}
            </Text>
            <Text>
              <b>{t("prices.standardCost")}:</b> {product.standardCost ?? t("common.noValue")}
            </Text>
            <Text>
              <b>{t("common.taxCategory")}:</b> {product.taxCategory.name} (
              {formatters.formatNumber(product.taxCategory.rate * 100)}%)
            </Text>
            <Text>
              <b>{t("prices.currentPrice")}:</b>{" "}
              {product.effectivePrices.length > 0
                ? product.effectivePrices
                    .map((price) => formatters.formatCurrency(price.amount, price.currency))
                    .join(" · ")
                : t("common.noValue")}
            </Text>
          </Group>
          {product.description && <Text style={{ whiteSpace: "pre-wrap" }}>{product.description}</Text>}
          {(product.weightKg !== null ||
            product.lengthCm !== null ||
            product.widthCm !== null ||
            product.heightCm !== null) && (
            <Stack gap={4}>
              <Title order={4}>{t("prices.logistics")}</Title>
              <Group>
                <Text>
                  <b>{t("prices.weight")}:</b>{" "}
                  {product.weightKg !== null ? `${product.weightKg} kg` : t("common.noValue")}
                </Text>
                <Text>
                  <b>{t("prices.dimensions")}:</b>{" "}
                  {[product.lengthCm, product.widthCm, product.heightCm]
                    .map((value) => (value !== null ? `${value} cm` : "—"))
                    .join(" × ")}
                </Text>
              </Group>
            </Stack>
          )}
        </Stack>
      </Card>
      {product.variants.length > 1 && (
        <Card withBorder>
          <Stack>
            <Group justify="space-between">
              <Title order={3}>{t("common.variants")}</Title>
              <Button leftSection={<IconPlus size={16} />} onClick={() => openVariant()}>
                {t("prices.addVariant")}
              </Button>
            </Group>
            <Table.ScrollContainer minWidth={720}>
              <Table striped>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("common.sku")}</Table.Th>
                    <Table.Th>{t("prices.optionValues")}</Table.Th>
                    <Table.Th>{t("common.unit")}</Table.Th>
                    <Table.Th>{t("common.cost")}</Table.Th>
                    <Table.Th>{t("common.prices")}</Table.Th>
                    <Table.Th />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {product.variants.map((item) => (
                    <Table.Tr key={item.id}>
                      <Table.Td>{item.sku}</Table.Td>
                      <Table.Td>
                        {Object.entries(item.optionValues)
                          .map(([key, value]) => `${key}: ${value}`)
                          .join(" · ") || t("common.noValue")}
                      </Table.Td>
                      <Table.Td>{item.unit || t("common.noValue")}</Table.Td>
                      <Table.Td>{item.standardCost ?? t("common.noValue")}</Table.Td>
                      <Table.Td>
                        {item.effectivePrices
                          .map((price) => formatters.formatCurrency(price.amount, price.currency))
                          .join(" · ") || t("common.noValue")}
                      </Table.Td>
                      <Table.Td>
                        <Group gap={4}>
                          <Button
                            size="compact-sm"
                            variant="subtle"
                            aria-label={t("common.editNamed", { name: item.sku })}
                            onClick={() => openVariant(item)}
                          >
                            <IconPencil size={16} />
                          </Button>
                          <Button
                            size="compact-sm"
                            variant="subtle"
                            color="red"
                            aria-label={t("common.deleteNamed", { name: item.sku })}
                            onClick={() => variantRemoval.mutate(item.id)}
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
          </Stack>
        </Card>
      )}
      <Card withBorder>
        <Stack>
          <Group justify="space-between">
            <Title order={3}>{t("common.prices")}</Title>
            <Button leftSection={<IconPlus size={16} />} onClick={openAddPrice}>
              {t("prices.addPrice")}
            </Button>
          </Group>
          <Text size="sm" c="dimmed">
            {t("prices.pricesDescription")}
          </Text>
          <Table.ScrollContainer minWidth={640}>
            <Table striped>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("common.currency")}</Table.Th>
                  <Table.Th>{t("common.amount")}</Table.Th>
                  <Table.Th>{t("prices.validFrom")}</Table.Th>
                  <Table.Th>{t("prices.validTo")}</Table.Th>
                  <Table.Th w={96} aria-label={t("common.actions")} />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {prices.map((price) => (
                  <Table.Tr key={price.id}>
                    <Table.Td>{price.currency}</Table.Td>
                    <Table.Td>
                      <Group gap="xs" wrap="nowrap">
                        {formatters.formatNumber(price.amount, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
                        {product.effectivePrices.some((effective) => effective.id === price.id) && (
                          <Badge size="sm" color="teal" variant="light">
                            {t("prices.currentBadge")}
                          </Badge>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      {price.validFrom
                        ? formatters.formatDate(price.validFrom, { dateStyle: "medium", timeStyle: "short" })
                        : t("prices.openEnded")}
                    </Table.Td>
                    <Table.Td>
                      {price.validTo
                        ? formatters.formatDate(price.validTo, { dateStyle: "medium", timeStyle: "short" })
                        : t("prices.openEnded")}
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4} wrap="nowrap" justify="flex-end">
                        <Button
                          variant="subtle"
                          size="compact-sm"
                          aria-label={t("common.editNamed", { name: `${price.currency} ${t("common.price")}` })}
                          onClick={() => openEditPrice(price)}
                        >
                          <IconPencil size={16} />
                        </Button>
                        <Button
                          variant="subtle"
                          color="red"
                          size="compact-sm"
                          aria-label={t("common.deleteNamed", { name: `${price.currency} ${t("common.price")}` })}
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
          {prices.length === 0 && <Text c="dimmed">{t("prices.noPrices")}</Text>}
        </Stack>
      </Card>
      <Modal
        opened={priceModal !== null}
        onClose={() => setPriceModal(null)}
        title={priceModal?.mode === "edit" ? t("prices.editPrice") : t("prices.addPrice")}
        centered
      >
        <form onSubmit={submitPrice}>
          <Stack>
            {priceModal?.mode === "edit" && (
              <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("prices.editingWarningTitle")}>
                {t("prices.editingWarning")}
              </Alert>
            )}
            <TextInput
              label={t("common.currency")}
              description={t("prices.isoCode")}
              withAsterisk
              {...priceForm.getInputProps("currency")}
            />
            <NumberInput
              label={t("prices.amountExVat")}
              min={0}
              decimalScale={2}
              withAsterisk
              {...priceForm.getInputProps("amount")}
            />
            <DateTimePicker
              label={t("prices.validFrom")}
              description={t("prices.validFromDescription")}
              clearable
              valueFormat="YYYY-MM-DD HH:mm"
              {...priceForm.getInputProps("validFrom")}
            />
            <DateTimePicker
              label={t("prices.validTo")}
              description={t("prices.validToDescription")}
              clearable
              valueFormat="YYYY-MM-DD HH:mm"
              {...priceForm.getInputProps("validTo")}
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setPriceModal(null)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={priceMutation.isPending}>
                {priceModal?.mode === "edit" ? t("prices.savePrice") : t("prices.addPriceSubmit")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
      <Modal
        opened={variantModal !== null}
        onClose={() => setVariantModal(null)}
        title={variantModal?.mode === "edit" ? t("prices.editVariant") : t("prices.addVariantTitle")}
        centered
      >
        <form onSubmit={submitVariant}>
          <Stack>
            <TextInput label={t("prices.skuRequired")} withAsterisk {...variantForm.getInputProps("sku")} />
            <TextInput
              label={t("prices.optionValues")}
              description={t("prices.optionValuesPlaceholder")}
              {...variantForm.getInputProps("optionValues")}
            />
            <TextInput label={t("common.unit")} {...variantForm.getInputProps("unit")} />
            <NumberInput
              label={t("prices.unitCost")}
              min={0}
              decimalScale={2}
              {...variantForm.getInputProps("standardCost")}
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setVariantModal(null)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={variantMutation.isPending}>
                {t("prices.saveVariant")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
};
