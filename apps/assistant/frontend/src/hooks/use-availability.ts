import { useQuery } from "@tanstack/react-query";
import { availabilityQueryOptions } from "../api/assistant";

/**
 * Whether the signed-in person has the assistant (S8), and whether the
 * provider is local. The shell shows nothing of the assistant until this says
 * `available`; `enabled` is the host's own gate (a session, the module on).
 */
export const useAssistantAvailability = (enabled = true) => {
  const query = useQuery({ ...availabilityQueryOptions(), enabled });
  return {
    available: query.data?.available === true,
    local: query.data?.local ?? true,
    reason: query.data?.reason,
    isPending: enabled && query.isPending,
  };
};
