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
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
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
  const client = useQueryClient();
  const { data = [] } = useQuery(taxCategoriesQueryOptions());
  const [editing, setEditing] = useState<TaxCategoryResponse | null | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);
  const form = useForm<FormValues>({
    initialValues: { name: "", kind: "Standard", rate: "" },
    validate: {
      name: (v) => (v.trim() ? null : "Name is required"),
      rate: (v) => (Number(v) >= 0 && Number(v) <= 100 ? null : "Enter a percentage from 0 to 100"),
    },
  });
  const save = useMutation({
    mutationFn: (input: TaxCategoryInput) =>
      editing ? updateTaxCategory(editing.id, input) : createTaxCategory(input),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["tax-categories"] });
      setEditing(undefined);
      notifications.show({ color: "teal", title: "Tax category saved", message: "Your changes are live." });
    },
    onError: (e) => setError(e.message),
  });
  const remove = useMutation({
    mutationFn: deleteTaxCategory,
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["tax-categories"] });
      setError(null);
    },
    onError: (e: ApiError) =>
      setError(e.status === 409 ? "This tax category is in use and cannot be deleted." : e.message),
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
      <Group justify="space-between">
        <Title order={2}>Tax categories</Title>
        <Button leftSection={<IconPlus size={16} />} onClick={() => open()}>
          New tax category
        </Button>
      </Group>
      {error && (
        <Alert color="red" title="Could not complete that action" withCloseButton onClose={() => setError(null)}>
          {error}
        </Alert>
      )}
      <Card withBorder>
        <Table striped>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Name</Table.Th>
              <Table.Th>Kind</Table.Th>
              <Table.Th>Rate</Table.Th>
              <Table.Th w={96} />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {data.map((category) => (
              <Table.Tr key={category.id}>
                <Table.Td>{category.name}</Table.Td>
                <Table.Td>{category.kind}</Table.Td>
                <Table.Td>{(category.rate * 100).toFixed(2)}%</Table.Td>
                <Table.Td>
                  <Group gap={4} justify="flex-end">
                    <ActionIcon aria-label={`Edit ${category.name}`} variant="subtle" onClick={() => open(category)}>
                      <IconPencil size={16} />
                    </ActionIcon>
                    <ActionIcon
                      aria-label={`Delete ${category.name}`}
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
            No tax categories yet.
          </Text>
        )}
      </Card>
      <Modal
        opened={editing !== undefined}
        onClose={() => setEditing(undefined)}
        title={editing ? "Edit tax category" : "New tax category"}
        centered
      >
        <form onSubmit={submit}>
          <Stack>
            <TextInput label="Name" withAsterisk {...form.getInputProps("name")} />
            <Select label="Kind" data={kinds} allowDeselect={false} {...form.getInputProps("kind")} />
            <NumberInput
              label="Rate (%)"
              description="Enter 25 for a 25% rate"
              min={0}
              max={100}
              decimalScale={2}
              withAsterisk
              {...form.getInputProps("rate")}
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setEditing(undefined)}>
                Cancel
              </Button>
              <Button type="submit" loading={save.isPending}>
                Save
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
};
