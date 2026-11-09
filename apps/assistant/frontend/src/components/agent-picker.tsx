import { NativeSelect } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { agentsQueryOptions } from "../api/assistant";

export interface AgentPickerProps {
  /** The virtual employee chosen; null is the Assistant itself. */
  value: string | null;
  onChange: (agentId: string | null) => void;
}

/**
 * Who a new conversation is with (virtual employees design V31): the Assistant
 * or one of the virtual employees the person may talk to. The server lists the
 * agents only for a person with agents:view, so for anyone else, and where
 * there are none, nothing is shown and the conversation is the Assistant's own.
 */
export const AgentPicker = ({ value, onChange }: AgentPickerProps) => {
  const { t } = useI18n("assistant");
  const agents = useQuery(agentsQueryOptions());
  const items = agents.data?.items ?? [];
  if (items.length === 0) return null;
  return (
    <NativeSelect
      size="xs"
      label={t("talkToLabel")}
      aria-label={t("talkToLabel")}
      data-testid="agent-picker"
      value={value ?? ""}
      onChange={(event) => onChange(event.currentTarget.value || null)}
      data={[
        { value: "", label: t("talkToAssistant") },
        ...items.map((agent) => ({ value: agent.id, label: agent.name })),
      ]}
    />
  );
};
