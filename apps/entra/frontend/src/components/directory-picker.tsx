import { Badge, Combobox, Group, Loader, Stack, Text, TextInput, useCombobox } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  DIRECTORY_SEARCH_LIMIT,
  DIRECTORY_SEARCH_MAX,
  DIRECTORY_SEARCH_MIN,
  type DirectoryPick,
  directoryUsersQueryOptions,
  type EntraDirectoryUser,
} from "../api/directory";
import "../i18n";
import { refusalText, type Translate } from "../lib/words";

/** How long the picker waits after the last keystroke before it asks the tenant. */
export const DIRECTORY_SEARCH_DEBOUNCE_MS = 300;

export interface DirectoryPickerProps {
  /** Called with the account's two keys when the Owner picks one. */
  onPick: (pick: DirectoryPick) => void;
  /** The user being connected: an account they hold already stays selectable. */
  forUserId?: string;
  /** The name of a user who holds an account, when the host knows it. */
  userName?: (userId: string) => string | undefined;
}

/** Who holds an account in words, or nothing when nobody does. */
const holderText = (account: EntraDirectoryUser, t: Translate, userName?: (userId: string) => string | undefined) => {
  const holder = account.holder;
  if (!holder) return undefined;
  if (holder.kind === "expected") {
    const name = holder.userId ? userName?.(holder.userId) : undefined;
    return name ? t("directoryHolderExpectedNamed", { name }) : t("directoryHolderExpected");
  }
  if (holder.kind === "mapped") return t("directoryHolderMapped");
  return t("directoryHolderAccount");
};

/** An account another user, or Staff, holds already: it cannot be connected to this one. */
const heldByAnother = (account: EntraDirectoryUser, forUserId?: string) =>
  account.holder !== undefined && (account.holder.userId === undefined || account.holder.userId !== forUserId);

/** One account as an option shows it: the name, the principal name, the employee id, and its state. */
export const DirectoryAccount = ({ account, held }: { account: EntraDirectoryUser; held?: string }) => {
  const { t } = useI18n("entra");
  return (
    <Group justify="space-between" wrap="nowrap" gap="xs">
      <Stack gap={0} style={{ minWidth: 0 }}>
        <Text size="sm" fw={500} truncate>
          {account.displayName}
        </Text>
        <Text size="xs" c="dimmed" truncate>
          {account.employeeId
            ? t("directoryAccountKeys", { upn: account.userPrincipalName, employeeId: account.employeeId })
            : account.userPrincipalName}
        </Text>
      </Stack>
      <Group gap={4} wrap="nowrap">
        {!account.accountEnabled && (
          <Badge size="sm" variant="light" color="gray">
            {t("directoryDisabled")}
          </Badge>
        )}
        {held && (
          <Badge size="sm" variant="light" color="orange">
            {held}
          </Badge>
        )}
      </Group>
    </Group>
  );
};

/**
 * A searchable select over the tenant's accounts (Entra phase 2, plan reading
 * 7): it asks the directory once the Owner has typed two characters and
 * paused, shows each hit's principal name, employee id, enabled state and who
 * holds it already, and answers the picked account's object id and principal
 * name. An account another user or Staff holds is shown but cannot be picked.
 */
export const DirectoryPicker = ({ onPick, forUserId, userName }: DirectoryPickerProps) => {
  const { t } = useI18n("entra");
  const combobox = useCombobox({ onDropdownClose: () => combobox.resetSelectedOption() });
  const [search, setSearch] = useState("");
  // The search the last pick put in the field: it is the account's name, not something to look for.
  const [picked, setPicked] = useState<string | null>(null);
  const [debounced] = useDebouncedValue(search.trim(), DIRECTORY_SEARCH_DEBOUNCE_MS);
  const asking = debounced.length >= DIRECTORY_SEARCH_MIN && debounced !== picked;
  const users = useQuery({ ...directoryUsersQueryOptions(debounced), enabled: asking });
  const hits = users.data?.users ?? [];

  const pick = (objectId: string) => {
    const account = hits.find((one) => one.objectId === objectId);
    if (!account) return;
    setPicked(account.displayName);
    setSearch(account.displayName);
    combobox.closeDropdown();
    onPick({ objectId: account.objectId, userPrincipalName: account.userPrincipalName });
  };

  const empty = () => {
    if (search.trim().length < DIRECTORY_SEARCH_MIN) return t("directoryTypeMore");
    if (users.isError) return refusalText(users.error, t);
    if (users.isFetching || search.trim() !== debounced) return t("directorySearching");
    return t("directoryNothingFound");
  };

  return (
    <Combobox store={combobox} onOptionSubmit={pick}>
      <Combobox.Target>
        <TextInput
          label={t("directorySearch")}
          description={t("directorySearchHelp")}
          placeholder={t("directorySearchPlaceholder")}
          value={search}
          maxLength={DIRECTORY_SEARCH_MAX}
          rightSection={users.isFetching ? <Loader size={16} /> : null}
          onChange={(event) => {
            setSearch(event.currentTarget.value);
            setPicked(null);
            combobox.openDropdown();
            combobox.updateSelectedOptionIndex();
          }}
          onClick={() => !picked && combobox.openDropdown()}
          onFocus={() => !picked && combobox.openDropdown()}
          onBlur={() => combobox.closeDropdown()}
        />
      </Combobox.Target>
      <Combobox.Dropdown>
        <Combobox.Options mah={320} style={{ overflowY: "auto" }}>
          {asking && users.isSuccess && !users.isFetching && search.trim() === debounced && hits.length > 0 ? (
            hits.map((account) => {
              const held = holderText(account, t, userName);
              return (
                <Combobox.Option
                  key={account.objectId}
                  value={account.objectId}
                  disabled={heldByAnother(account, forUserId)}
                >
                  <DirectoryAccount account={account} held={held} />
                </Combobox.Option>
              );
            })
          ) : (
            <Combobox.Empty>{empty()}</Combobox.Empty>
          )}
        </Combobox.Options>
        {hits.length >= DIRECTORY_SEARCH_LIMIT && <Combobox.Footer>{t("directoryNarrow")}</Combobox.Footer>}
      </Combobox.Dropdown>
    </Combobox>
  );
};
