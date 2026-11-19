import { Alert, Badge, Button, Code, Drawer, Group, Select, Stack, Table, Text, VisuallyHidden } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconArrowLeft } from "@tabler/icons-react";
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageSection, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import {
  getWebhookDelivery,
  listWebhookDeliveries,
  resendWebhookDelivery,
  type WebhookDelivery,
  type WebhookDeliveryFilters,
  type WebhookDeliveryState,
  type WebhookEndpoint,
  webhookDeliveriesQueryKey,
  webhooksQueryKey,
} from "../../api/webhooks";
import { codeOf } from "./-api-access-shared";

/** The states a delivery is in (outgoing webhooks design W23), as the State filter offers them. */
export const deliveryStates = ["pending", "succeeded", "dead", "skipped"] as const satisfies WebhookDeliveryState[];

const stateColor: Record<WebhookDeliveryState, string> = {
  pending: "blue",
  succeeded: "teal",
  dead: "red",
  skipped: "gray",
};

/** The test event's name, which the Event filter offers beside the endpoint's events (W19). */
const pingName = "webhook.ping";

/** The refusals of a resend the screen explains in its own words; any other shows the server's message. */
const resendRefusals = new Set([
  "rate_limited",
  "webhook_resend_limited",
  "webhook_event_not_permitted",
  "webhook_event_not_subscribed",
  "webhook_endpoint_disabled",
  "webhook_delivery_not_resendable",
  "integration_archived",
  "not_found",
]);

/**
 * The receiver's text — an attempt's excerpt — or a body, shown as plain
 * text only (S7): a React text node in a block of code, never HTML, Markdown
 * or a link, whatever it holds.
 */
const PlainText = ({ value, testId }: { value: string; testId?: string }) => (
  <Code block style={{ whiteSpace: "pre-wrap", wordBreak: "break-all" }} data-testid={testId}>
    {value}
  </Code>
);

/**
 * A webhook endpoint's delivery log (W22, W23): its deliveries in the last 30
 * days, newest first, filtered by state and event, a page at a time; a
 * delivery's body and attempts in a drawer; and resending one that succeeded
 * or is dead, after a confirmation, while the endpoint is enabled and the
 * integration is not archived.
 */
