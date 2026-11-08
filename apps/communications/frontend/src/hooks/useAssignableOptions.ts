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
  const options = useMemo(() => {
    const found: AssignableUser[] = users.data ?? [];
    const list = found.map((user) => ({ value: user.userId, label: user.displayName }));
    if (chosen.id && !list.some((option) => option.value === chosen.id))
      list.unshift({ value: chosen.id, label: chosen.name || chosen.id });
    return list;
  }, [users.data, chosen.id, chosen.name]);
  return { search, setSearch, options };
}
