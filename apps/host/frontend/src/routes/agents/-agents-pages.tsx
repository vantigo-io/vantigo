import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import type { AgentsAccess } from "@vantigo/agents-ui";
import { AgentPage } from "@vantigo/agents-ui/pages/agent";
import { AgentsPage } from "@vantigo/agents-ui/pages/agents";
import { RolePage } from "@vantigo/agents-ui/pages/role";
import { SettingsPage } from "@vantigo/agents-ui/pages/settings";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { useAgentsAccess } from "../../lib/agents-access";

/**
 * The Agents app's pages, each told what the caller may do once the root
 * layout's session has answered. They sit beside the route files because a
 * route file may export nothing but its `Route`.
 */
const withAccess = (render: (access: AgentsAccess) => ReactNode) =>
  function AgentsRoutePage() {
    const { ready, access } = useAgentsAccess();
    if (!ready) return <ContentSkeleton rows={4} rowHeight={48} />;
    return <>{render(access)}</>;
  };

export const AgentsRoutePage = withAccess((access) => <AgentsPage access={access} />);
export const SettingsRoutePage = withAccess((access) => <SettingsPage access={access} />);

const Agent = ({ access }: { access: AgentsAccess }) => {
  const { agentId } = useParams({ from: "/agents/$agentId/" });
  const { tab } = useSearch({ from: "/agents/$agentId/" });
  const navigate = useNavigate({ from: "/agents/$agentId/" });
  return (
    <AgentPage
      agentId={agentId}
      tab={tab}
      onTabChange={(next) => void navigate({ search: { tab: next } })}
      access={access}
    />
  );
};
export const AgentRoutePage = withAccess((access) => <Agent access={access} />);

const Role = ({ access }: { access: AgentsAccess }) => {
  const { agentId, roleId } = useParams({ from: "/agents/$agentId/roles/$roleId" });
  return <RolePage agentId={agentId} roleId={roleId} access={access} />;
};
export const RoleRoutePage = withAccess((access) => <Role access={access} />);
