import { Alert, Anchor, Badge, Button, Card, Group, Loader, SimpleGrid, Stack, Text } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { IconPlus, IconRobot, IconSparkles } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { agentsQueryOptions, settingsQueryOptions } from "../api/agents";
import type { Agent } from "../api/types";
import { AgentAvatar } from "../components/agent-avatar";
import { NewAgentModal } from "../components/new-agent-modal";
import { SettingsForm } from "../components/settings-form";
import { type AgentsAccess, noAgentsAccess } from "../lib/access";
import { agentLinkOptions } from "../lib/routes";
import "../i18n";

const AgentCard = ({ agent }: { agent: Agent }) => {
  const { t } = useI18n("agents");
  const modes = [...new Set(agent.roles.map((role) => role.replyMode))];
  return (
    <Card withBorder padding="md" radius="md" data-testid="agent-card">
      <Group wrap="nowrap" align="flex-start">
        <AgentAvatar agent={agent} size="lg" />
        <Stack gap={4} style={{ minWidth: 0, flex: 1 }}>
          <Group gap="xs">
            <Anchor fw={600} renderRoot={(props) => <Link {...agentLinkOptions(agent.id)} {...props} />}>
              {agent.name}
            </Anchor>
            {agent.state === "paused" && (
              <Badge color="gray" variant="light" size="sm">
                {t("statePaused")}
              </Badge>
            )}
          </Group>
          <Group gap={4}>
            {agent.roles.length === 0 ? (
              <Text size="sm" c="dimmed">
                {t("noRoles")}
              </Text>
            ) : (
              agent.roles.map((role) => (
                <Badge key={role.id} variant="outline" size="sm" color="gray">
                  {role.name}
                </Badge>
              ))
            )}
          </Group>
          {modes.length > 0 && (
            <Text size="sm" c="dimmed">
              {modes
                .map((mode) =>
                  t(`replyMode.${mode}`, { threshold: agent.roles.find((r) => r.replyMode === mode)?.threshold }),
                )
                .join(" · ")}
            </Text>
          )}
          <Text size="sm" c="dimmed">
            {agent.model || t("modelDefault")}
          </Text>
          <Text size="sm" c="dimmed" title={t("todayCountsHint")}>
            {t("todayCounts")}: —
          </Text>
        </Stack>
      </Group>
    </Card>
  );
};

/** The virtual employees (design V10, screen 1): a card each, and the way to make one. */
export const AgentsPage = ({ access = noAgentsAccess }: { access?: AgentsAccess }) => {
  const { t } = useI18n("agents");
  const [opened, { open, close }] = useDisclosure(false);
  const agents = useQuery(agentsQueryOptions());
  const settings = useQuery(settingsQueryOptions());
  const companyMissing = settings.data !== undefined && settings.data.companyName.trim() === "";
  const notConfigured = settings.data !== undefined && !settings.data.configured;
  const list = agents.data ?? [];

  let body: ReactNode;
  if (agents.isPending || settings.isPending) body = <Loader size="sm" />;
  else if (agents.isError)
    body = (
      <Alert color="red">
        <Group>
          <Text size="sm">{t("loadFailed")}</Text>
          <Button size="xs" variant="light" onClick={() => void agents.refetch()}>
            {t("tryAgain")}
          </Button>
        </Group>
      </Alert>
    );
  else if (notConfigured && list.length === 0)
    body = <EmptyState icon={IconSparkles} title={t("aiNotSetUpTitle")} description={t("aiNotSetUpDescription")} />;
  else if (list.length === 0)
    body = <EmptyState icon={IconRobot} title={t("noAgents")} description={t("noAgentsDescription")} />;
  else
    body = (
      <SimpleGrid cols={{ base: 1, md: 2, xl: 3 }}>
        {list.map((agent) => (
          <AgentCard key={agent.id} agent={agent} />
        ))}
      </SimpleGrid>
    );

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("listTitle")}
        description={t("listDescription")}
        actions={
          <Group gap="sm">
            {agents.data && (
              <Text size="sm" c="dimmed">
                {t("agentCount", { count: list.length })}
              </Text>
            )}
            {access.canManage && (
              <Button leftSection={<IconPlus size={16} />} onClick={open} disabled={companyMissing || notConfigured}>
                {t("newAgent")}
              </Button>
            )}
          </Group>
        }
      />
      {notConfigured && list.length > 0 && (
        <Alert color="yellow" title={t("aiNotSetUpTitle")}>
          {t("aiNotSetUpDescription")}
        </Alert>
      )}
      {companyMissing && access.canManage && (
        <Alert color="yellow" title={t("companyCalloutTitle")}>
          <Stack gap="sm">
            <Text size="sm">{t("companyCalloutBody")}</Text>
            <SettingsForm canManage />
          </Stack>
        </Alert>
      )}
      {body}
      <NewAgentModal opened={opened} onClose={close} />
    </Stack>
  );
};
