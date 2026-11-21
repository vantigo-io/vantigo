import { Alert, Badge, Button, Code, Stack, Table, Text, VisuallyHidden } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageSection, useI18n } from "@vantigo/frontend-shell";
import {
  type IntegrationConnection,
  integrationConnectionsQueryKey,
  listIntegrationConnections,
  revokeIntegrationConnection,
} from "../../api/oauth";

const statusOf = (error: unknown) => (error as { status?: number } | null)?.status;

/** A name the client chose, kept to its box however it is built. */
const clipped = { overflowWrap: "anywhere", overflow: "hidden" } as const;

/**
 * An integration's connected apps on its Overview, under MCP (OAuth
 * connections design C25, A17): each MCP client or app an Owner connected by
 * signing in — the name it calls itself and the host Vantigo verified, who
 * connected it, its access, when, its last use and when it ends by itself —
 * active, or inactive with the reason while the person who connected it no
 * longer qualifies. Revoke ends one behind a confirmation. While the
 * installation has OAuth switched off the list answers 404 and the section is
 * not shown.
 */
export const IntegrationConnections = ({ integrationId, archived }: { integrationId: string; archived: boolean }) => {
  const { t, formatters } = useI18n("host");
  const queryClient = useQueryClient();
  const connections = useQuery({
    queryKey: integrationConnectionsQueryKey(integrationId),
    queryFn: () => listIntegrationConnections(integrationId),
    retry: false,
  });
  const revoke = useMutation({
    mutationFn: (connection: IntegrationConnection) => revokeIntegrationConnection(integrationId, connection.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("apiAccess.connectionRevoked"), message: "" });
      await queryClient.invalidateQueries({ queryKey: integrationConnectionsQueryKey(integrationId) });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("apiAccess.couldNotRevokeConnection"), message: error.message }),
  });

  if (connections.isError && statusOf(connections.error) === 404) return null;
  const when = (value: string) => formatters.formatDate(value, { dateStyle: "medium", timeStyle: "short" });
  const confirmRevoke = (connection: IntegrationConnection) =>
    modals.openConfirmModal({
      title: (
        <Text fw={600} lineClamp={2} style={clipped}>
          {t("apiAccess.revokeConnectionTitle", { name: connection.clientName })}
        </Text>
      ),
      children: <Text size="sm">{t("apiAccess.revokeConnectionBody", { host: connection.clientHost })}</Text>,
      labels: { confirm: t("apiAccess.revoke"), cancel: t("common.cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => revoke.mutate(connection),
    });

  return (
    <PageSection title={t("apiAccess.connectedApps")}>
      <Stack>
        <Text size="sm">{t("apiAccess.connectedAppsDescription")}</Text>
        {connections.isPending ? (
          <ContentSkeleton rows={2} />
        ) : connections.isError ? (
          <Alert color="red" title={t("apiAccess.couldNotLoadConnections")}>
            {connections.error.message}
          </Alert>
        ) : connections.data.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("apiAccess.noConnections")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={960}>
            <Table verticalSpacing="sm" data-testid="connected-apps">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("apiAccess.connectionClient")}</Table.Th>
                  <Table.Th>{t("apiAccess.connectionBy")}</Table.Th>
                  <Table.Th>{t("apiAccess.connectionAccess")}</Table.Th>
                  <Table.Th>{t("apiAccess.connectionConnected")}</Table.Th>
                  <Table.Th>{t("apiAccess.lastUsed")}</Table.Th>
                  <Table.Th>{t("apiAccess.connectionEnds")}</Table.Th>
                  <Table.Th>
                    <VisuallyHidden>{t("apiAccess.actions")}</VisuallyHidden>
                  </Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {connections.data.map((connection) => (
                  <Table.Tr key={connection.id} data-connection={connection.id}>
                    <Table.Td>
                      <Stack gap={4}>
                        <Text fw={600} lineClamp={2} maw={280} style={clipped}>
                          {connection.clientName}
                        </Text>
                        <Code>{connection.clientHost}</Code>
                        <Badge color="gray" variant="outline" data-testid="connection-kind">
                          {t(`apiAccess.connectionKind_${connection.clientKind}`)}
                        </Badge>
                        {connection.status === "active" ? (
                          <Badge color="teal" variant="light">
                            {t("apiAccess.connectionStatus_active")}
                          </Badge>
                        ) : (
                          <Stack gap={2}>
                            <Badge color="orange" variant="light">
                              {t("apiAccess.connectionStatus_inactive")}
                            </Badge>
                            <Text size="xs" c="dimmed">
                              {t(
                                connection.inactiveReason === "authorizer_removed"
                                  ? "apiAccess.inactive_authorizer_removed"
                                  : connection.inactiveReason === "client_not_allowed"
                                    ? "apiAccess.inactive_client_not_allowed"
                                    : "apiAccess.inactive_other",
                              )}
                            </Text>
                          </Stack>
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td>{connection.authorizedByName ?? t("apiAccess.deletedPerson")}</Table.Td>
                    <Table.Td>
                      {connection.scopes.includes("write")
                        ? t("apiAccess.mcpMode_read_write")
                        : t("apiAccess.mcpMode_read")}
                    </Table.Td>
                    <Table.Td>{when(connection.connectedAt)}</Table.Td>
                    <Table.Td>
                      {connection.lastUsedAt
                        ? `${when(connection.lastUsedAt)}${connection.lastUsedIp ? ` · ${connection.lastUsedIp}` : ""}`
                        : t("apiAccess.neverUsed")}
                    </Table.Td>
                    <Table.Td>{when(connection.expiresAt)}</Table.Td>
                    <Table.Td>
                      {!archived && (
                        <Button
                          size="compact-sm"
                          variant="subtle"
                          color="red"
                          onClick={() => confirmRevoke(connection)}
                          aria-label={t("apiAccess.revokeConnectionLabel", { name: connection.clientName })}
                        >
                          {t("apiAccess.revoke")}
                        </Button>
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
    </PageSection>
  );
};
