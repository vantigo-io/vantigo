import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Code,
  Group,
  Menu,
  Modal,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  VisuallyHidden,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconDots, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageSection, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  createWebhook,
  deleteWebhook,
  disableWebhook,
  enableWebhook,
  listWebhookEvents,
  listWebhooks,
  testWebhook,
  updateWebhook,
  type WebhookEndpoint,
  type WebhookEndpointSecret,
  type WebhookEvent,
  webhookEventsQueryKey,
  webhooksQueryKey,
} from "../../api/webhooks";
import { translateHostPermissionModule } from "../../i18n";
import { codeOf, fieldErrorsOf } from "./-api-access-shared";
import { WebhookDeliveries } from "./-api-access-webhook-deliveries";
import { RollWebhookSecretDialog, WebhookSecretDialog } from "./-webhook-secret-dialog";

/** The most endpoints an integration may have (outgoing webhooks design W1). */
const maxEndpoints = 5;
/** How many of an endpoint's events its row names before "+n more". */
const eventsShown = 3;

/**
 * What an endpoint's badge says (W18): enabled, enabled but failing since
 * its last success, or disabled.
 */
type EndpointStatus = "enabled" | "failing" | "disabled";
const statusOf = (endpoint: WebhookEndpoint): EndpointStatus =>
  endpoint.status === "disabled" ? "disabled" : endpoint.failingSince ? "failing" : "enabled";
const statusColor: Record<EndpointStatus, string> = { enabled: "teal", failing: "yellow", disabled: "orange" };

/** The URL's host, as a banner names an endpoint; the URL itself when it does not parse. */
const hostOf = (url: string) => {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
};

type EndpointDraft = { url: string; description: string; events: string[] };

const emptyDraft: EndpointDraft = { url: "", description: "", events: [] };

/** The form being shown: a new endpoint, or the one being edited. */
type FormState = { endpoint: WebhookEndpoint | null; draft: EndpointDraft } | null;

/**
 * The event picker (W6, W7), grouped by module: only what the integration's
 * roles let it receive is offered, and an event the endpoint lists that its
 * roles no longer allow is shown, marked, so it can be taken off.
 */
const EventPicker = ({
  events,
  value,
  onChange,
  error,
}: {
  events: readonly WebhookEvent[];
  value: string[];
  onChange: (events: string[]) => void;
  error?: string;
}) => {
  const { t } = useI18n("host");
  const shown = events.filter((event) => event.permitted || value.includes(event.name));
  const groups = new Map<string, WebhookEvent[]>();
  for (const event of shown) {
    const group = translateHostPermissionModule(event.permissions[0] ?? "", event.module, t);
    groups.set(group, [...(groups.get(group) ?? []), event]);
  }
  const toggle = (name: string, checked: boolean) =>
    onChange(checked ? [...value, name] : value.filter((one) => one !== name));
  return (
    <Stack gap="sm">
      <Stack gap={2}>
        <Text size="sm" fw={500}>
          {t("apiAccess.webhookEvents")}
        </Text>
        <Text size="xs" c="dimmed">
          {t("apiAccess.webhookEventsHint")}
        </Text>
      </Stack>
      {shown.length === 0 && (
        <Alert color="gray" data-testid="webhook-no-events">
          {t("apiAccess.webhookEventsNone")}
        </Alert>
      )}
      {[...groups.entries()].map(([group, items]) => (
        <Stack key={group} gap="xs" data-group={group}>
          <Text fw={600}>{group}</Text>
          {items.map((event) => (
            <Checkbox
              key={event.name}
              label={
                <Group gap="xs">
                  <Code>{event.name}</Code>
                  {!event.permitted && (
                    <Badge color="yellow" variant="outline">
                      {t("apiAccess.webhookEventNotPermitted")}
                    </Badge>
                  )}
                </Group>
              }
              checked={value.includes(event.name)}
              onChange={(change) => toggle(event.name, change.currentTarget.checked)}
            />
          ))}
        </Stack>
      ))}
      {error && (
        <Text size="sm" c="red">
          {error}
        </Text>
      )}
    </Stack>
  );
};

/**
 * An integration's Webhooks tab (W1, W8-W10): its endpoints with their full
 * URLs, which only Owners see, their events and status; to create, edit,
 * enable, disable, delete and roll the secret of. A new or rolled secret is
 * shown once, in a dialog that forgets it when closed. An archived
 * integration's endpoints are shown and only deleted.
 */
