import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

/** One note an agent keeps about the customer (virtual employees design V27). */
export interface AgentMemoryNote {
  id: string;
  text: string;
  createdAt: string;
}

/** One agent's notes about the customer, the newest first. */
export interface AgentMemoryEntry {
  agentId: string;
  agentName: string;
  notes: AgentMemoryNote[];
}

/**
 * What the agents remember about a customer. The list is empty for a reader without `agents:view` and when the
 * agents module is off, so the section simply does not show; `canDelete` says the reader holds `agents:manage`.
 */
export interface CustomerAgentMemory {
  agents: AgentMemoryEntry[];
  canDelete: boolean;
}

export const agentMemoryQueryKey = (customerId: number) => ["customers", customerId, "agent-memory"] as const;

export const customerAgentMemoryQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: agentMemoryQueryKey(customerId),
    queryFn: ({ signal }) => request<CustomerAgentMemory>(`/api/v1/customers/${customerId}/agent-memory`, { signal }),
    retry: false,
  });

/** Deleting a note is the agents module's own operation, under its own permission (`agents:manage`). */
export const deleteAgentNote = (noteId: string) =>
  request<void>(`/api/v1/agents/memory/${encodeURIComponent(noteId)}`, { method: "DELETE" });
