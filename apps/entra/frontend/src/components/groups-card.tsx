import { Badge, Button, Group, Stack, Table, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { confirmGroupAdoption, type EntraGroup, groupsQueryOptions } from "../api/connection";
import { ENTRA_QUERY_KEY } from "../api/request";
import { refusalText } from "../lib/words";
import { SectionCard } from "./step-card";

/** A group the module found by its nickname and adopted, present, that nobody has confirmed yet. */
const awaitsConfirmation = (group: EntraGroup) =>
  group.state === "present" && !group.createdByUs && !group.adoptionConfirmedAt;

/**
 * The **Groups** table (Entra design D14, D15) under the Status card: every
 * group the module keeps in the tenant for Staff's groups, deleted and
 * retired ones included — its name as last written, its type, whether it is
 * a team, its state and when its members were last reconciled. Staff decides
 * what exists, and this is the tenant's side; the one thing done here is
 * confirming a group the module found rather than made — until then it
 * writes nothing to it.
 */
export const GroupsCard = () => {
  const { t, formatters } = useI18n("entra");
  const groups = useQuery(groupsQueryOptions());
  const queryClient = useQueryClient();
  const dateTime = (value: string) =>
    formatters.formatDate(new Date(value), { dateStyle: "medium", timeStyle: "short" });

  const confirm = useMutation({
    mutationFn: (group: EntraGroup) => confirmGroupAdoption(group.objectId),
    onSuccess: async () => {
      notifications.show({
        color: "teal",
        title: t("groupAdoptionConfirmed"),
        message: t("groupAdoptionConfirmedBody"),
      });
      await queryClient.invalidateQueries({ queryKey: [ENTRA_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotConfirm"), message: refusalText(error, t) }),
  });

  const askConfirm = (group: EntraGroup) =>
    modals.openConfirmModal({
      title: t("confirmGroupAdoptionTitle", { name: group.displayName }),
      children: (
        <Text size="sm">
          {t("confirmGroupAdoptionBody", { name: group.displayName, nickname: group.mailNickname })}
        </Text>
      ),
      labels: { confirm: t("confirmAdoption"), cancel: t("cancel") },
      onConfirm: () => confirm.mutate(group),
    });

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
                <Table.Tr key={group.objectId} c={group.state === "present" ? undefined : "dimmed"}>
                  <Table.Td>
                    <Text size="sm">{group.displayName}</Text>
                    <Text size="xs" c="dimmed">
                      {group.mailNickname}
                    </Text>
                  </Table.Td>
                  <Table.Td>{t(`groupType_${group.type}`)}</Table.Td>
                  <Table.Td>{group.team ? t("yes") : t("no")}</Table.Td>
                  <Table.Td>
                    <Stack gap={4} align="flex-start">
                      <Badge variant="light" color={group.state === "present" ? "teal" : "gray"}>
                        {t(`groupState_${group.state}`)}
                      </Badge>
                      {awaitsConfirmation(group) ? (
                        <Group gap="xs" wrap="nowrap">
                          <Badge color="orange" variant="light">
                            {t("groupAwaitsConfirmation")}
                          </Badge>
                          <Button
                            size="compact-sm"
                            variant="light"
                            aria-label={t("confirmGroupAdoptionNamed", { name: group.displayName })}
                            loading={confirm.isPending && confirm.variables?.objectId === group.objectId}
                            onClick={() => askConfirm(group)}
                          >
                            {t("confirmAdoption")}
                          </Button>
                        </Group>
                      ) : (
                        !group.createdByUs && (
                          <Text size="xs" c="dimmed">
                            {t("groupAdopted")}
                          </Text>
                        )
                      )}
                    </Stack>
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
