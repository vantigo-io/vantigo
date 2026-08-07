import { Alert, Badge, Button, Card, Group, Loader, Stack, Text, Timeline, Title } from "@mantine/core";
import { IconArrowLeft, IconCircleCheck } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { messageEventsQueryOptions, messageQueryOptions } from "../api/messages";
import { eventLabel, statusColor, statusLabel } from "../lib/status";
export const Route = createFileRoute("/messages/$messageId")({ component: DetailPage });
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
function DetailPage() {
  const { messageId } = Route.useParams();
  const message = useQuery(messageQueryOptions(messageId));
  const events = useQuery(messageEventsQueryOptions(messageId));
  if (message.isPending) return <Loader />;
  if (message.isError)
    return (
      <Alert color="red" title="Message unavailable">
        {message.error.message}
      </Alert>
    );
  const m = message.data;
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
      <Group justify="space-between" align="start">
        <div>
          <Text className="eyebrow">Message detail</Text>
          <Title order={2}>{m.subject}</Title>
          <Text c="dimmed">
            {m.source || "Unknown source"} · {new Date(m.createdAt).toLocaleString()}
          </Text>
          {m.mailbox && (
            <Text size="sm" c="dimmed">
              Sent from{" "}
              {m.mailbox.displayName ? `${m.mailbox.displayName} <${m.mailbox.fromAddress}>` : m.mailbox.fromAddress}
            </Text>
          )}
        </div>
      </Group>
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
