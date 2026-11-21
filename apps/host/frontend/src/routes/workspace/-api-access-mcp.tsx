import { Alert, Radio, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageSection, useI18n } from "@vantigo/frontend-shell";
import {
  type IntegrationDetail,
  type IntegrationMcpAccess,
  integrationQueryKey,
  integrationsQueryKey,
  setIntegrationMcpAccess,
} from "../../api/integrations";
import { integrationConnectionsQueryKey, listIntegrationConnections } from "../../api/oauth";
import { codeOf, mcpEndpoint, mcpSnippets } from "./-api-access-shared";
import { Copyable, McpSnippets } from "./-api-key-dialog";

const modes: IntegrationMcpAccess[] = ["off", "read", "read_write"];

/**
 * How a client connects by signing in instead of with a key (OAuth
 * connections design C22, C25): the address to paste into Claude or ChatGPT,
 * which reach it from the internet and so need a public https address, Claude
 * Code's command without a key, and, for a client that publishes no metadata
 * document, a client registered on the API access page.
 */
const SignInInstructions = ({ url, name }: { url: string; name: string }) => {
  const { t } = useI18n("host");
  return (
    <Stack gap="sm" data-testid="mcp-sign-in">
      <Text fw={600}>{t("apiAccess.mcpSignInTitle")}</Text>
      <Text size="sm">{t("apiAccess.mcpSignInHosted")}</Text>
      {!url.startsWith("https://") && (
        <Alert color="orange" icon={<IconAlertTriangle size={16} />}>
          {t("apiAccess.mcpSignInNotHttps")}
        </Alert>
      )}
      <Copyable label={t("apiAccess.mcpSignInClaudeCode")} value={mcpSnippets(url, name).claudeSignIn} />
      <Text size="sm" c="dimmed">
        {t("apiAccess.mcpSignInClaudeCodeHint")}
      </Text>
      <Text size="sm" c="dimmed">
        {t("apiAccess.mcpSignInRegistered")}
      </Text>
    </Stack>
  );
};

/**
 * An integration's MCP section on its Overview (MCP server design M16, M33):
 * what its keys and connected apps may do at /api/mcp, saved under If-Match,
 * the endpoint's address and, once it is on, how a client connects: with a
 * key read from the client's own store, or, while the installation has OAuth
 * on, by signing in. When the installation has switched MCP off the section
 * says so, whatever the setting.
 */
export const IntegrationMcp = ({ detail, archived }: { detail: IntegrationDetail; archived: boolean }) => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const { integration, mcpAccess, mcpAvailable } = detail;
  // The connected apps' list answers 404 while OAuth is switched off: only
  // once it has answered is anything said about signing in.
  const connections = useQuery({
    queryKey: integrationConnectionsQueryKey(integration.id),
    queryFn: () => listIntegrationConnections(integration.id),
    retry: false,
  });
  const oauthOn = connections.isSuccess;
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
  const connectable = mcpAccess !== "off" && mcpAvailable;

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
        {connectable && oauthOn && <Text fw={600}>{t("apiAccess.mcpWithKeyTitle")}</Text>}
        {connectable && <McpSnippets url={url} name={integration.name} />}
        {connectable && oauthOn && !archived && <SignInInstructions url={url} name={integration.name} />}
      </Stack>
    </PageSection>
  );
};
