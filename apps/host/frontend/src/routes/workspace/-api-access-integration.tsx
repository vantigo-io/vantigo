import {
  Alert,
  Badge,
  Button,
  Code,
  Group,
  Menu,
  Modal,
  NumberInput,
  Select,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  VisuallyHidden,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconDots, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader, PageSection, PageTabs, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  listAuthorizationRoles,
  listAuthorizationUsers,
  listPermissionCatalog,
  type PermissionDescriptor,
} from "../../api/authorization";
import {
  type ApiKey,
  archiveIntegration,
  disableIntegration,
  enableIntegration,
  getIntegration,
  type IntegrationDetail,
  integrationQueryKey,
  integrationsQueryKey,
  revokeApiKey,
  setIntegrationRoles,
  updateIntegration,
} from "../../api/integrations";
import { CustomRolePicker } from "../../components/custom-role-picker";
import { translateHostPermissionKey, translateHostPermissionModule } from "../../i18n";
import { IntegrationMcp } from "./-api-access-mcp";
import { IntegrationRequests } from "./-api-access-requests";
import { codeOf, contactOptions, fieldErrorsOf, integrationStatusColor, keyStatusColor } from "./-api-access-shared";
import { IntegrationWebhooks } from "./-api-access-webhooks";
import { NewKeyDialog, RollKeyDialog } from "./-api-key-dialog";

type DetailsDraft = {
  name: string;
  description: string;
  contactUserId: string | null;
  // What the field holds while it is typed in; read as a number on submit.
  rateLimitPerMinute: number | string;
};

/** The roles of an integration as ids, in a stable order. */
const roleIdsOf = (detail: IntegrationDetail) => detail.integration.roles.map((role) => role.id).sort();
const sameIds = (a: string[], b: string[]) => a.length === b.length && [...a].sort().join() === [...b].sort().join();

/**
 * An integration's page (API access design K22): its details, its roles and
 * the permissions they give it — those no API key ever holds marked, the
 * sensitive ones badged — and its keys, each with its narrowing, read-only
 * flag, networks and last use, to make, roll and revoke. An archived
 * integration is shown and not changed. Its Requests tab is the request log
 * (K23, -api-access-requests.tsx), its Webhooks tab its webhook endpoints
 * (outgoing webhooks design W1, -api-access-webhooks.tsx). Its Overview holds
 * the MCP section (MCP server design M16, -api-access-mcp.tsx).
 */
export type IntegrationTab = "overview" | "requests" | "webhooks";

