import {
  Badge,
  Button,
  Group,
  Modal,
  Select,
  SimpleGrid,
  Stack,
  Text,
  Textarea,
  TextInput,
  ThemeIcon,
  UnstyledButton,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconPackage, IconTool } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import "../i18n";
import { buildCategoryTree, categoriesQueryOptions } from "../api/categories";
import {
  ApiValidationError,
  createProduct,
  PRODUCT_TYPES,
  type ProductInput,
  type ProductResponse,
  type ProductType,
  updateProduct,
} from "../api/products";
import { taxCategoriesQueryOptions } from "../api/tax-categories";
export type ProductModalState = { mode: "create" } | { mode: "edit"; product: ProductResponse };
type Values = {
  name: string;
  type: ProductType;
  status: "Draft" | "Active" | "Discontinued";
  description: string;
  categoryId: string | null;
  taxCategoryId: string | null;
};
const empty: Values = {
  name: "",
  type: "Goods",
  status: "Draft",
  description: "",
  categoryId: null,
  taxCategoryId: null,
};
const fromState = (state: ProductModalState): Values =>
  state.mode === "create"
    ? empty
    : {
        name: state.product.name,
        type: state.product.type,
        status: state.product.status,
        description: state.product.description ?? "",
        categoryId: state.product.category ? String(state.product.category.id) : null,
        taxCategoryId: state.product.taxCategory ? String(state.product.taxCategory.id) : null,
      };
const typeIcons = { Goods: IconPackage, Service: IconTool } as const;

/** Step one of creating a product: what kind of thing is it? */
const ProductTypePicker = ({ onPick }: { onPick: (type: ProductType) => void }) => {
  const { t } = useI18n("products");
  return (
    <Stack>
      <Text size="sm" c="dimmed">
        {t("productForm.pickTypeDescription")}
      </Text>
      <SimpleGrid cols={{ base: 1, xs: 2 }}>
        {PRODUCT_TYPES.map((type) => {
          const Icon = typeIcons[type];
          return (
            <UnstyledButton
              key={type}
              onClick={() => onPick(type)}
              data-autofocus={type === "Goods" || undefined}
              style={{
                border: "1px solid var(--mantine-color-default-border)",
                borderRadius: "var(--mantine-radius-md)",
                padding: "var(--mantine-spacing-md)",
              }}
            >
              <Stack gap="xs" align="flex-start">
                <ThemeIcon variant="light" size="lg">
                  <Icon size={20} />
                </ThemeIcon>
                <Text fw={600}>{t(`type.${type}`)}</Text>
                <Text size="sm" c="dimmed">
                  {t(`productForm.typeDescription.${type}`)}
                </Text>
              </Stack>
            </UnstyledButton>
          );
        })}
      </SimpleGrid>
    </Stack>
  );
};

