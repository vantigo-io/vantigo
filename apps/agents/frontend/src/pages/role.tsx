import { Alert, Button, Group, Loader, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { EmptyState, PageHeader, PageSection, useI18n } from "@vantigo/frontend-shell";
import { agentQueryOptions } from "../api/agents";
import { NotFoundError } from "../api/request";
import { RoleForm } from "../components/role-form";
import { type AgentsAccess, noAgentsAccess } from "../lib/access";
import { AGENTS_HOME_ROUTE_PATH } from "../lib/routes";
import "../i18n";

export interface RolePageProps {
  agentId: string;
  roleId: string;
  access?: AgentsAccess;
}

/** One role of an agent (design V10, screen 3). The role is read from its agent, which holds all of its roles. */
export const RolePage = ({ agentId, roleId, access = noAgentsAccess }: RolePageProps) => {
  const { t } = useI18n("agents");
  const query = useQuery(agentQueryOptions(agentId));
  const agent = query.data;
  const role = agent?.roles.find((candidate) => candidate.id === roleId);

  if (query.isPending) return <Loader size="sm" />;
  if (query.isError || !agent)
    return (
      <Stack gap="lg">
        <PageHeader title={t("agents")} breadcrumbs={[{ label: t("breadcrumbAgents"), to: AGENTS_HOME_ROUTE_PATH }]} />
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

  const agentPath = `${AGENTS_HOME_ROUTE_PATH}/${agent.id}`;
  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[
          { label: t("breadcrumbAgents"), to: AGENTS_HOME_ROUTE_PATH },
          { label: agent.name, to: agentPath },
          { label: role?.name ?? t("tabRoles") },
        ]}
        title={role?.name ?? t("tabRoles")}
        description={t("rolesDescription")}
      />
      {role ? (
        <PageSection>
          {/* Keyed by the version, so a newer one (a stale save's reload) replaces what was typed. */}
          <RoleForm
            key={role.version}
            agent={agent}
            role={role}
            canManage={access.canManage}
            isOwner={access.isOwner}
          />
        </PageSection>
      ) : (
        <EmptyState title={t("roleNotFound")} />
      )}
    </Stack>
  );
};
