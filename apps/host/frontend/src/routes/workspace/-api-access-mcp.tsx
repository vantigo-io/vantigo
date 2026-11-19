import { Alert, Radio, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { appUrl, PageSection, useI18n } from "@vantigo/frontend-shell";
import {
  type IntegrationDetail,
  type IntegrationMcpAccess,
  integrationQueryKey,
  integrationsQueryKey,
  setIntegrationMcpAccess,
} from "../../api/integrations";
import { codeOf } from "./-api-access-shared";
import { Copyable } from "./-api-key-dialog";

const modes: IntegrationMcpAccess[] = ["off", "read", "read_write"];

/** The MCP endpoint's address on this installation, as the curl hint builds it (M33). */
export const mcpEndpoint = () => `${window.location.origin}${appUrl("/api/mcp")}`;

/**
 * How a client connects (M16): the Claude Code command, and the url and
 * headers block other clients take. The key is a placeholder: its secret is
 * shown once, when it is made.
 */
export const mcpSnippets = (url: string, name: string) => {
  const server =
    name
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "") || "vantigo";
  const header = "Authorization: Bearer vtg_…";
  return {
    claude: `claude mcp add --transport http ${server} ${url} --header "${header}"`,
    json: JSON.stringify({ mcpServers: { [server]: { url, headers: { Authorization: "Bearer vtg_…" } } } }, null, 2),
  };
};

/**
 * An integration's MCP section on its Overview (MCP server design M16, M21,
 * M33): what its keys may do at /api/mcp, saved under If-Match, the endpoint's
 * address and, once it is on, how a client connects. When the installation has
 * switched MCP off the section says so, whatever the setting.
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
  const snippets = mcpSnippets(url, integration.name);

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
          </Alert>
        )}
        <Copyable label={t("apiAccess.mcpUrl")} value={url} />
        {mcpAccess !== "off" && mcpAvailable && (
          <>
            <Copyable label={t("apiAccess.mcpClaudeCode")} value={snippets.claude} multiline />
            <Copyable label={t("apiAccess.mcpJson")} value={snippets.json} multiline />
            <Text size="sm" c="dimmed">
              {t("apiAccess.mcpSnippetHint")}
            </Text>
          </>
        )}
      </Stack>
    </PageSection>
  );
};