export const ProductFormModal = ({ state, onClose }: { state: ProductModalState | null; onClose: () => void }) => {
  const { t, formatters } = useI18n("products");
  const client = useQueryClient();
  const isEdit = state?.mode === "edit";
  // Create asks for the kind first; edit goes straight to the form. The pick
  // is remembered against the modal state it was made for, so every reopen
  // (a fresh state object) starts over at the picker without an effect.
  const [pickedFor, setPickedFor] = useState<ProductModalState | null>(null);
  const setTypePicked = (picked: boolean) => setPickedFor(picked ? state : null);
  const pickingType = state?.mode === "create" && pickedFor !== state;
  const { data: categories } = useQuery({ ...categoriesQueryOptions(), enabled: state !== null });
  const { data: taxes } = useQuery({ ...taxCategoriesQueryOptions(), enabled: state !== null });
  const form = useForm<Values>({
    initialValues: empty,
    validate: {
      name: (v) => (v.trim() ? null : t("categories.nameRequired")),
      taxCategoryId: (v) => (v ? null : t("common.required", { field: t("common.taxCategory") })),
    },
  });
  const { setValues, setFieldValue, resetDirty, clearErrors } = form;
  // Sync form values when the modal opens.
  useEffect(() => {
    if (state) {
      setValues(fromState(state));
      resetDirty();
      clearErrors();
    }
  }, [clearErrors, resetDirty, setValues, state]);
  const mutation = useMutation({
    mutationFn: (input: ProductInput) => (isEdit ? updateProduct(state.product.id, input) : createProduct(input)),
    onSuccess: (product) => {
      notifications.show({
        color: "teal",
        title: isEdit ? t("productForm.updated") : t("productForm.created"),
        message: t("productForm.savedMessage", { name: product.name }),
      });
      void client.invalidateQueries({ queryKey: ["products"] });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
      else notifications.show({ color: "red", title: t("productForm.couldNotSave"), message: error.message });
    },
  });
  const submit = form.onSubmit((v) =>
    mutation.mutate({
      name: v.name.trim(),
      type: v.type,
      status: v.status,
      taxCategoryId: Number(v.taxCategoryId),
      description: v.description.trim() || undefined,
      categoryId: v.categoryId ? Number(v.categoryId) : undefined,
      variants: isEdit ? undefined : [{ sku: `${v.name.trim().replace(/\s+/g, "-").toUpperCase()}-001` }],
    }),
  );
  const categoryOptions = buildCategoryTree(categories ?? []).map(({ category, depth }) => ({
    value: String(category.id),
    label: `${"\u00A0".repeat(depth * 3)}${category.name}`,
  }));
  const title = isEdit
    ? t("productForm.editTitle")
    : pickingType
      ? t("productForm.createTitle")
      : t(`productForm.createTypedTitle.${form.values.type}`);
  return (
    <Modal opened={state !== null} onClose={onClose} title={title} centered>
      {pickingType ? (
        <ProductTypePicker
          onPick={(type) => {
            setFieldValue("type", type);
            setTypePicked(true);
          }}
        />
      ) : (
        <form onSubmit={submit}>
          <Stack>
            {!isEdit && (
              <Group gap="xs">
                <Badge variant="light" size="lg">
                  {t(`type.${form.values.type}`)}
                </Badge>
                <Button variant="subtle" size="compact-sm" onClick={() => setTypePicked(false)}>
                  {t("productForm.changeType")}
                </Button>
              </Group>
            )}
            <TextInput label={t("common.name")} withAsterisk data-autofocus {...form.getInputProps("name")} />
            <Textarea label={t("common.description")} rows={3} {...form.getInputProps("description")} />
            <Select
              label={t("common.category")}
              clearable
              searchable
              data={categoryOptions}
              {...form.getInputProps("categoryId")}
            />
            <Select
              label={t("common.taxCategory")}
              withAsterisk
              data={(taxes ?? []).map((tax) => ({
                value: String(tax.id),
                label: `${tax.name} (${formatters.formatNumber(tax.rate * 100, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}%)`,
              }))}
              {...form.getInputProps("taxCategoryId")}
            />
            <Group grow>
              {isEdit && (
                <Select
                  label={t("common.type")}
                  data={PRODUCT_TYPES.map((type) => ({ value: type, label: t(`type.${type}`) }))}
                  allowDeselect={false}
                  {...form.getInputProps("type")}
                />
              )}
              <Select
                label={t("common.status")}
                data={["Draft", "Active", "Discontinued"].map((status) => ({
                  value: status,
                  label: t(`status.${status}`),
                }))}
                allowDeselect={false}
                {...form.getInputProps("status")}
              />
            </Group>
            {!isEdit && (
              <Text size="sm" c="dimmed">
                {t("productForm.defaultVariant")}
              </Text>
            )}
            <Group justify="flex-end">
              <Button variant="default" onClick={onClose}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={mutation.isPending}>
                {isEdit ? t("productForm.saveProduct") : t("productForm.createProduct")}
              </Button>
            </Group>
          </Stack>
        </form>
      )}
    </Modal>
  );
};