export const IntegrationWebhooks = ({ integrationId, archived }: { integrationId: string; archived: boolean }) => {
  const { t, formatters } = useI18n("host");
  const queryClient = useQueryClient();
  const endpoints = useQuery({ queryKey: webhooksQueryKey(integrationId), queryFn: () => listWebhooks(integrationId) });
  const catalogue = useQuery({
    queryKey: webhookEventsQueryKey(integrationId),
    queryFn: () => listWebhookEvents(integrationId),
  });
  const [form, setForm] = useState<FormState>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [secret, setSecret] = useState<WebhookEndpointSecret | null>(null);
  const [rolling, setRolling] = useState<WebhookEndpoint | null>(null);
  // The endpoint whose delivery log is shown in place of the list (W23).
  const [logOf, setLogOf] = useState<string | null>(null);

  const refresh = () => queryClient.invalidateQueries({ queryKey: webhooksQueryKey(integrationId) });
  const failed = (title: string) => (error: unknown) =>
    notifications.show({ color: "red", title, message: (error as Error).message });

  const save = useMutation({
    gcTime: 0,
    mutationFn: async ({ endpoint, draft }: NonNullable<FormState>) => {
      const body = {
        url: draft.url.trim(),
        events: draft.events,
        ...(draft.description.trim() ? { description: draft.description.trim() } : {}),
      };
      if (endpoint) {
        await updateWebhook(integrationId, endpoint.id, endpoint.version, body);
        return null;
      }
      return createWebhook(integrationId, body);
    },
    onSuccess: async (made) => {
      setForm(null);
      setErrors({});
      if (made) setSecret(made);
      else notifications.show({ color: "teal", title: t("apiAccess.webhookSaved"), message: "" });
      await refresh();
    },
    onError: async (error) => {
      if (codeOf(error) === "version_mismatch") {
        setForm(null);
        notifications.show({
          color: "orange",
          title: t("apiAccess.couldNotSave"),
          message: t("apiAccess.webhookChangedMeanwhile"),
        });
        await refresh();
        return;
      }
      setErrors(fieldErrorsOf(error));
      failed(t("apiAccess.couldNotSave"))(error);
    },
  });
  const lifecycle = useMutation({
    mutationFn: ({ endpoint, action }: { endpoint: WebhookEndpoint; action: "enable" | "disable" }) =>
      action === "enable" ? enableWebhook(integrationId, endpoint.id) : disableWebhook(integrationId, endpoint.id),
    onSuccess: async (_, { action }) => {
      notifications.show({ color: "teal", title: t(`apiAccess.webhookDone_${action}`), message: "" });
      await refresh();
    },
    onError: failed(t("apiAccess.couldNotSave")),
  });
  const test = useMutation({
    mutationFn: (endpoint: WebhookEndpoint) => testWebhook(integrationId, endpoint.id),
    onSuccess: () =>
      notifications.show({
        color: "teal",
        title: t("apiAccess.webhookTestSent"),
        message: t("apiAccess.webhookTestSentHint"),
      }),
    onError: failed(t("apiAccess.webhookCouldNotTest")),
  });
  const remove = useMutation({
    mutationFn: (endpoint: WebhookEndpoint) => deleteWebhook(integrationId, endpoint.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("apiAccess.webhookDeleted"), message: "" });
      await refresh();
    },
    onError: failed(t("apiAccess.couldNotSave")),
  });

  if (endpoints.isPending) return <ContentSkeleton rows={3} />;
  if (endpoints.isError)
    return (
      <Alert color="red" title={t("apiAccess.webhookCouldNotLoad")}>
        {endpoints.error.message}
      </Alert>
    );

  const list = endpoints.data;
  const logged = list.find((endpoint) => endpoint.id === logOf);
  if (logged)
    return (
      <WebhookDeliveries
        integrationId={integrationId}
        endpoint={logged}
        archived={archived}
        onBack={() => setLogOf(null)}
      />
    );
  const full = list.length >= maxEndpoints;
  const when = (value: string) => formatters.formatDate(value, { dateStyle: "medium", timeStyle: "short" });
  const confirmDisable = (endpoint: WebhookEndpoint) =>
    modals.openConfirmModal({
      title: t("apiAccess.webhookDisableTitle"),
      children: <Text size="sm">{t("apiAccess.webhookDisableBody")}</Text>,
      labels: { confirm: t("apiAccess.webhookDisable"), cancel: t("common.cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => lifecycle.mutate({ endpoint, action: "disable" }),
    });
  const confirmDelete = (endpoint: WebhookEndpoint) =>
    modals.openConfirmModal({
      title: t("apiAccess.webhookDeleteTitle"),
      children: <Text size="sm">{t("apiAccess.webhookDeleteBody")}</Text>,
      labels: { confirm: t("apiAccess.webhookDelete"), cancel: t("common.cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(endpoint),
    });
  const open = (endpoint: WebhookEndpoint | null) => {
    setErrors({});
    setForm({
      endpoint,
      draft: endpoint
        ? { url: endpoint.url, description: endpoint.description ?? "", events: endpoint.events }
        : emptyDraft,
    });
  };

  return (
    <>
      <PageSection
        title={t("apiAccess.webhooks")}
        actions={
          !archived && (
            <Button leftSection={<IconPlus size={16} />} disabled={full} onClick={() => open(null)}>
              {t("apiAccess.webhookNew")}
            </Button>
          )
        }
      >
        <Text size="sm" c="dimmed" mb="sm">
          {t("apiAccess.webhooksHint")}
        </Text>
        {full && !archived && (
          <Text size="sm" c="dimmed" mb="sm">
            {t("apiAccess.webhookLimit")}
          </Text>
        )}
        {list
          .filter((endpoint) => endpoint.disabledReason === "failing" || endpoint.disabledReason === "gone")
          .map((endpoint) => (
            <Alert
              key={endpoint.id}
              color="orange"
              mb="sm"
              title={t("apiAccess.webhookBannerTitle", { host: hostOf(endpoint.url) })}
              data-testid="webhook-disabled-banner"
              data-reason={endpoint.disabledReason}
            >
              <Stack gap="xs" align="flex-start">
                <Text size="sm">{t(`apiAccess.webhookBanner_${endpoint.disabledReason}`)}</Text>
                {!archived && (
                  <Button
                    size="compact-sm"
                    variant="light"
                    loading={lifecycle.isPending && lifecycle.variables?.endpoint.id === endpoint.id}
                    onClick={() => lifecycle.mutate({ endpoint, action: "enable" })}
                  >
                    {t("apiAccess.webhookEnable")}
                  </Button>
                )}
              </Stack>
            </Alert>
          ))}
        {list.length === 0 ? (
          <EmptyState title={t("apiAccess.webhookNone")} description={t("apiAccess.webhookNoneHint")} />
        ) : (
          <Table.ScrollContainer minWidth={820}>
            <Table verticalSpacing="sm" data-testid="webhook-endpoints">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("apiAccess.webhookUrl")}</Table.Th>
                  <Table.Th>{t("apiAccess.webhookEvents")}</Table.Th>
                  <Table.Th>{t("apiAccess.status")}</Table.Th>
                  <Table.Th>
                    <VisuallyHidden>{t("apiAccess.actions")}</VisuallyHidden>
                  </Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {list.map((endpoint) => (
                  <Table.Tr key={endpoint.id} data-endpoint={endpoint.id}>
                    <Table.Td>
                      <Stack gap={4}>
                        <Code style={{ wordBreak: "break-all" }}>{endpoint.url}</Code>
                        {endpoint.description && (
                          <Text size="sm" c="dimmed">
                            {endpoint.description}
                          </Text>
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      <Stack gap={2}>
                        {endpoint.events.slice(0, eventsShown).map((name) => (
                          <Code key={name}>{name}</Code>
                        ))}
                        {endpoint.events.length > eventsShown && (
                          <Text size="xs" c="dimmed">
                            {t("apiAccess.webhookMoreEvents", { count: endpoint.events.length - eventsShown })}
                          </Text>
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      <Stack gap={4}>
                        <Badge color={statusColor[statusOf(endpoint)]} variant="light" data-status={statusOf(endpoint)}>
                          {t(`apiAccess.webhookStatus_${statusOf(endpoint)}`)}
                        </Badge>
                        {endpoint.disabledReason && (
                          <Text size="xs">{t(`apiAccess.webhookReason_${endpoint.disabledReason}`)}</Text>
                        )}
                        {endpoint.status === "enabled" && endpoint.failingSince && (
                          <Text size="xs" c="orange">
                            {t("apiAccess.webhookFailingSince", { when: when(endpoint.failingSince) })}
                          </Text>
                        )}
                        {endpoint.lastSuccessAt && (
                          <Text size="xs" c="dimmed">
                            {t("apiAccess.webhookLastSuccess", { when: when(endpoint.lastSuccessAt) })}
                          </Text>
                        )}
                        {endpoint.previousSecretExpiresAt && (
                          <Text size="xs" c="dimmed">
                            {t("apiAccess.webhookPreviousSecret", { when: when(endpoint.previousSecretExpiresAt) })}
                          </Text>
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td>
                      <Group justify="flex-end" gap="xs" wrap="nowrap">
                        <Button size="compact-sm" variant="light" onClick={() => setLogOf(endpoint.id)}>
                          {t("apiAccess.webhookDeliveriesOpen")}
                        </Button>
                        <Menu position="bottom-end">
                          <Menu.Target>
                            <Button
                              size="compact-sm"
                              variant="subtle"
                              aria-label={t("apiAccess.actions")}
                              rightSection={<IconDots size={14} />}
                            >
                              {t("apiAccess.actions")}
                            </Button>
                          </Menu.Target>
                          <Menu.Dropdown>
                            {!archived && endpoint.status === "enabled" && (
                              <Menu.Item onClick={() => test.mutate(endpoint)}>{t("apiAccess.webhookTest")}</Menu.Item>
                            )}
                            {!archived && (
                              <Menu.Item onClick={() => open(endpoint)}>{t("apiAccess.webhookEdit")}</Menu.Item>
                            )}
                            {!archived && (
                              <Menu.Item onClick={() => setRolling(endpoint)}>{t("apiAccess.webhookRoll")}</Menu.Item>
                            )}
                            {!archived && endpoint.status === "disabled" && (
                              <Menu.Item onClick={() => lifecycle.mutate({ endpoint, action: "enable" })}>
                                {t("apiAccess.webhookEnable")}
                              </Menu.Item>
                            )}
                            {endpoint.status === "enabled" && (
                              <Menu.Item onClick={() => confirmDisable(endpoint)}>
                                {t("apiAccess.webhookDisable")}
                              </Menu.Item>
                            )}
                            <Menu.Item color="red" onClick={() => confirmDelete(endpoint)}>
                              {t("apiAccess.webhookDelete")}
                            </Menu.Item>
                          </Menu.Dropdown>
                        </Menu>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </PageSection>

      <Modal
        opened={form !== null}
        onClose={() => setForm(null)}
        title={form?.endpoint ? t("apiAccess.webhookEdit") : t("apiAccess.webhookNew")}
        size="lg"
        centered
      >
        {form && (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              save.mutate(form);
            }}
          >
            <Stack>
              <TextInput
                label={t("apiAccess.webhookUrl")}
                description={t("apiAccess.webhookUrlHint")}
                required
                maxLength={2048}
                value={form.draft.url}
                error={errors.url}
                onChange={(event) => setForm({ ...form, draft: { ...form.draft, url: event.currentTarget.value } })}
              />
              <Textarea
                label={t("apiAccess.webhookDescription")}
                autosize
                minRows={2}
                maxLength={500}
                value={form.draft.description}
                error={errors.description}
                onChange={(event) =>
                  setForm({ ...form, draft: { ...form.draft, description: event.currentTarget.value } })
                }
              />
              {catalogue.isPending ? (
                <ContentSkeleton rows={3} />
              ) : catalogue.isError ? (
                <Alert color="red" title={t("apiAccess.webhookEventsCouldNotLoad")} data-testid="webhook-events-error">
                  {catalogue.error.message}
                </Alert>
              ) : (
                <EventPicker
                  events={catalogue.data}
                  value={form.draft.events}
                  error={errors.events}
                  onChange={(events) => setForm({ ...form, draft: { ...form.draft, events } })}
                />
              )}
              <Button
                type="submit"
                loading={save.isPending}
                disabled={!form.draft.url.trim() || form.draft.events.length === 0}
              >
                {form.endpoint ? t("common.save") : t("apiAccess.webhookCreate")}
              </Button>
            </Stack>
          </form>
        )}
      </Modal>
      <WebhookSecretDialog
        secret={secret}
        onClose={() => {
          setSecret(null);
          save.reset();
        }}
      />
      <RollWebhookSecretDialog endpoint={rolling} integrationId={integrationId} onClose={() => setRolling(null)} />
    </>
  );
};
