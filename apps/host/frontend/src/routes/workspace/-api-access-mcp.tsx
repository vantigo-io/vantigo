import { Alert, Radio, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { PageSection, useI18n } from "@vantigo/frontend-shell";
import {
  type IntegrationDetail,
  type IntegrationMcpAccess,
  integrationQueryKey,
  integrationsQueryKey,
  setIntegrationMcpAccess,
} from "../../api/integrations";
import { codeOf, mcpEndpoint } from "./-api-access-shared";
import { Copyable, McpSnippets } from "./-api-key-dialog";

const modes: IntegrationMcpAccess[] = ["off", "read", "read_write"];

/**
 * An integration's MCP section on its Overview (MCP server design M16, M33):
 * what its keys may do at /api/mcp, saved under If-Match, the endpoint's
 * address and, once it is on, how a client connects, with a placeholder for
 * the key. When the installation has switched MCP off the section says so,
 * whatever the setting.
 */
export const IntegrationMcp = ({ detail, archived }: { detail: IntegrationDetail; archived: boolean }) => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const { integration, mcpAccess, mcpAvailable } = detail;
  const save = useMutation({
    mutationFn: (mode: IntegrationMcpAccess) => setIntegrationMcpAccess(integration.id, integration.version, mode),
    onSuccess: async (next) => {
      queryClient.setQueryData(integrationQueryKey(integration.id), next);
      notifications.show({ color: "teal", title: t("apiAccess.saved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: integrationsQueryKey, exact: true });
    },
    onError: async (error) => {
      notifications.show({
        color: codeOf(error) === "version_mismatch" ? "orange" : "red",
        title: t("apiAccess.couldNotSave"),
        message: codeOf(error) === "version_mismatch" ? t("apiAccess.changedMeanwhile") : (error as Error).message,
      });
      await queryClient.invalidateQueries({ queryKey: integrationQueryKey(integration.id) });
    },
  });
  const url = mcpEndpoint();

  return (
    <PageSection title={t("apiAccess.mcpTitle")}>
      <Stack>
        <Text size="sm">{t("apiAccess.mcpDescription")}</Text>
        {!mcpAvailable && (
          <Alert color="gray" title={t("apiAccess.mcpUnavailableTitle")}>
            {t("apiAccess.mcpUnavailable")}
          </Alert>
        )}
        <Radio.Group
          label={t("apiAccess.mcpAccess")}
          value={mcpAccess}
          onChange={(value) => save.mutate(value as IntegrationMcpAccess)}
        >
          <Stack gap="xs" mt="xs">
            {modes.map((mode) => (
              <Radio
                key={mode}
                value={mode}
                label={t(`apiAccess.mcpMode_${mode}`)}
                description={t(`apiAccess.mcpModeHint_${mode}`)}
                disabled={archived || save.isPending}
              />
            ))}
          </Stack>
        </Radio.Group>
        {mcpAccess !== "off" && (
          <Alert color="orange" icon={<IconAlertTriangle size={16} />}>
            {t("apiAccess.mcpWarning")}
            {mcpAccess === "read_write" && ` ${t("apiAccess.mcpWriteWarning")}`}
          </Alert>
        )}
        <Copyable label={t("apiAccess.mcpUrl")} value={url} />
        {mcpAccess !== "off" && mcpAvailable && (
          <>
            <McpSnippets url={url} name={integration.name} />
            <Text size="sm" c="dimmed">
              {t("apiAccess.mcpSnippetHint")}
            </Text>
          </>
        )}
      </Stack>
    </PageSection>
  );
};
