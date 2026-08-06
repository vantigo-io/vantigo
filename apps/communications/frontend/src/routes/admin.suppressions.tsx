import { Alert, Button, Card, Group, Loader, Modal, Stack, Table, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, redirect } from "@tanstack/react-router";
import { useState } from "react";
import { fetchSession, sessionQueryKey } from "../api/auth";
import type { ApiError } from "../api/request";
import { createSuppression, deleteSuppression, suppressionsQueryOptions } from "../api/suppressions";

export function SuppressionsPage() {
  const client = useQueryClient();
  const query = useQuery(suppressionsQueryOptions());
  const form = useForm({
    initialValues: { emailAddress: "", reason: "" },
    validate: {
      emailAddress: (value) => (/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value) ? null : "Enter a valid email address"),
    },
  });
  const [search, setSearch] = useState("");
  const [deleteId, setDeleteId] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: createSuppression,
    onSuccess: () => {
      form.reset();
      void client.invalidateQueries({ queryKey: ["suppressions"] });
      notifications.show({ title: "Suppression added", message: "The address will not receive messages." });
    },
    onError: (error: ApiError) =>
      notifications.show({ color: "red", title: "Suppression not added", message: error.message }),
  });
  const remove = useMutation({
    mutationFn: deleteSuppression,
    onSuccess: () => {
      setDeleteId(null);
      void client.invalidateQueries({ queryKey: ["suppressions"] });
      notifications.show({ title: "Suppression deleted", message: "The address can receive messages again." });
    },
    onError: (error: ApiError) =>
      notifications.show({ color: "red", title: "Suppression not deleted", message: error.message }),
  });
  if (query.isPending) return <Loader />;
  if (query.isError) return <Alert color="red">{query.error.message}</Alert>;
  const visible = query.data.filter((item) =>
    `${item.emailAddress} ${item.reason || ""}`.toLowerCase().includes(search.toLowerCase()),
  );
  return (
    <Stack gap="xl">
      <div>
        <Text className="eyebrow">Administration</Text>
        <Title order={2}>Suppressions</Title>
        <Text c="dimmed">Keep unwanted recipients out of future messages.</Text>
      </div>
      <Card withBorder radius="lg">
        <form
          onSubmit={form.onSubmit((values) =>
            create.mutate({ emailAddress: values.emailAddress.trim(), reason: values.reason || undefined }),
          )}
        >
          <Group align="end">
            <TextInput label="Email address" required {...form.getInputProps("emailAddress")} />
            <TextInput label="Reason (optional)" {...form.getInputProps("reason")} />
            <Button type="submit" loading={create.isPending}>
              Add suppression
            </Button>
          </Group>
        </form>
      </Card>
      <Card withBorder radius="lg">
        <TextInput
          label="Search"
          placeholder="Search email or reason"
          value={search}
          onChange={(event) => setSearch(event.currentTarget.value)}
          mb="lg"
        />
        <Table.ScrollContainer minWidth={620}>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Email</Table.Th>
                <Table.Th>Reason</Table.Th>
                <Table.Th>Created</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {visible.map((item) => (
                <Table.Tr key={item.id}>
                  <Table.Td>{item.emailAddress}</Table.Td>
                  <Table.Td>{item.reason || "—"}</Table.Td>
                  <Table.Td>{new Date(item.createdAt).toLocaleString()}</Table.Td>
                  <Table.Td>
                    <Button color="red" variant="subtle" size="xs" onClick={() => setDeleteId(item.id)}>
                      Delete
                    </Button>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
        {visible.length === 0 && (
          <Text c="dimmed" ta="center" py="lg">
            No suppressions found.
          </Text>
        )}
      </Card>
      <Modal opened={deleteId !== null} onClose={() => setDeleteId(null)} title="Delete suppression" centered>
        <Stack>
          <Text>Allow this address to receive messages again?</Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setDeleteId(null)}>
              Cancel
            </Button>
            <Button color="red" loading={remove.isPending} onClick={() => deleteId && remove.mutate(deleteId)}>
              Delete suppression
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  );
}

export const Route = createFileRoute("/admin/suppressions")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.ensureQueryData({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/messages", search: { page: 1 } });
  },
  component: SuppressionsPage,
});