export const IntegrationPage = ({
  integrationId,
  tab = "overview",
  onTab,
}: {
  integrationId: string;
  /** The view, from the URL's tab search param; the overview when absent. */
  tab?: IntegrationTab;
  onTab?: (tab: IntegrationTab) => void;
}) => {
  const { t, formatters } = useI18n("host");
  const queryClient = useQueryClient();
  const detail = useQuery({
    queryKey: integrationQueryKey(integrationId),
    queryFn: () => getIntegration(integrationId),
  });
  const catalog = useQuery({ queryKey: ["authorization", "catalog"], queryFn: listPermissionCatalog });
  const roles = useQuery({ queryKey: ["authorization", "roles"], queryFn: listAuthorizationRoles });
  const users = useQuery({ queryKey: ["authorization", "users"], queryFn: listAuthorizationUsers });
  const [details, setDetails] = useState<DetailsDraft | null>(null);
  const [detailErrors, setDetailErrors] = useState<Record<string, string>>({});
  // The roles being chosen; null while they are the integration's own.
  const [roleIds, setRoleIds] = useState<string[] | null>(null);
  const [newKey, setNewKey] = useState(false);
  const [rolling, setRolling] = useState<ApiKey | null>(null);

  const refreshed = async (next?: IntegrationDetail) => {
    if (next) queryClient.setQueryData(integrationQueryKey(integrationId), next);
    await queryClient.invalidateQueries({ queryKey: integrationsQueryKey });
  };
  const failed = (title: string) => (error: unknown) =>
    notifications.show({ color: "red", title, message: (error as Error).message });

  const saveDetails = useMutation({
    mutationFn: ({ draft, version }: { draft: DetailsDraft; version: string }) =>
      updateIntegration(integrationId, version, {
        name: draft.name.trim(),
        ...(draft.description.trim() ? { description: draft.description.trim() } : {}),
        ...(draft.contactUserId ? { contactUserId: draft.contactUserId } : {}),
        rateLimitPerMinute: Number(draft.rateLimitPerMinute),
      }),
    onSuccess: async (next) => {
      setDetails(null);
      notifications.show({ color: "teal", title: t("apiAccess.saved"), message: "" });
      await refreshed(next);
    },
    onError: async (error) => {
      if (codeOf(error) === "version_mismatch") {
        setDetails(null);
        notifications.show({
          color: "orange",
          title: t("apiAccess.couldNotSave"),
          message: t("apiAccess.changedMeanwhile"),
        });
        await queryClient.invalidateQueries({ queryKey: integrationQueryKey(integrationId) });
        return;
      }
      setDetailErrors(fieldErrorsOf(error));
      failed(t("apiAccess.couldNotSave"))(error);
    },
  });
  const saveRoles = useMutation({
    mutationFn: (ids: string[]) => setIntegrationRoles(integrationId, ids),
    onSuccess: async (next) => {
      setRoleIds(null);
      notifications.show({ color: "teal", title: t("apiAccess.rolesSaved"), message: "" });
      await refreshed(next);
    },
    onError: failed(t("apiAccess.couldNotSave")),
  });
  const lifecycle = useMutation({
    mutationFn: (action: "disable" | "enable" | "archive") =>
      action === "disable"
        ? disableIntegration(integrationId)
        : action === "enable"
          ? enableIntegration(integrationId)
          : archiveIntegration(integrationId),
    onSuccess: async (next, action) => {
      notifications.show({ color: "teal", title: t(`apiAccess.done_${action}`), message: "" });
      await refreshed(next);
    },
    onError: failed(t("apiAccess.couldNotSave")),
  });
  const revoke = useMutation({
    mutationFn: (key: ApiKey) => revokeApiKey(integrationId, key.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("apiAccess.revoked"), message: "" });
      await queryClient.invalidateQueries({ queryKey: integrationQueryKey(integrationId) });
      await queryClient.invalidateQueries({ queryKey: integrationsQueryKey, exact: true });
    },
    onError: failed(t("apiAccess.couldNotRevoke")),
  });

  if (detail.isPending) return <ContentSkeleton rows={5} />;
  if (detail.isError)
    return (
      <Alert color="red" title={t("apiAccess.couldNotLoad")}>
        {detail.error.message}
      </Alert>
    );

  const { integration, keys, effectivePermissions } = detail.data;
  const archived = integration.status === "archived";
  const descriptors = new Map<string, PermissionDescriptor>((catalog.data ?? []).map((one) => [one.key, one]));
  const chosenRoles = roleIds ?? roleIdsOf(detail.data);
  const sensitiveHeld = effectivePermissions.some(
    (permission) => !permission.apiNever && descriptors.get(permission.key)?.sensitive,
  );
  const rolled = new Set(keys.map((key) => key.rolledFrom).filter(Boolean));
  const when = (value: string | null, withTime = false) =>
    value
      ? formatters.formatDate(value, withTime ? { dateStyle: "medium", timeStyle: "short" } : { dateStyle: "medium" })
      : t("apiAccess.none");
  const permissionName = (key: string) => translateHostPermissionKey(key, t, descriptors.get(key)?.displayName);

  const confirm = (action: "disable" | "archive") =>
    modals.openConfirmModal({
      title: t(`apiAccess.confirm_${action}`, { name: integration.name }),
      children: <Text size="sm">{t(`apiAccess.confirmBody_${action}`)}</Text>,
      labels: { confirm: t(`apiAccess.action_${action}`), cancel: t("common.cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => lifecycle.mutate(action),
    });
  const confirmRevoke = (key: ApiKey) =>
    modals.openConfirmModal({
      title: t("apiAccess.revokeTitle", { name: key.name }),
      children: <Text size="sm">{t("apiAccess.revokeBody")}</Text>,
      labels: { confirm: t("apiAccess.revoke"), cancel: t("common.cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => revoke.mutate(key),
    });
  const editDetails = () => {
    setDetailErrors({});
    setDetails({
      name: integration.name,
      description: integration.description ?? "",
      contactUserId: integration.contactUserId,
      rateLimitPerMinute: integration.rateLimitPerMinute,
    });
  };

  return (
    <Stack gap="xl">
      <PageHeader
        breadcrumbs={[{ label: t("apiAccess.title"), to: "/workspace/api-access" }, { label: integration.name }]}
        title={
          <Group gap="sm">
            <span>{integration.name}</span>
            <Badge color={integrationStatusColor[integration.status]} variant="light">
              {t(`apiAccess.status_${integration.status}`)}
            </Badge>
          </Group>
        }
        description={integration.description ?? undefined}
        actions={
          !archived && (
            <Menu position="bottom-end">
              <Menu.Target>
                <Button variant="default" rightSection={<IconDots size={16} />}>
                  {t("apiAccess.actions")}
                </Button>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item onClick={editDetails}>{t("apiAccess.editDetails")}</Menu.Item>
                {integration.status === "disabled" ? (
                  <Menu.Item onClick={() => lifecycle.mutate("enable")}>{t("apiAccess.action_enable")}</Menu.Item>
                ) : (
                  <Menu.Item onClick={() => confirm("disable")}>{t("apiAccess.action_disable")}</Menu.Item>
                )}
                <Menu.Item color="red" onClick={() => confirm("archive")}>
                  {t("apiAccess.action_archive")}
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>
          )
        }
      />
      {archived && (
        <Alert color="gray" title={t("apiAccess.archivedTitle")}>
          {t("apiAccess.archivedBody", { when: when(integration.archivedAt) })}
        </Alert>
      )}
      {integration.status === "disabled" && (
        <Alert color="orange" title={t("apiAccess.disabledTitle")}>
          {t("apiAccess.disabledBody")}
        </Alert>
      )}
      <PageTabs
        aria-label={integration.name}
        items={[
          { value: "overview", label: t("apiAccess.tabOverview") },
          { value: "requests", label: t("apiAccess.tabRequests") },
          { value: "webhooks", label: t("apiAccess.tabWebhooks") },
        ]}
        value={tab}
        onChange={(next) => onTab?.(next)}
      />

      {tab === "requests" ? (
        <IntegrationRequests integrationId={integrationId} keys={keys} />
      ) : tab === "webhooks" ? (
        <IntegrationWebhooks integrationId={integrationId} archived={archived} />
      ) : (
        <>
          <PageSection title={t("apiAccess.details")}>
            <Table variant="vertical" layout="fixed" withTableBorder={false}>
              <Table.Tbody>
                <Table.Tr>
                  <Table.Th w={220}>{t("apiAccess.contact")}</Table.Th>
                  <Table.Td>{integration.contactDisplayName ?? t("apiAccess.noContact")}</Table.Td>
                </Table.Tr>
                <Table.Tr>
                  <Table.Th>{t("apiAccess.rateLimit")}</Table.Th>
                  <Table.Td>{t("apiAccess.perMinute", { rate: integration.rateLimitPerMinute })}</Table.Td>
                </Table.Tr>
                <Table.Tr>
                  <Table.Th>{t("apiAccess.createdAt")}</Table.Th>
                  <Table.Td>{when(integration.createdAt, true)}</Table.Td>
                </Table.Tr>
                <Table.Tr>
                  <Table.Th>{t("apiAccess.lastUsed")}</Table.Th>
                  <Table.Td>
                    {integration.lastUsedAt ? when(integration.lastUsedAt, true) : t("apiAccess.neverUsed")}
                  </Table.Td>
                </Table.Tr>
              </Table.Tbody>
            </Table>
          </PageSection>

          <PageSection title={t("apiAccess.roles")}>
            <Stack>
              <CustomRolePicker
                label={t("apiAccess.roles")}
                description={t("apiAccess.rolesHint")}
                roles={roles.data ?? []}
                value={chosenRoles}
                disabled={archived || saveRoles.isPending}
                onChange={setRoleIds}
              />
              {roleIds !== null && !sameIds(roleIds, roleIdsOf(detail.data)) && (
                <Group>
                  <Button loading={saveRoles.isPending} onClick={() => saveRoles.mutate(roleIds)}>
                    {t("apiAccess.saveRoles")}
                  </Button>
                  <Button variant="subtle" onClick={() => setRoleIds(null)}>
                    {t("common.cancel")}
                  </Button>
                </Group>
              )}
              <Text fw={600}>{t("apiAccess.effectivePermissions")}</Text>
              {sensitiveHeld && (
                <Alert color="orange" icon={<IconAlertTriangle size={16} />}>
                  {t("apiAccess.sensitiveWarning")}
                </Alert>
              )}
              {effectivePermissions.length === 0 ? (
                <Text size="sm" c="dimmed">
                  {t("apiAccess.noPermissions")}
                </Text>
              ) : (
                <Table data-testid="effective-permissions">
                  <Table.Tbody>
                    {effectivePermissions.map((permission) => {
                      const descriptor = descriptors.get(permission.key);
                      return (
                        <Table.Tr key={permission.key} data-permission={permission.key}>
                          <Table.Td>
                            <Text size="sm" td={permission.apiNever ? "line-through" : undefined}>
                              {permissionName(permission.key)}
                            </Text>
                          </Table.Td>
                          <Table.Td>
                            <Text size="sm" c="dimmed">
                              {translateHostPermissionModule(
                                permission.key,
                                descriptor?.module ?? permission.key.split(":")[0],
                                t,
                              )}
                            </Text>
                          </Table.Td>
                          <Table.Td>
                            <Group gap={4} justify="flex-end">
                              {descriptor?.sensitive && <Badge color="orange">{t("apiAccess.sensitive")}</Badge>}
                              {permission.apiNever && <Badge color="gray">{t("apiAccess.apiNever")}</Badge>}
                            </Group>
                          </Table.Td>
                        </Table.Tr>
                      );
                    })}
                  </Table.Tbody>
                </Table>
              )}
            </Stack>
          </PageSection>

          <PageSection
            title={t("apiAccess.keys")}
            actions={
              !archived && (
                <Button leftSection={<IconPlus size={16} />} onClick={() => setNewKey(true)}>
                  {t("apiAccess.newKey")}
                </Button>
              )
            }
          >
            {keys.length === 0 ? (
              <EmptyState title={t("apiAccess.noKeys")} description={t("apiAccess.noKeysHint")} />
            ) : (
              <Table.ScrollContainer minWidth={960}>
                <Table verticalSpacing="sm">
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("apiAccess.keyName")}</Table.Th>
                      <Table.Th>{t("apiAccess.expiry")}</Table.Th>
                      <Table.Th>{t("apiAccess.narrowing")}</Table.Th>
                      <Table.Th>{t("apiAccess.networks")}</Table.Th>
                      <Table.Th>{t("apiAccess.lastUsed")}</Table.Th>
                      <Table.Th>
                        <VisuallyHidden>{t("apiAccess.actions")}</VisuallyHidden>
                      </Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {keys.map((key) => (
                      <Table.Tr key={key.id} data-key={key.id}>
                        <Table.Td>
                          <Stack gap={4}>
                            <Text fw={600}>{key.name}</Text>
                            <Code>{key.display}</Code>
                            <Group gap={4}>
                              <Badge color={keyStatusColor[key.status]} variant="light">
                                {t(`apiAccess.keyStatus_${key.status}`)}
                              </Badge>
                              {key.readOnly && <Badge variant="outline">{t("apiAccess.readOnly")}</Badge>}
                              {rolled.has(key.id) && key.status === "active" && (
                                <Badge color="blue" variant="outline">
                                  {t("apiAccess.rolledBadge")}
                                </Badge>
                              )}
                            </Group>
                          </Stack>
                        </Table.Td>
                        <Table.Td>{when(key.expiresAt, true)}</Table.Td>
                        <Table.Td>
                          {key.permissions === null ? (
                            <Text size="sm">{t("apiAccess.notNarrowed")}</Text>
                          ) : (
                            <Stack gap={2}>
                              {key.permissions.length === 0 && (
                                <Text size="sm">{t("apiAccess.narrowedToNothing")}</Text>
                              )}
                              {key.permissions.map((entry) => (
                                <Group key={entry.key} gap={4} wrap="nowrap" data-narrowing={entry.key}>
                                  <Text
                                    size="sm"
                                    td={entry.inCatalog ? undefined : "line-through"}
                                    title={entry.inCatalog ? undefined : t("apiAccess.removedFromCatalog")}
                                  >
                                    {entry.inCatalog ? permissionName(entry.key) : entry.key}
                                  </Text>
                                  {entry.inCatalog && descriptors.get(entry.key)?.sensitive && (
                                    <Badge size="xs" color="orange">
                                      {t("apiAccess.sensitive")}
                                    </Badge>
                                  )}
                                  {entry.apiNever ? (
                                    <Badge size="xs" color="gray" variant="outline">
                                      {t("apiAccess.apiNever")}
                                    </Badge>
                                  ) : (
                                    entry.inCatalog &&
                                    !entry.grantedByRoles && (
                                      <Badge size="xs" color="yellow" variant="outline">
                                        {t("apiAccess.notGranted")}
                                      </Badge>
                                    )
                                  )}
                                </Group>
                              ))}
                            </Stack>
                          )}
                        </Table.Td>
                        <Table.Td>
                          {key.allowedNetworks.length === 0 ? (
                            <Text size="sm">{t("apiAccess.anywhere")}</Text>
                          ) : (
                            <Stack gap={2}>
                              {key.allowedNetworks.map((network) => (
                                <Code key={network}>{network}</Code>
                              ))}
                            </Stack>
                          )}
                        </Table.Td>
                        <Table.Td>
                          {key.lastUsedAt
                            ? `${when(key.lastUsedAt, true)}${key.lastUsedIp ? ` · ${key.lastUsedIp}` : ""}`
                            : t("apiAccess.neverUsed")}
                        </Table.Td>
                        <Table.Td>
                          {!archived && key.status === "active" && (
                            <Group gap={4} wrap="nowrap" justify="flex-end">
                              {!rolled.has(key.id) && (
                                <Button size="compact-sm" variant="subtle" onClick={() => setRolling(key)}>
                                  {t("apiAccess.roll")}
                                </Button>
                              )}
                              <Button size="compact-sm" variant="subtle" color="red" onClick={() => confirmRevoke(key)}>
                                {t("apiAccess.revoke")}
                              </Button>
                            </Group>
                          )}
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            )}
          </PageSection>

          <IntegrationMcp detail={detail.data} archived={archived} />
        </>
      )}

      <NewKeyDialog
        opened={newKey}
        onClose={() => setNewKey(false)}
        detail={detail.data}
        catalog={catalog.data ?? []}
      />
      <RollKeyDialog apiKey={rolling} integrationId={integrationId} onClose={() => setRolling(null)} />
      <Modal opened={details !== null} onClose={() => setDetails(null)} title={t("apiAccess.editDetails")} centered>
        {details && (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              const rate = Number(details.rateLimitPerMinute);
              if (details.rateLimitPerMinute === "" || !Number.isInteger(rate) || rate < 60 || rate > 6000) {
                setDetailErrors({ rateLimitPerMinute: t("apiAccess.rateLimitInvalid") });
                return;
              }
              saveDetails.mutate({ draft: details, version: integration.version });
            }}
          >
            <Stack>
              <TextInput
                label={t("apiAccess.name")}
                required
                maxLength={100}
                value={details.name}
                error={detailErrors.name}
                onChange={(event) => setDetails({ ...details, name: event.currentTarget.value })}
              />
              <Textarea
                label={t("apiAccess.descriptionLabel")}
                autosize
                minRows={2}
                maxLength={1000}
                value={details.description}
                error={detailErrors.description}
                onChange={(event) => setDetails({ ...details, description: event.currentTarget.value })}
              />
              <Select
                label={t("apiAccess.contact")}
                description={t("apiAccess.contactHint")}
                searchable
                clearable
                data={contactOptions(users.data ?? [], details.contactUserId)}
                value={details.contactUserId}
                error={detailErrors.contactUserId}
                onChange={(contactUserId) => setDetails({ ...details, contactUserId })}
              />
              <NumberInput
                label={t("apiAccess.rateLimit")}
                description={t("apiAccess.rateLimitHint")}
                min={60}
                max={6000}
                step={60}
                value={details.rateLimitPerMinute}
                error={detailErrors.rateLimitPerMinute}
                onChange={(value) => setDetails({ ...details, rateLimitPerMinute: value })}
              />
              <Button type="submit" loading={saveDetails.isPending} disabled={!details.name.trim()}>
                {t("common.save")}
              </Button>
            </Stack>
          </form>
        )}
      </Modal>
    </Stack>
  );
};
