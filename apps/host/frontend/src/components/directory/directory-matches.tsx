import { Alert, Badge, Button, Checkbox, Group, Stack, Table, Text } from "@mantine/core";
import { IconCircleCheck } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type DirectoryMatch, type DirectoryPick, directoryMatchesQueryOptions } from "../../api/directory";
import { DirectoryAccount } from "./directory-picker";
import { directoryRefusal } from "./words";

/** A user as the host holds it, for the names beside the proposals. */
export interface DirectoryMatchesUser {
  id: string;
  displayName?: string | null;
  email?: string | null;
  /** Whether the user is an Owner: connecting one is never proposed unchecked. */
  owner?: boolean;
}

export interface DirectoryMatchesProps {
  /** The users the host already holds, for each proposal's name and address. */
  users: readonly DirectoryMatchesUser[];
  /**
   * Connects one user to one account — identity's announced link, the host's
   * call. It throws an `Error` whose message is already in the Owner's words.
   */
  connect: (userId: string, pick: DirectoryPick) => Promise<void>;
  /** Called once *Connect selected* has gone through every chosen pair. */
  onDone?: () => void;
}

type Outcome = { connected: true } | { connected: false; message: string };

/**
 * The review an Owner connects a whole workspace from (Google Workspace
 * design G8; Entra phase 2, plan reading 8), through identity's
 * GET /owner/sso/directory/matches: for every user not yet connected, the one directory account
 * that matches them exactly, each with a checkbox — checked only when the
 * account is enabled, the principal name the person signs in with matched,
 * and the user is not an Owner; any other row is left unchecked and marked
 * *Check this match* with the reason — and *Connect selected*, which connects the chosen
 * pairs one at a time through the host and marks each row connected or
 * refused.
 */
export const DirectoryMatches = ({ users, connect, onDone }: DirectoryMatchesProps) => {
  const { t } = useI18n("host");
  const matches = useQuery({ ...directoryMatchesQueryOptions(), staleTime: 0 });
  // What the Owner changed; a row untouched is checked when it is proposed (proposed).
  const [choice, setChoice] = useState<Record<string, boolean>>({});
  const [outcomes, setOutcomes] = useState<Record<string, Outcome>>({});
  const [running, setRunning] = useState(false);

  const userFor = (userId: string) => users.find((one) => one.id === userId);

  // Why a match should be looked at before it is connected: a mail or an
  // employee number alone is a weaker key than the principal name the person
  // signs in with, and an Owner's sign-in is the workspace's.
  const concerns = (match: DirectoryMatch) => ({
    weakKey: !match.matchedBy.includes("principalName"),
    owner: userFor(match.userId)?.owner === true,
  });
  const proposed = (match: DirectoryMatch) => {
    const { weakKey, owner } = concerns(match);
    return match.account.enabled && !weakKey && !owner;
  };
  const done = (match: DirectoryMatch) => outcomes[match.userId]?.connected === true;
  const checked = (match: DirectoryMatch) => !done(match) && (choice[match.userId] ?? proposed(match));
  const proposals = matches.data?.matches ?? [];
  const selected = proposals.filter(checked);

  const connectSelected = async () => {
    setRunning(true);
    for (const match of selected) {
      let outcome: Outcome;
      try {
        await connect(match.userId, {
          objectId: match.account.objectId,
          userPrincipalName: match.account.userPrincipalName,
        });
        outcome = { connected: true };
      } catch (error) {
        outcome = { connected: false, message: error instanceof Error ? error.message : String(error) };
      }
      setOutcomes((current) => ({ ...current, [match.userId]: outcome }));
    }
    setRunning(false);
    onDone?.();
  };

  if (matches.isPending) return <ContentSkeleton rows={3} rowHeight={36} />;
  if (matches.isError)
    return (
      <Alert color="red" title={t("admin.directoryMatchesFailed")}>
        {directoryRefusal(matches.error, t)}
      </Alert>
    );

  const { candidates, ambiguous, truncated } = matches.data;
  return (
    <Stack>
      <Text size="sm">
        {t("admin.directoryMatchesSummary", { candidates, matches: proposals.length, ambiguous })}
        {truncated && ` ${t("admin.directoryMatchesTruncated")}`}
      </Text>
      {proposals.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("admin.directoryMatchesNone")}
        </Text>
      ) : (
        <>
          <Table.ScrollContainer minWidth={640}>
            <Table verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th w={40} />
                  <Table.Th>{t("admin.directoryMatchUser")}</Table.Th>
                  <Table.Th>{t("admin.directoryMatchAccount")}</Table.Th>
                  <Table.Th>{t("admin.directoryMatchedBy")}</Table.Th>
                  <Table.Th>{t("admin.directoryMatchResult")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {proposals.map((match) => {
                  const user = userFor(match.userId);
                  const name = user?.displayName || user?.email || t("admin.directoryUnknownUser");
                  const outcome = outcomes[match.userId];
                  const { weakKey, owner } = concerns(match);
                  return (
                    <Table.Tr key={match.userId}>
                      <Table.Td>
                        <Checkbox
                          aria-label={t("admin.directoryMatchChoose", {
                            name,
                            account: match.account.userPrincipalName,
                          })}
                          checked={checked(match)}
                          disabled={running || done(match)}
                          onChange={(event) => {
                            const value = event.currentTarget.checked;
                            setChoice((current) => ({ ...current, [match.userId]: value }));
                          }}
                        />
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm" fw={500}>
                          {name}
                        </Text>
                        {user?.email && user.email !== name && (
                          <Text size="xs" c="dimmed">
                            {user.email}
                          </Text>
                        )}
                      </Table.Td>
                      <Table.Td>
                        <DirectoryAccount account={match.account} />
                      </Table.Td>
                      <Table.Td>
                        <Group gap={4}>
                          {match.matchedBy.map((key) => (
                            <Badge key={key} size="sm" variant="light">
                              {t(`admin.directoryMatchedBy_${key}`)}
                            </Badge>
                          ))}
                        </Group>
                        {(weakKey || owner) && (
                          <Stack gap={2} mt={6}>
                            <Badge size="sm" variant="light" color="yellow">
                              {t("admin.directoryCheckMatch")}
                            </Badge>
                            {weakKey && (
                              <Text size="xs" c="dimmed">
                                {t("admin.directoryCheckWeakKey")}
                              </Text>
                            )}
                            {owner && (
                              <Text size="xs" c="dimmed">
                                {t("admin.directoryCheckOwner")}
                              </Text>
                            )}
                          </Stack>
                        )}
                      </Table.Td>
                      <Table.Td>
                        {outcome?.connected === true && (
                          <Group gap={5} wrap="nowrap">
                            <IconCircleCheck size={15} color="var(--mantine-color-teal-6)" />
                            <Text size="sm">{t("admin.directoryMatchConnected")}</Text>
                          </Group>
                        )}
                        {outcome?.connected === false && (
                          <Text size="sm" c="red">
                            {outcome.message}
                          </Text>
                        )}
                      </Table.Td>
                    </Table.Tr>
                  );
                })}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
          <Group justify="flex-end">
            <Button onClick={() => void connectSelected()} loading={running} disabled={selected.length === 0}>
              {t("admin.directoryConnectSelected", { count: selected.length })}
            </Button>
          </Group>
        </>
      )}
    </Stack>
  );
};
