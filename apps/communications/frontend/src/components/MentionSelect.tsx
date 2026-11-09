import { Group, Select } from "@mantine/core";
import { UserAvatar, useI18n } from "@vantigo/frontend-shell";
import { useAssignableOptions } from "../hooks/useAssignableOptions";
import type { Mention } from "../lib/mentions";

export type MentionSelectProps = {
  /** A colleague was picked: the note gets their @Name and, when sent, their id. */
  onPick: (mention: Mention) => void;
};

/**
 * The @ picker of an internal note (virtual employees design V32): a searchable list of the active users,
 * the AI agents among them with the AI badge. Picking one adds them to the note; the select stays empty so
 * the next colleague can be picked.
 */
export function MentionSelect({ onPick }: MentionSelectProps) {
  const { t } = useI18n("communications");
  const { search, setSearch, options, kinds } = useAssignableOptions({ id: null, name: null });
  return (
    <Select
      size="xs"
      aria-label={t("mentionColleague")}
      placeholder={t("mentionColleague")}
      nothingFoundMessage={t("noPeopleFound")}
      searchable
      data={options}
      value={null}
      searchValue={search}
      onSearchChange={setSearch}
      filter={({ options: all }) => all}
      renderOption={({ option }) => (
        <Group gap="xs" wrap="nowrap">
          <span aria-hidden="true">
            <UserAvatar size="xs" name={option.label} kind={kinds[option.value]} />
          </span>
          {option.label}
        </Group>
      )}
      onChange={(userId) => {
        const picked = options.find((option) => option.value === userId);
        if (userId && picked) onPick({ userId, displayName: picked.label });
        setSearch("");
      }}
    />
  );
}
