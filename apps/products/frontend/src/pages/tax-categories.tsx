import {
  ActionIcon,
  Alert,
  Button,
  Card,
  Group,
  Modal,
  NumberInput,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import "../i18n";
import type { ApiError } from "../api/request";
import {
  createTaxCategory,
  deleteTaxCategory,
  type TaxCategoryInput,
  type TaxCategoryKind,
  type TaxCategoryResponse,
  taxCategoriesQueryOptions,
  updateTaxCategory,
} from "../api/tax-categories";

type FormValues = { name: string; kind: TaxCategoryKind; rate: number | string };
const kinds: TaxCategoryKind[] = ["Standard", "Reduced", "Zero", "Exempt"];

export const TaxCategoriesPage = () => {
  const { t, formatters } = useI18n("products");
  const client = useQueryClient();
  const { data = [] } = useQuery(taxCategoriesQueryOptions());
  const [editing, setEditing] = useState<TaxCategoryResponse | null | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);
  const form = useForm<FormValues>({
    initialValues: { name: "", kind: "Standard", rate: "" },
    validate: {
      name: (v) => (v.trim() ? null : t("categories.nameRequired")),
      rate: (v) => (Number(v) >= 0 && Number(v) <= 100 ? null : t("taxCategories.rateValidation")),
    },
  });
  const save = useMutation({
    mutationFn: (input: TaxCategoryInput) =>
      editing ? updateTaxCategory(editing.id, input) : createTaxCategory(input),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["tax-categories"] });
      setEditing(undefined);
      notifications.show({ color: "teal", title: t("taxCategories.saved"), message: t("taxCategories.savedMessage") });
    },
    onError: (e) => setError(e.message),
  });
  const remove = useMutation({
    mutationFn: deleteTaxCategory,
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["tax-categories"] });
      setError(null);
    },
    onError: (e: ApiError) => setError(e.status === 409 ? t("taxCategories.inUse") : e.message),
  });
  const open = (category?: TaxCategoryResponse) => {
    setError(null);
    form.setValues(
      category
        ? { name: category.name, kind: category.kind, rate: category.rate * 100 }
        : { name: "", kind: "Standard", rate: 0 },
    );
    setEditing(category ?? null);
  };
  const submit = form.onSubmit((v) => save.mutate({ name: v.name.trim(), kind: v.kind, rate: Number(v.rate) / 100 }));
  return (
    <Stack gap="lg">
      <PageHeader
        eyebrow={t("navigation.products")}
        title={t("navigation.taxCategories")}
        description={t("taxCategories.description")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={() => open()}>
            {t("taxCategories.newCategory")}
          </Button>
        }
      />
      {error && (
        <Alert color="red" title={t("taxCategories.couldNotComplete")} withCloseButton onClose={() => setError(null)}>
          {error}
        </Alert>
      )}
      <Card withBorder>
        <Table striped>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("common.name")}</Table.Th>
              <Table.Th>{t("common.kind")}</Table.Th>
              <Table.Th>{t("common.rate")}</Table.Th>
              <Table.Th w={96} aria-label={t("common.actions")} />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {data.map((category) => (
              <Table.Tr key={category.id}>
                <Table.Td>{category.name}</Table.Td>
                <Table.Td>{t(`taxKind.${category.kind}`)}</Table.Td>
                <Table.Td>
                  {t("common.percent", {
                    value: formatters.formatNumber(category.rate * 100, {
                      minimumFractionDigits: 2,
                      maximumFractionDigits: 2,
                    }),
                  })}
                </Table.Td>
                <Table.Td>
                  <Group gap={4} justify="flex-end">
                    <ActionIcon
                      aria-label={t("common.editNamed", { name: category.name })}
                      variant="subtle"
                      onClick={() => open(category)}
                    >
                      <IconPencil size={16} />
                    </ActionIcon>
                    <ActionIcon
                      aria-label={t("common.deleteNamed", { name: category.name })}
                      color="red"
                      variant="subtle"
                      loading={remove.isPending && remove.variables === category.id}
                      onClick={() => remove.mutate(category.id)}
                    >
                      <IconTrash size={16} />
                    </ActionIcon>
                  </Group>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
        {data.length === 0 && (
          <Text c="dimmed" ta="center" py="lg">
            {t("taxCategories.noCategories")}
          </Text>
        )}
      </Card>
      <Modal
        opened={editing !== undefined}
        onClose={() => setEditing(undefined)}
        title={editing ? t("taxCategories.editTitle") : t("taxCategories.newTitle")}
        centered
      >
        <form onSubmit={submit}>
          <Stack>
            <TextInput label={t("common.name")} withAsterisk {...form.getInputProps("name")} />
            <Select
              label={t("common.kind")}
              data={kinds.map((kind) => ({ value: kind, label: t(`taxKind.${kind}`) }))}
              allowDeselect={false}
              {...form.getInputProps("kind")}
            />
            <NumberInput
              label={t("taxCategories.rateLabel")}
              description={t("taxCategories.rateDescription")}
              min={0}
              max={100}
              decimalScale={2}
              withAsterisk
              {...form.getInputProps("rate")}
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setEditing(undefined)}>
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
