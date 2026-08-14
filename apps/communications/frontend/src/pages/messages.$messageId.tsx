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
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import {
  archiveMessage,
  messageEventsQueryOptions,
  messageQueryOptions,
  type ResendScope,
  resendMessage,
  unarchiveMessage,
} from "../api/messages";
import { eventLabel, statusColor, statusLabel } from "../lib/status";
import "../i18n";

const htmlAsText = (html: string) => {
  const element = document.createElement("div");
  element.innerHTML = html;
  return element.textContent || "";
};
const eventDescription = (dataJson: string | null, statusRecorded: string) => {
  if (!dataJson) return statusRecorded;
  try {
    const parsed = JSON.parse(dataJson) as { error?: string };
    if (parsed && typeof parsed.error === "string" && parsed.error) return parsed.error;
  } catch {
    // Fall through to the raw payload.
  }
  return dataJson;
};
export function DetailPage() {
  const { t, formatters } = useI18n("communications");
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
        title: t("messageRequeued"),
        message: t(result.requeuedRecipientCount === 1 ? "recipientsQueuedSingular" : "recipientsQueuedPlural", {
          count: result.requeuedRecipientCount,
        }),
      });
      await invalidate();
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotResend"), message: error.message }),
  });
  const archive = useMutation({
    mutationFn: () => archiveMessage(messageId),
    onSuccess: async () => {
      archiveConfirm.close();
      notifications.show({ title: t("messageArchived"), message: t("messageMovedToArchive") });
      await invalidate();
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotArchive"), message: error.message }),
  });
  const unarchive = useMutation({
    mutationFn: () => unarchiveMessage(messageId),
    onSuccess: async () => {
      notifications.show({ title: t("messageRestored"), message: t("messageBackInHistory") });
      await invalidate();
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotRestore"), message: error.message }),
  });
  if (message.isPending) return <Loader />;
  if (message.isError)
    return (
      <Alert color="red" title={t("messageUnavailable")}>
        {message.error.message}
      </Alert>
    );
  const m = message.data;
  const isArchived = m.archivedAt !== null;
  const hasFailedDeliveries = m.deliveries.some((delivery) => delivery.status === "submission_failed");
  const sendInProgress = m.deliveries.some((delivery) => ["queued", "sending", "retrying"].includes(delivery.status));
  const body = m.textBody || (m.htmlBody ? htmlAsText(m.htmlBody) : t("noMessageBody"));
  return (
    <Stack gap="xl">
      <Button
        component={Link}
        to="/messages"
        variant="subtle"
        leftSection={<IconArrowLeft size={16} />}
        w="fit-content"
      >
        {t("searchHistory")}
      </Button>
      <PageHeader
        eyebrow={t("communications")}
        title={m.subject}
        description={
          <>
            {m.source || t("unknownSource")} ·{" "}
            {formatters.formatDate(m.createdAt, { dateStyle: "medium", timeStyle: "short" })}
            {m.mailbox && (
              <>
                {` · ${t("sentFrom")} `}
                {m.mailbox.displayName ? `${m.mailbox.displayName} <${m.mailbox.fromAddress}>` : m.mailbox.fromAddress}
              </>
            )}
          </>
        }
        actions={
          <Group gap="sm">
            {isArchived && (
              <Badge color="gray" variant="light">
                {t("archived")}
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
                    title={sendInProgress ? t("sendInProgress") : undefined}
                  >
                    {t("resend")}
                  </Button>
                </Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item disabled={!hasFailedDeliveries} onClick={() => resend.mutate("failed")}>
                    {t("resendFailedRecipients")}
                  </Menu.Item>
                  <Menu.Item onClick={() => resend.mutate("all")}>{t("resendAllRecipients")}</Menu.Item>
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
                {t("unarchive")}
              </Button>
            ) : (
              <Button
                variant="default"
                color="gray"
                leftSection={<IconArchive size={16} />}
                onClick={archiveConfirm.open}
              >
                {t("archive")}
              </Button>
            )}
          </Group>
        }
      />
      <Modal opened={archiveConfirmOpened} onClose={archiveConfirm.close} title={t("archiveMessage")} centered>
        <Stack gap="md">
          <Text>
            {t("archiveMessageDescription", { pendingText: sendInProgress ? t("pendingSendCancelled") : "" })}
          </Text>
          <Group justify="end">
            <Button variant="default" onClick={archiveConfirm.close}>
              {t("cancel")}
            </Button>
            <Button color="red" loading={archive.isPending} onClick={() => archive.mutate()}>
              {t("archive")}
            </Button>
          </Group>
        </Stack>
      </Modal>
      <Card withBorder radius="lg">
        <Text size="sm" c="dimmed">
          {t("messageBody")}
        </Text>
        <Text mt="sm" style={{ whiteSpace: "pre-wrap" }}>
          {body}
        </Text>
      </Card>
      <Card withBorder radius="lg">
        <Title order={4} mb="lg">
          {t("recipients")}
        </Title>
        <Stack gap="sm">
          {m.deliveries.length ? (
            m.deliveries.map((delivery) => (
              <Group key={delivery.id} justify="space-between">
                <div>
                  <Text>{delivery.emailAddress}</Text>
                  <Text size="xs" c="dimmed">
                    {delivery.recipientType === "to"
                      ? t("recipientTo")
                      : delivery.recipientType === "cc"
                        ? t("recipientCc")
                        : delivery.recipientType === "bcc"
                          ? t("recipientBcc")
                          : delivery.recipientType}
                  </Text>
                  {delivery.lastError && (
                    <Text size="xs" c="red">
                      {delivery.lastError}
                    </Text>
                  )}
                </div>
                <Badge color={statusColor(delivery.status)} variant="light">
                  {statusLabel(delivery.status, t)}
                </Badge>
              </Group>
            ))
          ) : (
            <Text c="dimmed">{t("noRecipientsRecorded")}</Text>
          )}
        </Stack>
      </Card>
      {m.externalLinks.length > 0 && (
        <Card withBorder radius="lg">
          <Title order={4} mb="lg">
            {t("externalReferences")}
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
          {t("deliveryTimeline")}
        </Title>
        {events.isPending ? (
          <Loader size="sm" />
        ) : events.isError ? (
          <Alert color="red">{t("couldNotLoadDeliveryEvents")}</Alert>
        ) : events.data.data.length === 0 ? (
          <Text c="dimmed">{t("noEventsRecorded")}</Text>
        ) : (
          <Timeline active={events.data.data.length - 1}>
            {events.data.data.map((e) => (
              <Timeline.Item
                key={e.id}
                bullet={<IconCircleCheck size={14} />}
                title={
                  <Group gap="sm">
                    <Text fw={600}>{eventLabel(e.eventType, t)}</Text>
                    <Text size="xs" c="dimmed">
                      {formatters.formatDate(e.occurredAt, { dateStyle: "medium", timeStyle: "short" })}
                    </Text>
                  </Group>
                }
              >
                <Text size="sm" c="dimmed">
                  {eventDescription(e.dataJson, t("statusRecorded"))}
                </Text>
              </Timeline.Item>
            ))}
          </Timeline>
        )}
      </Card>
    </Stack>
  );
}
