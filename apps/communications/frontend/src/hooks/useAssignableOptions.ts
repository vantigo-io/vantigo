import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { type AssignableUser, assignableUsersQueryOptions } from "../api/conversations";

/**
 * The people a select offers, found by a typed name through the user directory. The one already
 * chosen is kept in the list, so its name shows even when the search no longer finds it.
 */
export function useAssignableOptions(chosen: { id: string | null; name: string | null | undefined }) {
  const [search, setSearch] = useState("");
  const [debounced] = useDebouncedValue(search, 250);
  const users = useQuery(assignableUsersQueryOptions(debounced.trim()));
  // The unfiltered first page, so a person chosen earlier is named even when the search no longer finds them.
  const everyone = useQuery(assignableUsersQueryOptions(""));
  const options = useMemo(() => {
    const found: AssignableUser[] = users.data ?? [];
    const list = found.map((user) => ({ value: user.userId, label: user.displayName }));
    if (chosen.id && !list.some((option) => option.value === chosen.id)) {
      const known = everyone.data?.find((user) => user.userId === chosen.id)?.displayName;
      list.unshift({ value: chosen.id, label: chosen.name || known || chosen.id });
    }
    return list;
  }, [users.data, everyone.data, chosen.id, chosen.name]);
  // Who is an AI agent, by user id, so the avatars can carry the AI badge.
  const kinds = useMemo(() => {
    const byId: Record<string, "person" | "agent" | undefined> = {};
    for (const user of [...(everyone.data ?? []), ...(users.data ?? [])]) byId[user.userId] = user.kind;
    return byId;
  }, [users.data, everyone.data]);
  return { search, setSearch, options, kinds };
}
