import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Center,
  Group,
  Loader,
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
import { IconAlertCircle, IconCategory, IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
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

const StatCard = ({ label, value, hint }: { label: string; value: string | number; hint?: string }) => (
  <Card withBorder padding="md" radius="md">
    <Text size="sm" c="dimmed">
      {label}
    </Text>
    <Text size="xl" fw={700}>
      {value}
    </Text>
    {hint && (
      <Text size="xs" c="dimmed">
        {hint}
      </Text>
    )}
  </Card>
);

export const CategoriesPage = () => {
  const queryClient = useQueryClient();
  const { data: categories, isPending, isError, error } = useQuery(categoriesQueryOptions());
  const { data: uncategorizedPage } = useQuery(productsQueryOptions({ pageSize: 1, uncategorized: true }));
  const [modalState, setModalState] = useState<CategoryModalState | null>(null);
  const form = useForm<CategoryFormValues>({
    initialValues: { name: "", parentId: null },
    validate: { name: (value) => (value.trim() ? null : "Name is required") },
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
        title: isEdit ? "Category updated" : "Category created",
        message: `"${category.name}" was ${isEdit ? "updated" : "created"} successfully.`,
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
        title: isEdit ? "Failed to update category" : "Failed to create category",
        message: error.message,
      });
    },
  });

  const removal = useMutation({
    mutationFn: (id: number) => deleteCategory(id),
    onSuccess: () => {
      notifications.show({ color: "teal", title: "Category deleted", message: "The category was deleted." });
      invalidate();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: "Failed to delete category", message: error.message }),
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
      title: "Delete category",
      centered: true,
      children: (
        <Text size="sm">
          Delete <b>{category.name}</b>? Categories with subcategories or assigned products cannot be deleted.
        </Text>
      ),
      labels: { confirm: "Delete", cancel: "Cancel" },
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
        eyebrow="Products"
        title={
          <>
            <IconCategory size={28} /> Categories
          </>
        }
        description="Organize products into groups for browsing and reporting."
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openCreate}>
            New category
          </Button>
        }
      />
      {categories && (
        <SimpleGrid cols={{ base: 2, sm: 3, lg: 5 }}>
          <StatCard label="Total categories" value={categories.length} />
          <StatCard label="Root categories" value={rootCount} />
          <StatCard label="Max depth" value={maxDepth(categories)} hint="Nesting levels" />
          <StatCard label="Empty categories" value={emptyCount} hint="No products in subtree" />
          <StatCard label="Uncategorised products" value={uncategorizedCount ?? "…"} hint="Without any category" />
        </SimpleGrid>
      )}
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            Categories form a hierarchy; each product belongs to at most one category. Filtering by a category also
            matches products in its subcategories.
          </Text>
          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title="Failed to load categories">
              {error.message}
            </Alert>
          )}
          {isPending && (
            <Center py="xl">
              <Loader />
            </Center>
          )}
          {categories && (
            <>
              <Table striped highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Name</Table.Th>
                    <Table.Th>Products</Table.Th>
                    <Table.Th w={96} aria-label="Actions" />
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
                                Empty
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
                                · {subtreeCount} in subtree
                              </Text>
                            )}
                          </Text>
                        </Table.Td>
                        <Table.Td>
                          <Group gap={4} wrap="nowrap" justify="flex-end">
                            <ActionIcon
                              variant="subtle"
                              color="gray"
                              aria-label={`Edit ${category.name}`}
                              onClick={() => openEdit(category)}
                            >
                              <IconPencil size={16} />
                            </ActionIcon>
                            <ActionIcon
                              variant="subtle"
                              color="red"
                              aria-label={`Delete ${category.name}`}
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
              {categories.length === 0 && (
                <Center py="xl">
                  <Text c="dimmed">No categories yet. Create one to organise the catalog.</Text>
                </Center>
              )}
            </>
          )}
        </Stack>
      </Card>
      <Modal
        opened={modalState !== null}
        onClose={() => setModalState(null)}
        title={isEdit ? "Edit category" : "Create new category"}
        centered
      >
        <form onSubmit={handleSubmit}>
          <Stack>
            <TextInput
              label="Name"
              placeholder="e.g. Furniture"
              withAsterisk
              data-autofocus
              {...form.getInputProps("name")}
            />
            <Select
              label="Parent category"
              placeholder="None (root category)"
              clearable
              searchable
              data={parentOptions}
              {...form.getInputProps("parentId")}
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setModalState(null)}>
                Cancel
              </Button>
              <Button type="submit" loading={mutation.isPending}>
                {isEdit ? "Save changes" : "Create category"}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
};
