import {
  Button,
  Divider,
  Group,
  Modal,
  NumberInput,
  Select,
  Stack,
  Text,
  Textarea,
  TextInput,
  Tooltip,
} from "@mantine/core";
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

export type ProductModalState = { mode: "create" } | { mode: "edit"; product: ProductResponse };

interface ProductFormValues {
  name: string;
  sku: string;
  type: "Goods" | "Service";
  status: "Draft" | "Active" | "Discontinued";
  unit: string;
  standardCost: number | string;
  vatRate: number | string;
  description: string;
  categoryId: string | null;
  barcode: string;
  weightKg: number | string;
  lengthCm: number | string;
  widthCm: number | string;
  heightCm: number | string;
}

const emptyValues: ProductFormValues = {
  name: "",
  sku: "",
  type: "Goods",
  status: "Draft",
  unit: "",
  standardCost: "",
  vatRate: 0.25,
  description: "",
  categoryId: null,
  barcode: "",
  weightKg: "",
  lengthCm: "",
  widthCm: "",
  heightCm: "",
};

const valuesFromState = (state: ProductModalState): ProductFormValues =>
  state.mode === "create"
    ? emptyValues
    : {
        name: state.product.name,
        sku: state.product.sku,
        type: state.product.type,
        status: state.product.status,
        unit: state.product.unit,
        standardCost: state.product.standardCost ?? "",
        vatRate: state.product.vatRate,
        description: state.product.description ?? "",
        categoryId: state.product.category ? String(state.product.category.id) : null,
        barcode: state.product.barcode ?? "",
        weightKg: state.product.weightKg ?? "",
        lengthCm: state.product.lengthCm ?? "",
        widthCm: state.product.widthCm ?? "",
        heightCm: state.product.heightCm ?? "",
      };

interface ProductFormModalProps {
  state: ProductModalState | null;
  onClose: () => void;
}

export const ProductFormModal = ({ state, onClose }: ProductFormModalProps) => {
  const queryClient = useQueryClient();
  const isEdit = state?.mode === "edit";
  const skuLocked = isEdit && state.product.status !== "Draft";
  const { data: categories } = useQuery({ ...categoriesQueryOptions(), enabled: state !== null });
  const categoryOptions = buildCategoryTree(categories ?? []).map(({ category, depth }) => ({
    value: String(category.id),
    label: `${"\u00A0".repeat(depth * 3)}${category.name}`,
  }));
  const form = useForm<ProductFormValues>({
    initialValues: emptyValues,
    validate: {
      name: (value) => (value.trim() ? null : "Name is required"),
      sku: (value) => (value.trim() ? null : "SKU is required"),
      vatRate: (value) => (Number(value) >= 0 && Number(value) <= 1 ? null : "VAT rate must be between 0 and 1"),
      standardCost: (value) => (value === "" || Number(value) >= 0 ? null : "Standard cost must be zero or greater"),
      barcode: (value) =>
        value.trim() === "" || /^\d{8}$|^\d{12,14}$/.test(value.trim())
          ? null
          : "Barcode must be a GTIN of 8, 12, 13 or 14 digits",
    },
  });

  useEffect(() => {
    if (state) {
      form.setValues(valuesFromState(state));
      form.resetDirty();
      form.clearErrors();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);

  const mutation = useMutation({
    mutationFn: (input: ProductInput) => (isEdit ? updateProduct(state.product.id, input) : createProduct(input)),
    onSuccess: (product) => {
      notifications.show({
        color: "teal",
        title: isEdit ? "Product updated" : "Product created",
        message: `"${product.name}" was ${isEdit ? "updated" : "created"} successfully.`,
      });
      void queryClient.invalidateQueries({ queryKey: ["products"] });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      notifications.show({
        color: "red",
        title: isEdit ? "Failed to update product" : "Failed to create product",
        message: error.message,
      });
    },
  });

  const handleSubmit = form.onSubmit((values) => {
    mutation.mutate({
      name: values.name.trim(),
      sku: values.sku.trim(),
      type: values.type,
      status: values.status,
      unit: values.unit.trim() || undefined,
      standardCost: values.standardCost === "" ? undefined : Number(values.standardCost),
      vatRate: Number(values.vatRate),
      description: values.description.trim() || undefined,
      categoryId: values.categoryId ? Number(values.categoryId) : undefined,
      barcode: values.barcode.trim() || undefined,
      weightKg: values.weightKg === "" ? undefined : Number(values.weightKg),
      lengthCm: values.lengthCm === "" ? undefined : Number(values.lengthCm),
      widthCm: values.widthCm === "" ? undefined : Number(values.widthCm),
      heightCm: values.heightCm === "" ? undefined : Number(values.heightCm),
    });
  });

  return (
    <Modal opened={state !== null} onClose={onClose} title={isEdit ? "Edit product" : "Create new product"} centered>
      <form onSubmit={handleSubmit}>
        <Stack>
          <TextInput
            label="Name"
            placeholder="e.g. Consulting service"
            withAsterisk
            data-autofocus
            {...form.getInputProps("name")}
          />
          <Tooltip label="SKU cannot be changed after a product becomes active or discontinued" disabled={!skuLocked}>
            <TextInput
              label="SKU"
              placeholder="e.g. CONSULT-001"
              withAsterisk
              disabled={skuLocked}
              {...form.getInputProps("sku")}
            />
          </Tooltip>
          <Textarea
            label="Description"
            placeholder="Optional plain-text description"
            rows={3}
            {...form.getInputProps("description")}
          />
          <Select
            label="Category"
            placeholder="Uncategorised"
            clearable
            searchable
            data={categoryOptions}
            {...form.getInputProps("categoryId")}
          />
          <TextInput
            label="Barcode"
            description="GTIN-8, GTIN-12, GTIN-13 or GTIN-14"
            placeholder="e.g. 7350053850019"
            {...form.getInputProps("barcode")}
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
          <Group grow>
            <TextInput label="Unit" placeholder="e.g. pcs, hour" {...form.getInputProps("unit")} />
            <NumberInput label="Standard cost" min={0} decimalScale={2} {...form.getInputProps("standardCost")} />
          </Group>
          <NumberInput
            label="VAT rate"
            description="Enter a fraction, e.g. 0.25 for 25%"
            min={0}
            max={1}
            decimalScale={4}
            withAsterisk
            {...form.getInputProps("vatRate")}
          />
          <Divider label="Logistics" labelPosition="left" />
          <Text size="xs" c="dimmed">
            Optional weight and dimensions for shipping and warehouse planning.
          </Text>
          <Group grow>
            <NumberInput label="Weight (kg)" min={0} decimalScale={3} {...form.getInputProps("weightKg")} />
            <NumberInput label="Length (cm)" min={0} decimalScale={1} {...form.getInputProps("lengthCm")} />
            <NumberInput label="Width (cm)" min={0} decimalScale={1} {...form.getInputProps("widthCm")} />
            <NumberInput label="Height (cm)" min={0} decimalScale={1} {...form.getInputProps("heightCm")} />
          </Group>
          <Divider />
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
