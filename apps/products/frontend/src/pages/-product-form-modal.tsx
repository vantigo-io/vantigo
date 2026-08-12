import { Button, Group, Modal, Select, Stack, Text, Textarea, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { buildCategoryTree, categoriesQueryOptions } from "../api/categories";
import {
  ApiValidationError,
  createProduct,
  type ProductInput,
  type ProductResponse,
  updateProduct,
} from "../api/products";
import { taxCategoriesQueryOptions } from "../api/tax-categories";
export type ProductModalState = { mode: "create" } | { mode: "edit"; product: ProductResponse };
type Values = {
  name: string;
  type: "Goods" | "Service";
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
export const ProductFormModal = ({ state, onClose }: { state: ProductModalState | null; onClose: () => void }) => {
  const client = useQueryClient();
  const isEdit = state?.mode === "edit";
  const { data: categories } = useQuery({ ...categoriesQueryOptions(), enabled: state !== null });
  const { data: taxes } = useQuery({ ...taxCategoriesQueryOptions(), enabled: state !== null });
  const form = useForm<Values>({
    initialValues: empty,
    validate: {
      name: (v) => (v.trim() ? null : "Name is required"),
      taxCategoryId: (v) => (v ? null : "Tax category is required"),
    },
  });
  const { setValues, resetDirty, clearErrors } = form;
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
        title: isEdit ? "Product updated" : "Product created",
        message: `"${product.name}" was saved.`,
      });
      void client.invalidateQueries({ queryKey: ["products"] });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
      else notifications.show({ color: "red", title: "Could not save product", message: error.message });
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
  return (
    <Modal opened={state !== null} onClose={onClose} title={isEdit ? "Edit product" : "Create new product"} centered>
      <form onSubmit={submit}>
        <Stack>
          <TextInput label="Name" withAsterisk data-autofocus {...form.getInputProps("name")} />
          <Textarea label="Description" rows={3} {...form.getInputProps("description")} />
          <Select label="Category" clearable searchable data={categoryOptions} {...form.getInputProps("categoryId")} />
          <Select
            label="Tax category"
            withAsterisk
            data={(taxes ?? []).map((tax) => ({
              value: String(tax.id),
              label: `${tax.name} (${(tax.rate * 100).toFixed(2)}%)`,
            }))}
            {...form.getInputProps("taxCategoryId")}
          />
          <Group grow>
            <Select label="Type" data={["Goods", "Service"]} allowDeselect={false} {...form.getInputProps("type")} />
            <Select
              label="Status"
              data={["Draft", "Active", "Discontinued"]}
              allowDeselect={false}
              {...form.getInputProps("status")}
            />
          </Group>
          {!isEdit && (
            <Text size="sm" c="dimmed">
              A default variant will be created. Add more variants from the product page.
            </Text>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {isEdit ? "Save changes" : "Create product"}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
