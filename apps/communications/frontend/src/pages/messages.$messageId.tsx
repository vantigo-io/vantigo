import { Alert, Badge, Button, Card, Group, Loader, Menu, Modal, Stack, Text, Timeline, Title } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import {
  IconArchive,
  IconArchiveOff,
  IconArrowLeft,
  IconChevronDown,
  IconCircleCheck,
  IconSend,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { PageHeader } from "@vantigo/frontend-shell";
import {
  archiveMessage,
  messageEventsQueryOptions,
  messageQueryOptions,
  type ResendScope,
  resendMessage,
  unarchiveMessage,
} from "../api/messages";
import { eventLabel, statusColor, statusLabel } from "../lib/status";

const htmlAsText = (html: string) => {
  const element = document.createElement("div");
  element.innerHTML = html;
  return element.textContent || "";
};
const eventDescription = (dataJson: string | null) => {
  if (!dataJson) return "Status recorded by the service.";
  try {
    const parsed = JSON.parse(dataJson) as { error?: string };
    if (parsed && typeof parsed.error === "string" && parsed.error) return parsed.error;
  } catch {
    // Fall through to the raw payload.
  }
  return dataJson;
};
export function DetailPage() {
  const { messageId } = useParams({ strict: false }) as { messageId: string };
  const queryClient = useQueryClient();
  const message = useQuery(messageQueryOptions(messageId));
  const events = useQuery(messageEventsQueryOptions(messageId));
  const [archiveConfirmOpened, archiveConfirm] = useDisclosure(false);
  const invalidate = () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: ["message", messageId] }),
      queryClient.invalidateQueries({ queryKey: ["message-events", messageId] }),
      queryClient.invalidateQueries({ queryKey: ["messages"] }),
    ]);
  const resend = useMutation({
    mutationFn: (scope: ResendScope) => resendMessage(messageId, scope),
    onSuccess: async (result) => {
      notifications.show({
        title: "Message re-queued",
        message: `${result.requeuedRecipientCount} recipient${result.requeuedRecipientCount === 1 ? "" : "s"} queued for sending.`,
      });
      await invalidate();
    },
    onError: (error) => notifications.show({ color: "red", title: "Could not resend", message: error.message }),
  });
  const archive = useMutation({
    mutationFn: () => archiveMessage(messageId),
    onSuccess: async () => {
      archiveConfirm.close();
      notifications.show({ title: "Message archived", message: "The message was moved to the archive." });
      await invalidate();
    },
    onError: (error) => notifications.show({ color: "red", title: "Could not archive", message: error.message }),
  });
  const unarchive = useMutation({
    mutationFn: () => unarchiveMessage(messageId),
    onSuccess: async () => {
      notifications.show({ title: "Message restored", message: "The message is back in the history." });
      await invalidate();
    },
    onError: (error) => notifications.show({ color: "red", title: "Could not restore", message: error.message }),
  });
  if (message.isPending) return <Loader />;
  if (message.isError)
    return (
      <Alert color="red" title="Message unavailable">
        {message.error.message}
      </Alert>
    );
  const m = message.data;
  const isArchived = m.archivedAt !== null;
  const hasFailedDeliveries = m.deliveries.some((delivery) => delivery.status === "submission_failed");
  const sendInProgress = m.deliveries.some((delivery) => ["queued", "sending", "retrying"].includes(delivery.status));
  const body = m.textBody || (m.htmlBody ? htmlAsText(m.htmlBody) : "No message body was provided.");
  return (
    <Stack gap="xl">
      <Button
        component={Link}
        to="/messages"
        variant="subtle"
        leftSection={<IconArrowLeft size={16} />}
        w="fit-content"
      >
        Back to history
      </Button>
      <PageHeader
        eyebrow="Communications"
        title={m.subject}
        description={
          <>
            {m.source || "Unknown source"} · {new Date(m.createdAt).toLocaleString()}
            {m.mailbox && (
              <>
                {" · Sent from "}
                {m.mailbox.displayName ? `${m.mailbox.displayName} <${m.mailbox.fromAddress}>` : m.mailbox.fromAddress}
              </>
            )}
          </>
        }
        actions={
          <Group gap="sm">
            {isArchived && (
              <Badge color="gray" variant="light">
                Archived
              </Badge>
            )}
            {!isArchived && (
              <Menu position="bottom-end" withinPortal>
                <Menu.Target>
                  <Button
                    variant={hasFailedDeliveries ? "filled" : "light"}
                    leftSection={<IconSend size={16} />}
                    rightSection={<IconChevronDown size={16} />}
                    loading={resend.isPending}
                    disabled={sendInProgress}
                    title={sendInProgress ? "A send is already in progress." : undefined}
                  >
                    Resend
                  </Button>
                </Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item disabled={!hasFailedDeliveries} onClick={() => resend.mutate("failed")}>
                    Resend failed recipients only
                  </Menu.Item>
                  <Menu.Item onClick={() => resend.mutate("all")}>Resend to all recipients</Menu.Item>
                </Menu.Dropdown>
              </Menu>
            )}
            {isArchived ? (
              <Button
                variant="default"
                leftSection={<IconArchiveOff size={16} />}
                loading={unarchive.isPending}
                onClick={() => unarchive.mutate()}
              >
                Unarchive
              </Button>
            ) : (
              <Button
                variant="default"
                color="gray"
                leftSection={<IconArchive size={16} />}
                onClick={archiveConfirm.open}
              >
                Archive
              </Button>
            )}
          </Group>
        }
      />
      <Modal opened={archiveConfirmOpened} onClose={archiveConfirm.close} title="Archive message" centered>
        <Stack gap="md">
          <Text>
            The message will be hidden from the default history view.
            {sendInProgress ? " Any pending send will be cancelled." : ""} You can unarchive it later.
          </Text>
          <Group justify="end">
            <Button variant="default" onClick={archiveConfirm.close}>
              Cancel
            </Button>
            <Button color="red" loading={archive.isPending} onClick={() => archive.mutate()}>
              Archive
            </Button>
          </Group>
        </Stack>
      </Modal>
      <Card withBorder radius="lg">
        <Text size="sm" c="dimmed">
          Message body
        </Text>
        <Text mt="sm" style={{ whiteSpace: "pre-wrap" }}>
          {body}
        </Text>
      </Card>
      <Card withBorder radius="lg">
        <Title order={4} mb="lg">
          Recipients
        </Title>
        <Stack gap="sm">
          {m.deliveries.length ? (
            m.deliveries.map((delivery) => (
              <Group key={delivery.id} justify="space-between">
                <div>
                  <Text>{delivery.emailAddress}</Text>
                  <Text size="xs" c="dimmed">
                    {delivery.recipientType}
                  </Text>
                  {delivery.lastError && (
                    <Text size="xs" c="red">
                      {delivery.lastError}
                    </Text>
                  )}
                </div>
                <Badge color={statusColor(delivery.status)} variant="light">
                  {statusLabel(delivery.status)}
                </Badge>
              </Group>
            ))
          ) : (
            <Text c="dimmed">No recipients recorded.</Text>
          )}
        </Stack>
      </Card>
      {m.externalLinks.length > 0 && (
        <Card withBorder radius="lg">
          <Title order={4} mb="lg">
            External references
          </Title>
          <Stack gap="sm">
            {m.externalLinks.map((link) => (
              <div key={link.id}>
                <Text fw={600}>{link.displayLabel || link.externalEntityId}</Text>
                <Text size="sm" c="dimmed">
                  {link.sourceSystem} · {link.sourceInstance} · {link.entityType}
                </Text>
              </div>
            ))}
          </Stack>
        </Card>
      )}
      <Card withBorder radius="lg">
        <Title order={4} mb="lg">
          Delivery timeline
        </Title>
        {events.isPending ? (
          <Loader size="sm" />
        ) : events.isError ? (
          <Alert color="red">Could not load delivery events.</Alert>
        ) : events.data.data.length === 0 ? (
          <Text c="dimmed">No events recorded.</Text>
        ) : (
          <Timeline active={events.data.data.length - 1}>
            {events.data.data.map((e) => (
              <Timeline.Item
                key={e.id}
                bullet={<IconCircleCheck size={14} />}
                title={
                  <Group gap="sm">
                    <Text fw={600}>{eventLabel(e.eventType)}</Text>
                    <Text size="xs" c="dimmed">
                      {new Date(e.occurredAt).toLocaleString()}
                    </Text>
                  </Group>
                }
              >
                <Text size="sm" c="dimmed">
                  {eventDescription(e.dataJson)}
                </Text>
              </Timeline.Item>
            ))}
          </Timeline>
        )}
      </Card>
    </Stack>
  );
}
