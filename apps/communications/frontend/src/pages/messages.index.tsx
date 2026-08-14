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
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { messagesQueryOptions } from "../api/messages";
import { statusColor, statusLabel } from "../lib/status";
import "../i18n";
export function MessagesPage() {
  const { t, formatters } = useI18n("communications");
  const { page, archived } = useSearch({ strict: false }) as { page: number; archived?: boolean };
  const navigate = useNavigate() as (options: unknown) => void;
  const includeArchived = archived ?? false;
  const q = useQuery(messagesQueryOptions(page, 20, undefined, includeArchived));
  return (
    <Stack gap="xl">
      <PageHeader
        eyebrow={t("communications")}
        title={t("messages")}
        description={t("messagesDescription")}
        actions={
          q.data && (
            <Badge size="lg" variant="light">
              {t(q.data.pagination.totalCount === 1 ? "messageCountSingular" : "messageCountPlural", {
                count: q.data.pagination.totalCount,
              })}
            </Badge>
          )
        }
      />
      <Card withBorder radius="lg" padding="lg">
        <Group mb="lg" align="center" gap="md">
          <TextInput
            placeholder={t("searchHistory")}
            leftSection={<IconSearch size={16} />}
            disabled
            aria-label={t("searchHistoryComingSoon")}
            style={{ flex: 1 }}
          />
          <Switch
            label={t("showArchived")}
            checked={includeArchived}
            onChange={(event) => navigate({ search: { page: 1, archived: event.currentTarget.checked || undefined } })}
          />
        </Group>
        {q.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoadMessages")}>
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
                <Title order={4}>{t("noMessagesYet")}</Title>
                <Text c="dimmed" ta="center">
                  {t("messagesWillAppear")}
                </Text>
              </Stack>
            </Center>
          ) : (
            <>
              <Table.ScrollContainer minWidth={720}>
                <Table highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("message")}</Table.Th>
                      <Table.Th>{t("recipients")}</Table.Th>
                      <Table.Th>{t("status")}</Table.Th>
                      <Table.Th>{t("created")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {q.data.data.map((m) => (
                      <Table.Tr key={m.id} opacity={m.archivedAt ? 0.6 : 1}>
                        <Table.Td>
                          <Group gap="xs" wrap="nowrap">
                            <Link className="message-link" to="/messages/$messageId" params={{ messageId: m.id }}>
                              {m.subject || t("messageFallback", { id: m.id })}
                            </Link>
                            {m.archivedAt && (
                              <Badge size="xs" color="gray" variant="light">
                                {t("archived")}
                              </Badge>
                            )}
                          </Group>
                        </Table.Td>
                        <Table.Td>{m.recipientCount}</Table.Td>
                        <Table.Td>
                          <Badge color={statusColor(m.status)} variant="light">
                            {statusLabel(m.status, t)}
                          </Badge>
                        </Table.Td>
                        <Table.Td>
                          <Text size="sm" c="dimmed">
                            {formatters.formatDate(m.createdAt, { dateStyle: "medium", timeStyle: "short" })}
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