export const WebhookDeliveries = ({
  integrationId,
  endpoint,
  archived,
  onBack,
}: {
  integrationId: string;
  endpoint: WebhookEndpoint;
  archived: boolean;
  onBack: () => void;
}) => {
  const { t, formatters } = useI18n("host");
  const queryClient = useQueryClient();
  const [filters, setFilters] = useState<WebhookDeliveryFilters>({});
  const [opened, setOpened] = useState<string | null>(null);
  const key = webhookDeliveriesQueryKey(integrationId, endpoint.id);
  const log = useInfiniteQuery({
    queryKey: [...key, filters],
    queryFn: ({ pageParam }) => listWebhookDeliveries(integrationId, endpoint.id, filters, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
    placeholderData: keepPreviousData,
  });
  const resend = useMutation({
    mutationFn: (delivery: WebhookDelivery) => resendWebhookDelivery(integrationId, endpoint.id, delivery.id),
    onSuccess: async () => {
      notifications.show({
        color: "teal",
        title: t("apiAccess.webhookResent"),
        message: t("apiAccess.webhookResentHint"),
      });
      await queryClient.invalidateQueries({ queryKey: key });
    },
    onError: async (error) => {
      const code = codeOf(error);
      notifications.show({
        color: "red",
        title: t("apiAccess.webhookCouldNotResend"),
        message:
          code && resendRefusals.has(code) ? t(`apiAccess.webhookResendRefused_${code}`) : (error as Error).message,
      });
      // A refusal says the screen is stale — the endpoint or the delivery
      // changed meanwhile — so both are read again.
      await queryClient.invalidateQueries({ queryKey: webhooksQueryKey(integrationId) });
    },
  });
  const canResend = !archived && endpoint.status === "enabled";
  const when = (value: string) => formatters.formatDate(value, { dateStyle: "short", timeStyle: "medium" });
  const confirmResend = (delivery: WebhookDelivery) =>
    modals.openConfirmModal({
      title: t("apiAccess.webhookResendTitle"),
      children: <Text size="sm">{t("apiAccess.webhookResendBody")}</Text>,
      labels: { confirm: t("apiAccess.webhookResend"), cancel: t("common.cancel") },
      onConfirm: () => resend.mutate(delivery),
    });
  const set = (patch: WebhookDeliveryFilters) => setFilters((current) => ({ ...current, ...patch }));
  const deliveries = log.data?.pages.flatMap((page) => page.deliveries) ?? [];
  const filtered = filters.state !== undefined || filters.event !== undefined;
  // While another filter's answer loads, the previous rows show, dimmed, and
  // cannot be resent.
  const busy = log.isPlaceholderData;
  // The Event filter offers the endpoint's events, the test event and any
  // other event a loaded row has — one the endpoint no longer lists.
  const eventOptions = [
    ...new Set([
      ...endpoint.events,
      pingName,
      ...deliveries.map((delivery) => delivery.eventName),
      ...(filters.event ? [filters.event] : []),
    ]),
  ];

  let body: ReactNode;
  if (log.isPending) {
    body = <ContentSkeleton rows={4} />;
  } else if (log.isError) {
    body = (
      <Alert color="red" title={t("apiAccess.webhookDeliveriesCouldNotLoad")}>
        {log.error.message}
      </Alert>
    );
  } else if (deliveries.length === 0) {
    body = filtered ? (
      <EmptyState title={t("apiAccess.webhookDeliveriesNoneFiltered")} description="" />
    ) : (
      <EmptyState title={t("apiAccess.webhookDeliveriesNone")} description={t("apiAccess.webhookDeliveriesNoneHint")} />
    );
  } else {
    body = (
      <Stack gap="sm">
        <Table.ScrollContainer minWidth={900}>
          <Table
            data-testid="webhook-deliveries"
            data-busy={busy || undefined}
            aria-busy={busy}
            fz="sm"
            verticalSpacing="xs"
            style={busy ? { opacity: 0.5 } : undefined}
          >
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("apiAccess.webhookDeliveryCreated")}</Table.Th>
                <Table.Th>{t("apiAccess.webhookDeliveryEvent")}</Table.Th>
                <Table.Th>{t("apiAccess.status")}</Table.Th>
                <Table.Th>{t("apiAccess.webhookDeliveryAttempts")}</Table.Th>
                <Table.Th>{t("apiAccess.webhookDeliveryLast")}</Table.Th>
                <Table.Th>{t("apiAccess.webhookDeliveryCompleted")}</Table.Th>
                <Table.Th>
                  <VisuallyHidden>{t("apiAccess.actions")}</VisuallyHidden>
                </Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {deliveries.map((delivery) => (
                <Table.Tr key={delivery.id} data-delivery={delivery.id}>
                  <Table.Td>{when(delivery.createdAt)}</Table.Td>
                  <Table.Td>
                    <Group gap={4} wrap="nowrap">
                      <Code>{delivery.eventName}</Code>
                      {delivery.kind === "ping" && (
                        <Badge size="sm" variant="outline" color="gray">
                          {t("apiAccess.webhookDeliveryPing")}
                        </Badge>
                      )}
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    <Stack gap={2}>
                      <Badge color={stateColor[delivery.state]} variant="light" data-state={delivery.state}>
                        {t(`apiAccess.webhookDeliveryState_${delivery.state}`)}
                      </Badge>
                      {delivery.stateReason && (
                        <Text size="xs" c="dimmed">
                          {t(`apiAccess.webhookDeliveryReason_${delivery.stateReason}`)}
                        </Text>
                      )}
                      {delivery.state === "pending" && delivery.nextAttemptAt && (
                        <Text size="xs" c="dimmed">
                          {t("apiAccess.webhookDeliveryNext", { when: when(delivery.nextAttemptAt) })}
                        </Text>
                      )}
                    </Stack>
                  </Table.Td>
                  <Table.Td>
                    {t("apiAccess.webhookDeliveryRun", {
                      run: delivery.runAttempts,
                      max: delivery.maxRunAttempts,
                      total: delivery.totalAttempts,
                    })}
                  </Table.Td>
                  <Table.Td>
                    <Group gap={4} wrap="nowrap">
                      {delivery.lastStatus !== null && (
                        <Text span size="sm" fw={600}>
                          {delivery.lastStatus}
                        </Text>
                      )}
                      {delivery.lastError && (
                        <Badge size="sm" variant="light" color="gray">
                          {t(`apiAccess.webhookError_${delivery.lastError}`)}
                        </Badge>
                      )}
                      {delivery.lastStatus === null && !delivery.lastError && t("apiAccess.none")}
                    </Group>
                  </Table.Td>
                  <Table.Td>{delivery.completedAt ? when(delivery.completedAt) : t("apiAccess.none")}</Table.Td>
                  <Table.Td>
                    <Group gap="xs" justify="flex-end" wrap="nowrap">
                      <Button size="compact-sm" variant="subtle" onClick={() => setOpened(delivery.id)}>
                        {t("apiAccess.webhookDeliveryView")}
                      </Button>
                      {canResend && delivery.resendable && (
                        <Button
                          size="compact-sm"
                          variant="light"
                          loading={resend.isPending && resend.variables?.id === delivery.id}
                          disabled={busy || (resend.isPending && resend.variables?.id !== delivery.id)}
                          onClick={() => confirmResend(delivery)}
                        >
                          {t("apiAccess.webhookResend")}
                        </Button>
                      )}
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
        {log.hasNextPage && (
          <Group justify="center">
            <Button variant="default" loading={log.isFetchingNextPage} onClick={() => log.fetchNextPage()}>
              {t("apiAccess.webhookDeliveriesMore")}
            </Button>
          </Group>
        )}
      </Stack>
    );
  }

  return (
    <PageSection
      title={t("apiAccess.webhookDeliveries")}
      actions={
        <Button variant="subtle" leftSection={<IconArrowLeft size={16} />} onClick={onBack}>
          {t("apiAccess.webhookDeliveriesBack")}
        </Button>
      }
    >
      <Stack gap="md">
        <Stack gap={4}>
          <Code style={{ wordBreak: "break-all" }}>{endpoint.url}</Code>
          <Text size="sm" c="dimmed">
            {t("apiAccess.webhookDeliveriesHint")}
          </Text>
          {!canResend && (
            <Text size="sm" c="dimmed" data-testid="webhook-no-resend">
              {t(archived ? "apiAccess.webhookResendArchived" : "apiAccess.webhookResendDisabled")}
            </Text>
          )}
        </Stack>
        <Group align="flex-end" gap="sm" data-testid="delivery-filters">
          <Select
            label={t("apiAccess.filterStatus")}
            placeholder={t("apiAccess.filterAllStatuses")}
            clearable
            data={deliveryStates.map((value) => ({ value, label: t(`apiAccess.webhookDeliveryState_${value}`) }))}
            value={filters.state ?? null}
            onChange={(value) => set({ state: (value as WebhookDeliveryState | null) ?? undefined })}
          />
          <Select
            label={t("apiAccess.webhookDeliveryEvent")}
            placeholder={t("apiAccess.webhookFilterAllEvents")}
            clearable
            searchable
            data={eventOptions}
            value={filters.event ?? null}
            onChange={(value) => set({ event: value ?? undefined })}
          />
        </Group>
        {body}
      </Stack>
      <DeliveryDrawer
        integrationId={integrationId}
        endpointId={endpoint.id}
        deliveryId={opened}
        onClose={() => setOpened(null)}
      />
    </PageSection>
  );
};

/** A delivery's drawer: its webhook-id, the body it sends and its attempts, newest first. */
const DeliveryDrawer = ({
  integrationId,
  endpointId,
  deliveryId,
  onClose,
}: {
  integrationId: string;
  endpointId: string;
  deliveryId: string | null;
  onClose: () => void;
}) => {
  const { t, formatters } = useI18n("host");
  const detail = useQuery({
    queryKey: [...webhookDeliveriesQueryKey(integrationId, endpointId), "detail", deliveryId],
    queryFn: () => getWebhookDelivery(integrationId, endpointId, deliveryId ?? ""),
    enabled: deliveryId !== null,
  });
  const when = (value: string) => formatters.formatDate(value, { dateStyle: "short", timeStyle: "medium" });
  let content: ReactNode;
  if (detail.isPending) {
    content = <ContentSkeleton rows={4} />;
  } else if (detail.isError) {
    content = (
      <Alert color="red" title={t("apiAccess.webhookDeliveryCouldNotLoad")}>
        {detail.error.message}
      </Alert>
    );
  } else {
    const { delivery, body, attempts } = detail.data;
    content = (
      <Stack gap="md" data-testid="webhook-delivery-detail">
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            {t("apiAccess.webhookDeliveryMessageId")}
          </Text>
          <Code>{delivery.messageId}</Code>
          <Text size="xs" c="dimmed">
            {t("apiAccess.webhookDeliveryMessageIdHint")}
          </Text>
        </Stack>
        <Group gap="xs">
          <Code>{delivery.eventName}</Code>
          <Badge color={stateColor[delivery.state]} variant="light">
            {t(`apiAccess.webhookDeliveryState_${delivery.state}`)}
          </Badge>
          {delivery.stateReason && (
            <Text size="sm" c="dimmed">
              {t(`apiAccess.webhookDeliveryReason_${delivery.stateReason}`)}
            </Text>
          )}
        </Group>
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            {t("apiAccess.webhookDeliveryBody")}
          </Text>
          <PlainText value={body} testId="webhook-delivery-body" />
        </Stack>
        <Stack gap="xs">
          <Text size="sm" fw={500}>
            {t("apiAccess.webhookDeliveryAttemptsTitle")}
          </Text>
          {attempts.length === 0 ? (
            <Text size="sm" c="dimmed">
              {t("apiAccess.webhookDeliveryNoAttempts")}
            </Text>
          ) : (
            attempts.map((attempt, index) => (
              <Stack
                key={`${attempt.at}-${index}`}
                gap={4}
                data-testid="webhook-attempt"
                style={{ borderTop: "1px solid var(--mantine-color-default-border)", paddingTop: 8 }}
              >
                <Group gap="xs">
                  <Text size="sm">{when(attempt.at)}</Text>
                  {attempt.statusCode !== null && (
                    <Text size="sm" fw={600}>
                      {attempt.statusCode}
                    </Text>
                  )}
                  {attempt.error && (
                    <Badge size="sm" variant="light" color="gray">
                      {t(`apiAccess.webhookError_${attempt.error}`)}
                    </Badge>
                  )}
                  <Text size="xs" c="dimmed">
                    {t("apiAccess.durationMs", { ms: attempt.durationMs })}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {t(`apiAccess.webhookSecretUsed_${attempt.secretUsed}`)}
                  </Text>
                </Group>
                {attempt.excerpt !== null && (
                  <Stack gap={2}>
                    <Text size="xs" c="dimmed">
                      {t("apiAccess.webhookDeliveryExcerpt")}
                    </Text>
                    <PlainText value={attempt.excerpt} testId="webhook-attempt-excerpt" />
                  </Stack>
                )}
              </Stack>
            ))
          )}
        </Stack>
      </Stack>
    );
  }
  return (
    <Drawer
      opened={deliveryId !== null}
      onClose={onClose}
      position="right"
      size="lg"
      title={t("apiAccess.webhookDelivery")}
    >
      {deliveryId !== null && content}
    </Drawer>
  );
};
