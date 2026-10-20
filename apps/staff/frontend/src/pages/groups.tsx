import { Alert, Anchor, Badge, Button, Card, Group, Stack, Switch, Table } from "@mantine/core";
import { IconAlertCircle, IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { groupsQueryOptions } from "../api/groups";
import { staffMetaQueryOptions } from "../api/meta";
import "../i18n";
import { providerNameOf, syncStateColor } from "../lib/groups";
import { groupLinkOptions } from "../lib/routes";
import { GroupFormModal } from "./-group-form-modal";

/**
 * Groups (Entra design D9): every group by name with its key, its kind, how
 * many members it has and where its sync stands with each provider HR turned
 * on for it; the archived behind a switch; *New group* opens the form, and a
 * new group opens on its own page. `staff:manage`'s alone.
 */
export const GroupsPage = () => {
  const { t } = useI18n("staff");
  const groups = useQuery(groupsQueryOptions());
  const providers = useQuery(staffMetaQueryOptions()).data?.providers;
  const [showArchived, setShowArchived] = useState(false);
  const [creating, setCreating] = useState(false);
  const rows = (groups.data ?? []).filter((group) => showArchived || !group.archivedAt);

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("navGroups")}
        description={t("groupsDescription")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={() => setCreating(true)}>
            {t("newGroup")}
          </Button>
        }
      />
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group justify="flex-end">
            <Switch
              label={t("showArchivedGroups")}
              checked={showArchived}
              onChange={(event) => setShowArchived(event.currentTarget.checked)}
            />
          </Group>
          {groups.isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadGroups")}>
              {groups.error.message}
            </Alert>
          )}
          {groups.isPending && <ContentSkeleton rows={3} rowHeight={44} />}
          {groups.isSuccess && rows.length === 0 && (
            <EmptyState title={t("noGroups")} description={t("noGroupsHint")} />
          )}
          {rows.length > 0 && (
            <Table.ScrollContainer minWidth={720}>
              <Table aria-label={t("navGroups")}>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("name")}</Table.Th>
                    <Table.Th>{t("groupKey")}</Table.Th>
                    <Table.Th>{t("groupKind")}</Table.Th>
                    <Table.Th>{t("groupMembers")}</Table.Th>
                    <Table.Th>{t("groupProviders")}</Table.Th>
                    <Table.Th>{t("status")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {rows.map((group) => (
                    <Table.Tr key={group.id} c={group.archivedAt ? "dimmed" : undefined}>
                      <Table.Td>
                        <Anchor size="sm" renderRoot={(props) => <Link {...groupLinkOptions(group.id)} {...props} />}>
                          {group.name}
                        </Anchor>
                      </Table.Td>
                      <Table.Td>{group.key}</Table.Td>
                      <Table.Td>{t(`groupKind_${group.kind}`)}</Table.Td>
                      <Table.Td>{group.memberCount}</Table.Td>
                      <Table.Td>
                        <Group gap={4}>
                          {group.providers.length === 0
                            ? t("notSet")
                            : group.providers.map((provider) => (
                                <Badge
                                  key={provider.key}
                                  variant="light"
                                  color={provider.enabled ? syncStateColor[provider.state] : "gray"}
                                >
                                  {t("groupProviderState", {
                                    provider: providerNameOf(provider.key, providers),
                                    state: t(
                                      provider.enabled ? `groupSyncState_${provider.state}` : "groupProviderOff",
                                    ),
                                  })}
                                </Badge>
                              ))}
                        </Group>
                      </Table.Td>
                      <Table.Td>
                        {group.archivedAt && (
                          <Badge variant="light" color="gray">
                            {t("archived")}
                          </Badge>
                        )}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
        </Stack>
      </Card>
      <GroupFormModal opened={creating} onClose={() => setCreating(false)} />
    </Stack>
  );
};
