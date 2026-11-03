import {
  ActionIcon,
  Button,
  Group,
  Modal,
  NumberInput,
  SegmentedControl,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import "../i18n";
import {
  ApiConflictError,
  ApiValidationError,
  addPackaging,
  deletePackaging,
  PACKAGING_KINDS,
  type PackagingInput,
  type PackagingKind,
  type PackagingResponse,
  updatePackaging,
  type VariantResponse,
} from "../api/products";

type Mode = "quantity" | "contains";
type ModalState = { mode: "add" } | { mode: "edit"; packaging: PackagingResponse };
type Optional = number | string;

interface PackagingFormValues {
  name: string;
  kind: PackagingKind;
  mode: Mode;
  quantity: Optional;
  containsPackagingId: string | null;
  containsCount: Optional;
  barcode: string;
  weightKg: Optional;
  lengthCm: Optional;
  widthCm: Optional;
  heightCm: Optional;
}

const emptyValues: PackagingFormValues = {
  name: "",
  kind: "case",
  mode: "quantity",
  quantity: "",
  containsPackagingId: null,
  containsCount: "",
  barcode: "",
  weightKg: "",
  lengthCm: "",
  widthCm: "",
  heightCm: "",
};

const optionalNumber = (value: Optional) => (value === "" ? undefined : Number(value));

/** The conflicts the packagings API answers. Products' problems carry no code: they are told apart by title. */
const conflictKeys: Record<string, string> = {
  "Barcode taken": "packagings.barcodeTaken",
  "Packaging cycle": "packagings.cycle",
  "Packaging in use": "packagings.inUse",
};

export interface PackagingsEditorProps {
  productId: number;
  variant: VariantResponse;
  /** Names the variant in the heading, for a product with several. */
  showVariant?: boolean;
}

/**
 * A variant's packagings (inventory design I24): the boxes, cases and pallets
 * it is stored and counted in, each holding a quantity of the variant's unit
 * or a count of another of its packagings.
 */
export const PackagingsEditor = ({ productId, variant, showVariant = false }: PackagingsEditorProps) => {
  const { t, formatters } = useI18n("products");
  const queryClient = useQueryClient();
  const packagings = variant.packagings ?? [];
  const byId = new Map(packagings.map((packaging) => [packaging.id, packaging]));
  const [modal, setModal] = useState<ModalState | null>(null);
  const form = useForm<PackagingFormValues>({
    initialValues: emptyValues,
    validate: {
      name: (value) => (value.trim() ? null : t("common.required", { field: t("common.name") })),
      quantity: (value, values) =>
        values.mode === "quantity" && !(Number(value) > 0) ? t("packagings.validQuantity") : null,
      containsPackagingId: (value, values) =>
        values.mode === "contains" && !value ? t("common.required", { field: t("packagings.packaging") }) : null,
      containsCount: (value, values) =>
        values.mode === "contains" && !(Number(value) >= 2) ? t("packagings.validCount") : null,
    },
  });

  const amount = (quantity: number) =>
    `${formatters.formatNumber(quantity, { maximumFractionDigits: 3 })} ${variant.unit}`;
  const conflictMessage = (error: ApiConflictError) => {
    const key = error.title ? conflictKeys[error.title] : undefined;
    return key ? t(key) : (error.detail ?? error.message);
  };
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ["products", productId] });

  const save = useMutation({
    mutationFn: (input: PackagingInput) =>
      modal?.mode === "edit"
        ? updatePackaging(productId, variant.id, modal.packaging.id, input)
        : addPackaging(productId, variant.id, input),
    onSuccess: () => {
      refresh();
      setModal(null);
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
      notifications.show({
        color: "red",
        title: t("packagings.couldNotSave"),
        message: error instanceof ApiConflictError ? conflictMessage(error) : error.message,
      });
    },
  });
  const removal = useMutation({
    mutationFn: (id: number) => deletePackaging(productId, variant.id, id),
    onSuccess: refresh,
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("packagings.couldNotDelete"),
        message: error instanceof ApiConflictError ? conflictMessage(error) : error.message,
      }),
  });

  const open = (packaging?: PackagingResponse) => {
    form.clearErrors();
    form.setValues(
      packaging
        ? {
            name: packaging.name,
            kind: packaging.kind,
            mode: packaging.containsPackagingId != null ? "contains" : "quantity",
            quantity: packaging.containsPackagingId != null ? "" : packaging.quantity,
            containsPackagingId: packaging.containsPackagingId != null ? String(packaging.containsPackagingId) : null,
            containsCount: packaging.containsCount ?? "",
            barcode: packaging.barcode ?? "",
            weightKg: packaging.weightKg ?? "",
            lengthCm: packaging.lengthCm ?? "",
            widthCm: packaging.widthCm ?? "",
            heightCm: packaging.heightCm ?? "",
          }
        : emptyValues,
    );
    setModal(packaging ? { mode: "edit", packaging } : { mode: "add" });
  };
  const submit = form.onSubmit((values) =>
    save.mutate({
      name: values.name.trim(),
      kind: values.kind,
      ...(values.mode === "quantity"
        ? { quantity: Number(values.quantity) }
        : { containsPackagingId: Number(values.containsPackagingId), containsCount: Number(values.containsCount) }),
      barcode: values.barcode.trim() || undefined,
      weightKg: optionalNumber(values.weightKg),
      lengthCm: optionalNumber(values.lengthCm),
      widthCm: optionalNumber(values.widthCm),
      heightCm: optionalNumber(values.heightCm),
      sortOrder: modal?.mode === "edit" ? modal.packaging.sortOrder : undefined,
    }),
  );
  const confirmDelete = (packaging: PackagingResponse) =>
    modals.openConfirmModal({
      title: t("packagings.deleteTitle"),
      centered: true,
      children: <Text size="sm">{t("packagings.deleteConfirm", { name: packaging.name })}</Text>,
      labels: { confirm: t("common.delete"), cancel: t("common.cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => removal.mutate(packaging.id),
    });

  const contains = (packaging: PackagingResponse) => {
    const inner = packaging.containsPackagingId != null ? byId.get(packaging.containsPackagingId) : undefined;
    if (!inner) return amount(packaging.quantity);
    return t("packagings.containsOf", {
      count: packaging.containsCount ?? 0,
      name: inner.name,
      quantity: amount(packaging.quantity),
    });
  };
  const dimensions = (packaging: PackagingResponse) => {
    const sides = [packaging.lengthCm, packaging.widthCm, packaging.heightCm];
    const parts = [];
    if (sides.some((value) => value != null))
      parts.push(`${sides.map((value) => (value != null ? formatters.formatNumber(value) : "?")).join(" × ")} cm`);
    if (packaging.weightKg != null) parts.push(`${formatters.formatNumber(packaging.weightKg)} kg`);
    return parts.join(" · ") || t("common.noValue");
  };
  const otherPackagings = packagings.filter(
    (packaging) => modal?.mode !== "edit" || packaging.id !== modal.packaging.id,
  );
  const mode = form.values.mode;

  return (
    <Stack gap="xs">
      <Group justify="space-between">
        <Title order={4}>
          {showVariant ? t("packagings.headingFor", { sku: variant.sku }) : t("packagings.heading")}
        </Title>
        <Button
          size="compact-sm"
          variant="light"
          leftSection={<IconPlus size={14} />}
          disabled={packagings.length >= 10}
          onClick={() => open()}
        >
          {t("packagings.add")}
        </Button>
      </Group>
      {packagings.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("packagings.none")}
        </Text>
      ) : (
        <Table.ScrollContainer minWidth={640}>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("common.name")}</Table.Th>
                <Table.Th>{t("common.kind")}</Table.Th>
                <Table.Th>{t("packagings.contains")}</Table.Th>
                <Table.Th>{t("packagings.barcode")}</Table.Th>
                <Table.Th>{t("prices.dimensions")}</Table.Th>
                <Table.Th w={72} aria-label={t("common.actions")} />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {packagings.map((packaging) => (
                <Table.Tr key={packaging.id}>
                  <Table.Td>{packaging.name}</Table.Td>
                  <Table.Td>{t(`packagings.kind.${packaging.kind}`)}</Table.Td>
                  <Table.Td>{contains(packaging)}</Table.Td>
                  <Table.Td>{packaging.barcode ?? t("common.noValue")}</Table.Td>
                  <Table.Td>{dimensions(packaging)}</Table.Td>
                  <Table.Td>
                    <Group gap={4} wrap="nowrap">
                      <ActionIcon
                        variant="subtle"
                        color="gray"
                        aria-label={t("common.editNamed", { name: packaging.name })}
                        onClick={() => open(packaging)}
                      >
                        <IconPencil size={16} />
                      </ActionIcon>
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        aria-label={t("common.deleteNamed", { name: packaging.name })}
                        onClick={() => confirmDelete(packaging)}
                      >
                        <IconTrash size={16} />
                      </ActionIcon>
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
      <Modal
        opened={modal !== null}
        onClose={() => setModal(null)}
        title={modal?.mode === "edit" ? t("packagings.editTitle") : t("packagings.addTitle")}
      >
        <form onSubmit={submit}>
          <Stack>
            <TextInput label={t("common.name")} maxLength={40} withAsterisk {...form.getInputProps("name")} />
            <Select
              label={t("common.kind")}
              allowDeselect={false}
              data={PACKAGING_KINDS.map((kind) => ({ value: kind, label: t(`packagings.kind.${kind}`) }))}
              {...form.getInputProps("kind")}
            />
            <SegmentedControl
              aria-label={t("packagings.contains")}
              data={[
                { value: "quantity", label: t("packagings.modeQuantity", { unit: variant.unit }) },
                { value: "contains", label: t("packagings.modeContains") },
              ]}
              disabled={otherPackagings.length === 0}
              {...form.getInputProps("mode")}
            />
            {mode === "quantity" ? (
              <NumberInput
                label={t("packagings.quantity")}
                rightSection={<Text size="sm">{variant.unit}</Text>}
                rightSectionWidth={56}
                decimalScale={3}
                min={0}
                withAsterisk
                {...form.getInputProps("quantity")}
              />
            ) : (
              <Group grow align="start">
                <Select
                  label={t("packagings.packaging")}
                  data={otherPackagings.map((packaging) => ({
                    value: String(packaging.id),
                    label: `${packaging.name} (${amount(packaging.quantity)})`,
                  }))}
                  withAsterisk
                  {...form.getInputProps("containsPackagingId")}
                />
                <NumberInput
                  label={t("packagings.count")}
                  min={2}
                  allowDecimal={false}
                  withAsterisk
                  {...form.getInputProps("containsCount")}
                />
              </Group>
            )}
            <TextInput label={t("packagings.barcode")} maxLength={14} {...form.getInputProps("barcode")} />
            <Group grow>
              <NumberInput label={t("packagings.lengthCm")} min={0} {...form.getInputProps("lengthCm")} />
              <NumberInput label={t("packagings.widthCm")} min={0} {...form.getInputProps("widthCm")} />
              <NumberInput label={t("packagings.heightCm")} min={0} {...form.getInputProps("heightCm")} />
            </Group>
            <NumberInput
              label={t("packagings.weightKg")}
              min={0}
              decimalScale={3}
              {...form.getInputProps("weightKg")}
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setModal(null)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={save.isPending}>
                {t("common.save")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
};
