import { Alert, Badge, Button, Card, Group, Loader, Stack, Table, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, redirect } from "@tanstack/react-router";
import { useState } from "react";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { createMailbox, mailboxesQueryOptions, updateMailbox } from "../api/mailboxes";
import type { ApiError } from "../api/request";

export function MailboxesPage() {
  const client = useQueryClient();
  const query = useQuery(mailboxesQueryOptions());
  const form = useForm({ initialValues: { fromAddress: "", displayName: "" } });
  const [deactivatingId, setDeactivatingId] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: createMailbox,
    onSuccess: () => {
      form.reset();
      void client.invalidateQueries({ queryKey: ["mailboxes"] });
      notifications.show({ title: "Mailbox created", message: "The workspace mailbox is ready." });
    },
    onError: (error: ApiError) =>
      notifications.show({ color: "red", title: "Mailbox not created", message: error.message }),
  });
  const update = useMutation({
    mutationFn: ({ id, isActive, displayName }: { id: string; isActive: boolean; displayName: string | null }) =>
      updateMailbox(id, { isActive, displayName }),
    onSuccess: () => {
      setDeactivatingId(null);
      void client.invalidateQueries({ queryKey: ["mailboxes"] });
      notifications.show({ title: "Mailbox updated", message: "Mailbox settings saved." });
    },
    onError: (error: ApiError) =>
      notifications.show({ color: "red", title: "Mailbox not updated", message: error.message }),
  });
  if (query.isPending) return <Loader />;
  if (query.isError) return <Alert color="red">{query.error.message}</Alert>;
  const mailboxes = query.data;
  return (
    <Stack gap="xl">
      <div>
        <Text className="eyebrow">Administration</Text>
        <Title order={2}>Mailboxes</Title>
        <Text c="dimmed">Configure the single active sending mailbox for this workspace.</Text>
      </div>
      {mailboxes.length === 0 && (
        <Card withBorder radius="lg">
          <form
            onSubmit={form.onSubmit((values) =>
              create.mutate({ fromAddress: values.fromAddress, displayName: values.displayName || undefined }),
            )}
          >
            <Stack>
              <Title order={4}>Add mailbox</Title>
              <TextInput label="From address" type="email" required {...form.getInputProps("fromAddress")} />
              <TextInput label="Display name" {...form.getInputProps("displayName")} />
              <Button type="submit" loading={create.isPending} w="fit-content">
                Create mailbox
              </Button>
            </Stack>
          </form>
        </Card>
      )}
      <Card withBorder radius="lg">
        <Table.ScrollContainer minWidth={650}>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>From address</Table.Th>
                <Table.Th>Display name</Table.Th>
                <Table.Th>Status</Table.Th>
                <Table.Th>Created</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {mailboxes.map((mailbox) => (
                <MailboxRow
                  key={mailbox.id}
                  mailbox={mailbox}
                  onDeactivate={() => setDeactivatingId(mailbox.id)}
                  onSave={(displayName) => update.mutate({ id: mailbox.id, isActive: mailbox.isActive, displayName })}
                  onToggle={(isActive) =>
                    isActive
                      ? update.mutate({ id: mailbox.id, isActive, displayName: mailbox.displayName })
                      : setDeactivatingId(mailbox.id)
                  }
                />
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      </Card>
      {deactivatingId && (
        <Card withBorder shadow="md">
          <Stack>
            <Text fw={600}>Deactivate this mailbox?</Text>
            <Text size="sm" c="dimmed">
              Messages cannot be sent while no active mailbox is configured.
            </Text>
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setDeactivatingId(null)}>
                Cancel
              </Button>
              <Button
                color="red"
                loading={update.isPending}
                onClick={() => {
                  const mailbox = mailboxes.find((item) => item.id === deactivatingId);
                  if (mailbox) update.mutate({ id: mailbox.id, isActive: false, displayName: mailbox.displayName });
                }}
              >
                Deactivate
              </Button>
            </Group>
          </Stack>
        </Card>
      )}
    </Stack>
  );
}

function MailboxRow({
  mailbox,
  onSave,
  onToggle,
}: {
  mailbox: { id: string; fromAddress: string; displayName: string | null; createdAt: string; isActive: boolean };
  onDeactivate: () => void;
  onSave: (displayName: string | null) => void;
  onToggle: (isActive: boolean) => void;
}) {
  const [displayName, setDisplayName] = useState(mailbox.displayName || "");
  return (
    <Table.Tr>
      <Table.Td>{mailbox.fromAddress}</Table.Td>
      <Table.Td>
        <TextInput
          aria-label={`Display name for ${mailbox.fromAddress}`}
          value={displayName}
          onChange={(event) => setDisplayName(event.currentTarget.value)}
          onBlur={() => onSave(displayName || null)}
        />
      </Table.Td>
      <Table.Td>
        <Badge color={mailbox.isActive ? "green" : "gray"}>{mailbox.isActive ? "Active" : "Inactive"}</Badge>
      </Table.Td>
      <Table.Td>
        <Text size="sm">{new Date(mailbox.createdAt).toLocaleString()}</Text>
      </Table.Td>
      <Table.Td>
        <Button
          size="xs"
          color={mailbox.isActive ? "red" : "green"}
          variant="subtle"
          onClick={() => onToggle(!mailbox.isActive)}
        >
          {mailbox.isActive ? "Deactivate" : "Activate"}
        </Button>
      </Table.Td>
    </Table.Tr>
  );
}

export const Route = createFileRoute("/admin/mailboxes")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.ensureQueryData({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/messages", search: { page: 1 } });
  },
  component: MailboxesPage,
});
