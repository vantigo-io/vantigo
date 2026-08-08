import {
  Alert,
  Badge,
  Card,
  Center,
  Group,
  Loader,
  Pagination,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { IconAlertCircle, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { messagesQueryOptions } from "../api/messages";
import { statusColor, statusLabel } from "../lib/status";
export const Route = createFileRoute("/messages/")({
  validateSearch: (s: Record<string, unknown>): { page: number; archived?: boolean } => ({
    page: Math.max(1, Number(s.page) || 1),
    archived: s.archived === true || s.archived === "true" || undefined,
  }),
  loaderDeps: ({ search }) => search,
  loader: ({ context: { queryClient }, deps: { page, archived } }) =>
    queryClient.ensureQueryData(messagesQueryOptions(page, 20, undefined, archived ?? false)),
  component: MessagesPage,
});
export function MessagesPage() {
  const { page, archived } = Route.useSearch();
  const navigate = Route.useNavigate();
  const includeArchived = archived ?? false;
  const q = useQuery(messagesQueryOptions(page, 20, undefined, includeArchived));
  return (
    <Stack gap="xl">
      <Group justify="space-between" align="end">
        <div>
          <Text className="eyebrow">Activity log</Text>
          <Title order={2}>Message history</Title>
          <Text c="dimmed">A clear trail from submission to reply.</Text>
        </div>
        {q.data && (
          <Badge size="lg" variant="light">
            {q.data.pagination.totalCount} messages
          </Badge>
        )}
      </Group>
      <Card withBorder radius="lg" padding="lg">
        <Group mb="lg" align="center" gap="md">
          <TextInput
            placeholder="Search history"
            leftSection={<IconSearch size={16} />}
            disabled
            aria-label="Search history (coming soon)"
            style={{ flex: 1 }}
          />
          <Switch
            label="Show archived"
            checked={includeArchived}
            onChange={(event) => navigate({ search: { page: 1, archived: event.currentTarget.checked || undefined } })}
          />
        </Group>
        {q.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title="Could not load messages">
            {q.error.message}
          </Alert>
        )}
        {q.isPending && (
          <Center py="xl">
            <Loader color="indigo" />
          </Center>
        )}
        {q.data &&
          !q.isError &&
          (q.data.data.length === 0 ? (
            <Center py={70}>
              <Stack align="center">
                <div className="empty-icon">✦</div>
                <Title order={4}>No messages yet</Title>
                <Text c="dimmed" ta="center">
                  Messages sent through your service will appear here.
                </Text>
              </Stack>
            </Center>
          ) : (
            <>
              <Table.ScrollContainer minWidth={720}>
                <Table highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>Message</Table.Th>
                      <Table.Th>Recipients</Table.Th>
                      <Table.Th>Status</Table.Th>
                      <Table.Th>Created</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {q.data.data.map((m) => (
                      <Table.Tr key={m.id} opacity={m.archivedAt ? 0.6 : 1}>
                        <Table.Td>
                          <Group gap="xs" wrap="nowrap">
                            <Link className="message-link" to="/messages/$messageId" params={{ messageId: m.id }}>
                              {m.subject || `Message ${m.id}`}
                            </Link>
                            {m.archivedAt && (
                              <Badge size="xs" color="gray" variant="light">
                                Archived
                              </Badge>
                            )}
                          </Group>
                        </Table.Td>
                        <Table.Td>{m.recipientCount}</Table.Td>
                        <Table.Td>
                          <Badge color={statusColor(m.status)} variant="light">
                            {statusLabel(m.status)}
                          </Badge>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm" c="dimmed">
                            {new Date(m.createdAt).toLocaleString()}
                          </Text>
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
              {q.data.pagination.totalPages > 1 && (
                <Group justify="center">
                  <Pagination
                    total={q.data.pagination.totalPages}
                    value={page}
                    onChange={(p) => navigate({ search: { page: p, archived: includeArchived || undefined } })}
                  />
                </Group>
              )}
            </>
          ))}
      </Card>
    </Stack>
  );
}
