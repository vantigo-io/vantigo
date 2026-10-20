import { Badge, Table, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { groupsQueryOptions } from "../api/connection";
import { SectionCard } from "./step-card";

/**
 * The **Groups** table (Entra design D14, D15) under the Status card: every
 * group the module keeps in the tenant for Staff's groups, deleted ones
 * included — its name as last written, its type, whether it is a team, its
 * state and when its members were last reconciled. Read-only: Staff decides
 * what exists, and this is the tenant's side.
 */
export const GroupsCard = () => {
  const { t, formatters } = useI18n("entra");
  const groups = useQuery(groupsQueryOptions());
  const dateTime = (value: string) =>
    formatters.formatDate(new Date(value), { dateStyle: "medium", timeStyle: "short" });

  return (
    <SectionCard title={t("groups")}>
      {groups.isPending && <ContentSkeleton rows={2} rowHeight={24} />}
      {groups.isError && (
        <Text size="sm" c="red">
          {t("groupsFailed")}
        </Text>
      )}
      {groups.data?.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noGroups")}
        </Text>
      )}
      {groups.data && groups.data.length > 0 && (
        <Table.ScrollContainer minWidth={560}>
          <Table verticalSpacing={4} aria-label={t("groups")}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("groupName")}</Table.Th>
                <Table.Th>{t("groupType")}</Table.Th>
                <Table.Th>{t("groupTeam")}</Table.Th>
                <Table.Th>{t("groupState")}</Table.Th>
                <Table.Th>{t("groupLastMembers")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {groups.data.map((group) => (
                <Table.Tr key={group.objectId} c={group.state === "deleted" ? "dimmed" : undefined}>
                  <Table.Td>
                    <Text size="sm">{group.displayName}</Text>
                    <Text size="xs" c="dimmed">
                      {group.mailNickname}
                    </Text>
                  </Table.Td>
                  <Table.Td>{t(`groupType_${group.type}`)}</Table.Td>
                  <Table.Td>{group.team ? t("yes") : t("no")}</Table.Td>
                  <Table.Td>
                    <Badge variant="light" color={group.state === "present" ? "teal" : "gray"}>
                      {t(`groupState_${group.state}`)}
                    </Badge>
                    {!group.createdByUs && (
                      <Text size="xs" c="dimmed">
                        {t("groupAdopted")}
                      </Text>
                    )}
                  </Table.Td>
                  <Table.Td>{group.lastMembersAt ? dateTime(group.lastMembersAt) : t("never")}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
    </SectionCard>
  );
};
