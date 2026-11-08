import { Alert, Badge, Button, Group, Loader, Stack, Tabs, Text } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconPlayerPause, IconPlayerPlay, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { EmptyState, PageHeader, PageSection, PageTabs, useI18n } from "@vantigo/frontend-shell";
import { agentQueryKey, agentQueryOptions, agentsQueryKey, deleteAgent, pauseAgent, resumeAgent } from "../api/agents";
import { NotFoundError } from "../api/request";
import { ConfirmDialog } from "../components/confirm-dialog";
import { PermissionsTab } from "../components/permissions-tab";
import { PersonaTab } from "../components/persona-tab";
import { RolesTab } from "../components/roles-tab";
import { useErrorText } from "../components/use-error-text";
import { type AgentsAccess, noAgentsAccess } from "../lib/access";
import { AGENTS_HOME_ROUTE_PATH, type AgentTab, agentTabs } from "../lib/routes";
import "../i18n";

const tabLabel = {
  persona: "tabPersona",
  roles: "tabRoles",
  permissions: "tabPermissions",
  usage: "tabUsage",
  activity: "tabActivity",
} as const satisfies Record<AgentTab, string>;

export interface AgentPageProps {
  agentId: string;
  /** The tab the URL names; the host keeps it in the `tab` search parameter. */
  tab: AgentTab;
  onTabChange: (tab: AgentTab) => void;
  access?: AgentsAccess;
}

/** One virtual employee (design V10, screen 2): persona, roles, permissions, and the places usage and activity will be. */
export const AgentPage = ({ agentId, tab, onTabChange, access = noAgentsAccess }: AgentPageProps) => {
  const { t } = useI18n("agents");
  const errorText = useErrorText();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const query = useQuery(agentQueryOptions(agentId));
  const [confirming, { open, close }] = useDisclosure(false);
  const agent = query.data;

  const changeState = useMutation({
    mutationFn: () => (agent?.state === "paused" ? resumeAgent(agentId) : pauseAgent(agentId)),
    onSuccess: (saved) => {
      queryClient.setQueryData(agentQueryKey(agentId), saved);
      void queryClient.invalidateQueries({ queryKey: agentsQueryKey });
      notifications.show({ color: "teal", title: saved.state === "paused" ? t("paused") : t("resumed"), message: "" });
    },
    onError: (error) => notifications.show({ color: "red", title: t("pauseFailed"), message: errorText(error) }),
  });

  const remove = useMutation({
    mutationFn: () => deleteAgent(agentId),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: agentQueryKey(agentId) });
      void queryClient.invalidateQueries({ queryKey: agentsQueryKey });
      notifications.show({ color: "teal", title: t("deleted"), message: "" });
      close();
      navigate({ to: AGENTS_HOME_ROUTE_PATH });
    },
    onError: (error) => {
      close();
      notifications.show({ color: "red", title: t("deleteFailed"), message: errorText(error) });
    },
  });

  const breadcrumbs = [{ label: t("breadcrumbAgents"), to: AGENTS_HOME_ROUTE_PATH }];
  if (query.isPending) return <Loader size="sm" />;
  if (query.isError || !agent) {
    return (
      <Stack gap="lg">
        <PageHeader title={t("agents")} breadcrumbs={breadcrumbs} />
        {query.error instanceof NotFoundError ? (
          <EmptyState title={t("agentNotFound")} />
        ) : (
          <Alert color="red">
            <Group>
              <Text size="sm">{t("loadFailed")}</Text>
              <Button size="xs" variant="light" onClick={() => void query.refetch()}>
                {t("tryAgain")}
              </Button>
            </Group>
          </Alert>
        )}
      </Stack>
    );
  }

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[...breadcrumbs, { label: agent.name }]}
        title={
          <Group gap="xs">
            {agent.name}
            {agent.state === "paused" && (
              <Badge color="gray" variant="light">
                {t("statePaused")}
              </Badge>
            )}
          </Group>
        }
        actions={
          access.canManage && (
            <Group gap="xs">
              <Button
                variant="default"
                leftSection={agent.state === "paused" ? <IconPlayerPlay size={16} /> : <IconPlayerPause size={16} />}
                loading={changeState.isPending}
                onClick={() => changeState.mutate()}
              >
                {agent.state === "paused" ? t("resume") : t("pause")}
              </Button>
              <Button variant="default" color="red" leftSection={<IconTrash size={16} />} onClick={open}>
                {t("deleteAgent")}
              </Button>
            </Group>
          )
        }
      />
      <PageTabs
        aria-label={t("tabsLabel")}
        value={tab}
        onChange={onTabChange}
        items={agentTabs.map((value) => ({ value, label: t(tabLabel[value]) }))}
      >
        <Tabs.Panel value="persona" pt="md">
          <PageSection>
            {/* Keyed by the version, so a newer one (a stale save's reload) replaces what was typed. */}
            <PersonaTab key={agent.version} agent={agent} canManage={access.canManage} />
          </PageSection>
        </Tabs.Panel>
        <Tabs.Panel value="roles" pt="md">
          <PageSection>
            <RolesTab agent={agent} canManage={access.canManage} />
          </PageSection>
        </Tabs.Panel>
        <Tabs.Panel value="permissions" pt="md">
          <PageSection>
            <PermissionsTab agent={agent} />
          </PageSection>
        </Tabs.Panel>
        <Tabs.Panel value="usage" pt="md">
          <PageSection title={t("usageTitle")}>
            <EmptyState title={t("comingLater")} size="sm" />
          </PageSection>
        </Tabs.Panel>
        <Tabs.Panel value="activity" pt="md">
          <PageSection title={t("activityTitle")}>
            <EmptyState title={t("comingLater")} size="sm" />
          </PageSection>
        </Tabs.Panel>
      </PageTabs>
      <ConfirmDialog
        opened={confirming}
        title={t("deleteAgentTitle", { name: agent.name })}
        body={t("deleteAgentBody")}
        confirmLabel={t("deleteAgent")}
        pending={remove.isPending}
        onConfirm={() => remove.mutate()}
        onClose={close}
      />
    </Stack>
  );
};
