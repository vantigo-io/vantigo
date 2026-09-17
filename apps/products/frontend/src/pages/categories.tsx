import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Modal,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, KpiCard, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import "../i18n";
import {
  ApiValidationError,
  buildCategoryTree,
  type CategoryInput,
  type CategoryResponse,
  categoriesQueryOptions,
  createCategory,
  deleteCategory,
  maxDepth,
  subtreeProductCounts,
  updateCategory,
} from "../api/categories";
import { productsQueryOptions } from "../api/products";

type CategoryModalState = { mode: "create" } | { mode: "edit"; category: CategoryResponse };

interface CategoryFormValues {
  name: string;
  parentId: string | null;
}

export const CategoriesPage = () => {
  const { t } = useI18n("products");
  const queryClient = useQueryClient();
  const { data: categories, isPending, isError, error } = useQuery(categoriesQueryOptions());
  const { data: uncategorizedPage } = useQuery(productsQueryOptions({ pageSize: 1, uncategorized: true }));
  const [modalState, setModalState] = useState<CategoryModalState | null>(null);
  const form = useForm<CategoryFormValues>({
    initialValues: { name: "", parentId: null },
    validate: { name: (value) => (value.trim() ? null : t("categories.nameRequired")) },
  });

  const tree = buildCategoryTree(categories ?? []);
  const subtreeCounts = subtreeProductCounts(categories ?? []);
  const emptyCount = (categories ?? []).filter(({ id }) => (subtreeCounts.get(id) ?? 0) === 0).length;
  const rootCount = (categories ?? []).filter(({ parentId }) => parentId === null).length;
  const uncategorizedCount = uncategorizedPage?.pagination.totalCount;

  const isEdit = modalState?.mode === "edit";
  const parentOptions = tree
    // A category cannot become its own parent; deeper cycles are rejected server-side.
    .filter(({ category }) => !(isEdit && category.id === modalState.category.id))
    .map(({ category, depth }) => ({
      value: String(category.id),
      label: `${"\u00A0".repeat(depth * 3)}${category.name}`,
    }));

  const invalidate = () => void queryClient.invalidateQueries({ queryKey: ["categories"] });

  const mutation = useMutation({
    mutationFn: (input: CategoryInput) =>
      modalState?.mode === "edit" ? updateCategory(modalState.category.id, input) : createCategory(input),
    onSuccess: (category) => {
      notifications.show({
        color: "teal",
        title: isEdit ? t("categories.updated") : t("categories.created"),
        message: t("categories.savedMessage", {
          name: category.name,
          action: isEdit ? t("categories.updatedAction") : t("categories.createdAction"),
        }),
      });
      invalidate();
      setModalState(null);
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      notifications.show({
        color: "red",
        title: isEdit ? t("categories.failedToUpdate") : t("categories.failedToCreate"),
        message: error.message,
      });
    },
  });

  const removal = useMutation({
    mutationFn: (id: number) => deleteCategory(id),
    onSuccess: () => {
      notifications.show({ color: "teal", title: t("categories.deleted"), message: t("categories.deletedMessage") });
      invalidate();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("categories.failedToDelete"), message: error.message }),
  });

  const openCreate = () => {
    form.setValues({ name: "", parentId: null });
    form.clearErrors();
    setModalState({ mode: "create" });
  };

  const openEdit = (category: CategoryResponse) => {
    form.setValues({ name: category.name, parentId: category.parentId === null ? null : String(category.parentId) });
    form.clearErrors();
    setModalState({ mode: "edit", category });
  };

  const confirmDelete = (category: CategoryResponse) =>
    modals.openConfirmModal({
      title: t("categories.deleteTitle"),
      centered: true,
      children: <Text size="sm">{t("categories.deleteConfirm", { name: category.name })}</Text>,
      labels: { confirm: t("common.delete"), cancel: t("common.cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => removal.mutate(category.id),
    });

  const handleSubmit = form.onSubmit((values) =>
    mutation.mutate({
      name: values.name.trim(),
      parentId: values.parentId ? Number(values.parentId) : null,
    }),
  );

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("navigation.categories")}
        description={t("categories.description")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openCreate}>
            {t("categories.newCategory")}
          </Button>
        }
      />
      {categories && (
        <SimpleGrid cols={{ base: 2, sm: 3, lg: 5 }}>
          <KpiCard label={t("categories.totalCategories")} value={categories.length} />
          <KpiCard label={t("categories.rootCategories")} value={rootCount} />
          <KpiCard label={t("categories.maxDepth")} value={maxDepth(categories)} hint={t("categories.nestingLevels")} />
          <KpiCard
            label={t("categories.emptyCategories")}
            value={emptyCount}
            hint={t("categories.noProductsInSubtree")}
          />
          <KpiCard
            label={t("categories.uncategorisedProducts")}
            value={uncategorizedCount ?? t("common.noValue")}
            hint={t("categories.withoutCategory")}
          />
        </SimpleGrid>
      )}
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            {t("categories.hierarchyDescription")}
          </Text>
          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("categories.failedToLoad")}>
              {error.message}
            </Alert>
          )}
          {isPending && <ContentSkeleton rows={6} rowHeight={52} />}
          {categories && (
            <>
              <Table striped highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("common.name")}</Table.Th>
                    <Table.Th>{t("common.products")}</Table.Th>
                    <Table.Th w={96} aria-label={t("common.actions")} />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {tree.map(({ category, depth }) => {
                    const subtreeCount = subtreeCounts.get(category.id) ?? 0;
                    return (
                      <Table.Tr key={category.id}>
                        <Table.Td style={{ paddingLeft: 12 + depth * 24 }}>
                          <Group gap="xs" wrap="nowrap">
                            {category.name}
                            {subtreeCount === 0 && (
                              <Badge size="sm" color="gray" variant="light">
                                {t("categories.empty")}
                              </Badge>
                            )}
                          </Group>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm">
                            {category.productCount}
                            {subtreeCount !== category.productCount && (
                              <Text span size="sm" c="dimmed">
                                {" "}
                                · {t("categories.inSubtree", { count: subtreeCount })}
                              </Text>
                            )}
                          </Text>
                        </Table.Td>
                        <Table.Td>
                          <Group gap={4} wrap="nowrap" justify="flex-end">
                            <ActionIcon
                              variant="subtle"
                              color="gray"
                              aria-label={t("common.editNamed", { name: category.name })}
                              onClick={() => openEdit(category)}
                            >
                              <IconPencil size={16} />
                            </ActionIcon>
                            <ActionIcon
                              variant="subtle"
                              color="red"
                              aria-label={t("common.deleteNamed", { name: category.name })}
                              loading={removal.isPending && removal.variables === category.id}
                              onClick={() => confirmDelete(category)}
                            >
                              <IconTrash size={16} />
                            </ActionIcon>
                          </Group>
                        </Table.Td>
                      </Table.Tr>
                    );
                  })}
                </Table.Tbody>
              </Table>
              {categories.length === 0 && <EmptyState title={t("categories.noCategories")} />}
            </>
          )}
        </Stack>
      </Card>
      <Modal
        opened={modalState !== null}
        onClose={() => setModalState(null)}
        title={isEdit ? t("categories.editTitle") : t("categories.createTitle")}
        centered
      >
        <form onSubmit={handleSubmit}>
          <Stack>
            <TextInput
              label={t("common.name")}
              placeholder={t("categories.namePlaceholder")}
              withAsterisk
              data-autofocus
              {...form.getInputProps("name")}
            />
            <Select
              label={t("categories.parentCategory")}
              placeholder={t("categories.rootPlaceholder")}
              clearable
              searchable
              data={parentOptions}
              {...form.getInputProps("parentId")}
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setModalState(null)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={mutation.isPending}>
                {isEdit ? t("common.saveChanges") : t("categories.createCategory")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
};
