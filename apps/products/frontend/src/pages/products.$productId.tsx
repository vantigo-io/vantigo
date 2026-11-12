import {
  Alert,
  Anchor,
  Badge,
  Button,
  Group,
  Modal,
  NumberInput,
  Select,
  SimpleGrid,
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
import { IconAlertTriangle, IconArchive, IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { EmptyState, PageHeader, PageSection, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import "../i18n";
import { categoriesQueryOptions, categoryPath } from "../api/categories";
import {
  ApiConflictError,
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
  type VariantTracking,
} from "../api/products";
import { toIsoTimestamp, toPickerValue } from "../lib/dates";
import { PackagingsEditor } from "./-packagings-editor";
import { ProductFormModal, type ProductModalState } from "./-product-form-modal";
import { ProductImages } from "./-product-images";

const statusColor = (status: ProductStatus) => ({ Draft: "gray", Active: "teal", Discontinued: "red" })[status];

type PriceModalState = { mode: "add" } | { mode: "edit"; price: PriceRow };
type VariantModalState = { mode: "add" } | { mode: "edit"; variant: VariantResponse };

/** The variant form, as typed: the offsets are whole days, "" for none. */
interface VariantFormValues {
  sku: string;
  unit: string;
  standardCost: number | string;
  optionValues: string;
  tracking: VariantTracking;
  expiryDays: number | string;
  bestBeforeDays: number | string;
}

const emptyVariantForm: VariantFormValues = {
  sku: "",
  unit: "pcs",
  standardCost: "",
  optionValues: "",
  tracking: "none",
  expiryDays: "",
  bestBeforeDays: "",
};

interface PriceFormValues {
  currency: string;
  amount: number | string;
  validFrom: string | null;
  validTo: string | null;
}

/**
 * What the host composes into the product page from other modules. Both are
 * optional, and the page is whole without them.
 */
export interface ProductDetailsPageProps {
  /** Drawn after the variants card (after the details card when the product has one variant), before the prices. */
  extraPanels?: ReactNode;
  /**
   * The href of a variant's stock, linked from the refusal to delete a variant
   * that still has stock. Without it the refusal has no link.
   */
  stockHref?: (variantId: number) => string;
}

export const ProductDetailsPage = ({ extraPanels, stockHref }: ProductDetailsPageProps = {}) => {
  const { t, formatters } = useI18n("products");
  const { productId } = useParams({ strict: false }) as { productId: number };
  const queryClient = useQueryClient();
  const { data: product } = useSuspenseQuery(productQueryOptions(productId));
  const variant = product.variants?.[0];
  const prices = variant?.effectivePrices ?? product.effectivePrices;
  // The server omits empty logistics fields rather than sending null.
  const hasLogistics = [product.weightKg, product.lengthCm, product.widthCm, product.heightCm].some(
    (value) => value != null,
  );
  const { data: categories } = useQuery({ ...categoriesQueryOptions(), enabled: product.category != null });
  const [modalState, setModalState] = useState<ProductModalState | null>(null);
  const [priceModal, setPriceModal] = useState<PriceModalState | null>(null);
  const [variantModal, setVariantModal] = useState<VariantModalState | null>(null);
  const variantForm = useForm<VariantFormValues>({
    initialValues: emptyVariantForm,
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
    onError: (error) => {
      // A change of the tracking mode while the variant holds stock, reservations or stock in transit (manufacturing phase 2 M51).
      if (error instanceof ApiConflictError && error.title === "Tracking held") {
        notifications.show({
          color: "red",
          autoClose: false,
          title: t("prices.trackingHeld"),
          message: (
            <Stack gap={4}>
              <Text size="sm">{t("prices.trackingHeldMessage")}</Text>
              {variantModal?.mode === "edit" && stockHref && (
                <Anchor size="sm" href={stockHref(variantModal.variant.id)}>
                  {t("prices.seeWhereItLies")}
                </Anchor>
              )}
            </Stack>
          ),
        });
        return;
      }
      notifications.show({ color: "red", title: t("prices.couldNotSaveVariant"), message: error.message });
    },
  });
  const variantRemoval = useMutation({
    mutationFn: (id: number) => deleteProductVariant(product.id, id),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["products", product.id] }),
    onError: (error, id) => {
      // Products' problems carry no code: the two 409s are told apart by title.
      if (error instanceof ApiConflictError && error.title === "Variant has stock") {
        // A variant held on its way between sites (inventory phase 2 design I47) is said by the detail Inventory's hold gives.
        const inTransit = /in transit between sites/.test(error.detail ?? error.message);
        notifications.show({
          color: "red",
          autoClose: stockHref ? false : undefined,
          title: t(inTransit ? "prices.variantInTransit" : "prices.variantHasStock"),
          message: (
            <Stack gap={4}>
              <Text size="sm">{t(inTransit ? "prices.variantInTransitMessage" : "prices.variantHasStockMessage")}</Text>
              {stockHref && (
                <Anchor size="sm" href={stockHref(id)}>
                  {t("prices.seeWhereItLies")}
                </Anchor>
              )}
            </Stack>
          ),
        });
        return;
      }
      notifications.show({
        color: "red",
        title: t("prices.couldNotDeleteVariant"),
        message: (error as { status?: number }).status === 409 ? t("prices.lastVariant") : error.message,
      });
    },
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
            tracking: item.tracking ?? "none",
            expiryDays: item.expiryDays ?? "",
            bestBeforeDays: item.bestBeforeDays ?? "",
          }
        : emptyVariantForm,
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
      // Only a goods product's variant is tracked (manufacturing phase 2 M49); a service's keeps none.
      tracking: product.type === "Goods" ? values.tracking : undefined,
      expiryDays: values.expiryDays === "" ? undefined : Number(values.expiryDays),
      bestBeforeDays: values.bestBeforeDays === "" ? undefined : Number(values.bestBeforeDays),
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

  const confirmDeleteVariant = (item: VariantResponse) =>
    modals.openConfirmModal({
      title: t("prices.deleteVariantTitle"),
      centered: true,
      children: <Text size="sm">{t("prices.deleteVariantConfirm", { sku: item.sku })}</Text>,
      labels: { confirm: t("common.delete"), cancel: t("common.cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => variantRemoval.mutate(item.id),
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
      <PageHeader
        breadcrumbs={[{ label: t("navigation.products"), to: "/products" }, { label: product.name }]}
        title={
          <>
            {product.name} <Badge color={statusColor(product.status)}>{t(`status.${product.status}`)}</Badge>
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
      <PageSection>
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
          {product.type === "Goods" && (
            <Stack gap={4}>
              <Title order={4}>{t("prices.logistics")}</Title>
              {hasLogistics ? (
                <Group>
                  <Text>
                    <b>{t("prices.weight")}:</b>{" "}
                    {product.weightKg != null ? `${product.weightKg} kg` : t("common.noValue")}
                  </Text>
                  <Text>
                    <b>{t("prices.dimensions")}:</b>{" "}
                    {[product.lengthCm, product.widthCm, product.heightCm]
                      .map((value) => (value != null ? `${value} cm` : t("common.noValue")))
                      .join(" × ")}
                  </Text>
                </Group>
              ) : (
                <Text size="sm" c="dimmed">
                  {t("prices.noLogistics")}
                </Text>
              )}
            </Stack>
          )}
          {product.type === "Goods" && product.variants.length === 1 && variant && (
            <PackagingsEditor productId={product.id} variant={variant} />
          )}
        </Stack>
      </PageSection>
      <ProductImages productId={product.id} />
      {product.variants.length > 1 && (
        <PageSection>
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
                            onClick={() => confirmDeleteVariant(item)}
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
            {product.type === "Goods" &&
              product.variants.map((item) => (
                <PackagingsEditor key={item.id} productId={product.id} variant={item} showVariant />
              ))}
          </Stack>
        </PageSection>
      )}
      {extraPanels}
      <PageSection>
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
          {prices.length === 0 && <EmptyState size="sm" title={t("prices.noPrices")} />}
        </Stack>
      </PageSection>
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
            {product.type === "Goods" && (
              <Select
                label={t("prices.tracking")}
                description={t("prices.trackingHint")}
                allowDeselect={false}
                data={(["none", "lot", "serial"] as const).map((mode) => ({
                  value: mode,
                  label: t(`prices.tracking_${mode}`),
                }))}
                {...variantForm.getInputProps("tracking")}
              />
            )}
            <SimpleGrid cols={2}>
              <NumberInput
                label={t("prices.expiryDays")}
                description={t("prices.expiryDaysHint")}
                min={1}
                max={3650}
                allowDecimal={false}
                {...variantForm.getInputProps("expiryDays")}
              />
              <NumberInput
                label={t("prices.bestBeforeDays")}
                description={t("prices.bestBeforeDaysHint")}
                min={1}
                max={3650}
                allowDecimal={false}
                {...variantForm.getInputProps("bestBeforeDays")}
              />
            </SimpleGrid>
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
