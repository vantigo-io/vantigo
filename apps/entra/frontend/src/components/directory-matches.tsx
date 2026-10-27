import { Alert, Badge, Button, Checkbox, Group, Stack, Table, Text } from "@mantine/core";
import { IconCircleCheck } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type DirectoryPick, directoryMatchesQueryOptions, type EntraDirectoryMatch } from "../api/directory";
import "../i18n";
import { refusalText } from "../lib/words";
import { DirectoryAccount } from "./directory-picker";

/** A user as the host holds it, for the names beside the proposals. */
export interface DirectoryMatchesUser {
  id: string;
  displayName?: string | null;
  email?: string | null;
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
 * The review an Owner connects a whole workspace from (Entra phase 2, plan
 * reading 8): for every user not yet connected, the one directory account
 * that matches them exactly, each with a checkbox — all checked but a
 * disabled account's — and *Connect selected*, which connects the chosen
 * pairs one at a time through the host and marks each row connected or
 * refused.
 */
export const DirectoryMatches = ({ users, connect, onDone }: DirectoryMatchesProps) => {
  const { t } = useI18n("entra");
  const matches = useQuery({ ...directoryMatchesQueryOptions(), staleTime: 0 });
  // What the Owner changed; a row untouched is checked when its account is enabled.
  const [choice, setChoice] = useState<Record<string, boolean>>({});
  const [outcomes, setOutcomes] = useState<Record<string, Outcome>>({});
  const [running, setRunning] = useState(false);

  const done = (match: EntraDirectoryMatch) => outcomes[match.userId]?.connected === true;
  const checked = (match: EntraDirectoryMatch) =>
    !done(match) && (choice[match.userId] ?? match.account.accountEnabled);
  const proposals = matches.data?.matches ?? [];
  const selected = proposals.filter(checked);

  const userFor = (userId: string) => users.find((one) => one.id === userId);

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
      <Alert color="red" title={t("directoryMatchesFailed")}>
        {refusalText(matches.error, t)}
      </Alert>
    );

  const { candidates, ambiguous, truncated } = matches.data;
  return (
    <Stack>
      <Text size="sm">
        {t("directoryMatchesSummary", { candidates, matches: proposals.length, ambiguous })}
        {truncated && ` ${t("directoryMatchesTruncated")}`}
      </Text>
      {proposals.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("directoryMatchesNone")}
        </Text>
      ) : (
        <>
          <Table.ScrollContainer minWidth={640}>
            <Table verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th w={40} />
                  <Table.Th>{t("directoryMatchUser")}</Table.Th>
                  <Table.Th>{t("directoryMatchAccount")}</Table.Th>
                  <Table.Th>{t("directoryMatchedBy")}</Table.Th>
                  <Table.Th>{t("directoryMatchResult")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {proposals.map((match) => {
                  const user = userFor(match.userId);
                  const name = user?.displayName || user?.email || t("directoryUnknownUser");
                  const outcome = outcomes[match.userId];
                  return (
                    <Table.Tr key={match.userId}>
                      <Table.Td>
                        <Checkbox
                          aria-label={t("directoryMatchChoose", { name, account: match.account.userPrincipalName })}
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
                              {t(`directoryMatchedBy_${key}`)}
                            </Badge>
                          ))}
                        </Group>
                      </Table.Td>
                      <Table.Td>
                        {outcome?.connected === true && (
                          <Group gap={5} wrap="nowrap">
                            <IconCircleCheck size={15} color="var(--mantine-color-teal-6)" />
                            <Text size="sm">{t("directoryMatchConnected")}</Text>
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
              {t("directoryConnectSelected", { count: selected.length })}
            </Button>
          </Group>
        </>
      )}
    </Stack>
  );
};
