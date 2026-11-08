import { type MantineSize, Tooltip } from "@mantine/core";
import { UserAvatar } from "@vantigo/frontend-shell";
import { avatarUrl } from "../api/agents";
import type { Agent } from "../api/types";

/** An agent's picture with the AI badge on it: an agent is never shown as if it were a person. */
export const AgentAvatar = ({ agent, size = "lg" }: { agent: Agent; size?: MantineSize | number }) => {
  return (
    <Tooltip label={agent.name} withArrow>
      <span>
        <UserAvatar kind="agent" name={agent.name} src={avatarUrl(agent)} size={size} />
      </span>
    </Tooltip>
  );
};
