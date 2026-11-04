import { Badge, Button, Group, Table, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { confirmGroupAdoption, type GoogleGroup, groupsQueryOptions } from "../api/connection";
import { GOOGLE_QUERY_KEY } from "../api/request";
import { refusalText } from "../lib/words";
import { SectionCard } from "./step-card";

/** A group the module found holding a Staff group's address and adopted, that nobody has confirmed yet. */
const awaitsConfirmation = (group: GoogleGroup) => !group.createdByUs && !group.adoptionConfirmedAt;

/**
 * The **Groups** table (Google Workspace design G20): every group the module
 * made or found for a Staff group — its address, the Staff group and whether
 * Vantigo made or adopted it. The one thing done here is confirming a group
 * the module found rather than made — until then it writes nothing to it.
 */
export const GroupsCard = () => {
  const { t } = useI18n("google");
  const groups = useQuery(groupsQueryOptions());
  const queryClient = useQueryClient();

  const confirm = useMutation({
    mutationFn: (group: GoogleGroup) => confirmGroupAdoption(group.groupId),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("adoptionConfirmed"), message: t("adoptionConfirmedBody") });
      await queryClient.invalidateQueries({ queryKey: [GOOGLE_QUERY_KEY, "groups"] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotConfirm"), message: refusalText(error, t) }),
  });

  const askConfirm = (group: GoogleGroup) =>
    modals.openConfirmModal({
      title: t("confirmGroupTitle", { email: group.email }),
      children: <Text size="sm">{t("confirmGroupBody", { email: group.email })}</Text>,
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
        <Table.ScrollContainer minWidth={480}>
          <Table verticalSpacing={4} aria-label={t("groups")}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("groupEmail")}</Table.Th>
                <Table.Th>{t("groupStaffId")}</Table.Th>
                <Table.Th>{t("groupOrigin")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {groups.data.map((group) => (
                <Table.Tr key={group.groupId}>
                  <Table.Td>{group.email}</Table.Td>
                  <Table.Td>{group.staffGroupId}</Table.Td>
                  <Table.Td>
                    {awaitsConfirmation(group) ? (
                      <Group gap="xs" wrap="nowrap">
                        <Badge color="orange" variant="light">
                          {t("groupAwaitsConfirmation")}
                        </Badge>
                        <Button
                          size="compact-sm"
                          variant="light"
                          aria-label={t("confirmGroupNamed", { email: group.email })}
                          loading={confirm.isPending && confirm.variables?.groupId === group.groupId}
                          onClick={() => askConfirm(group)}
                        >
                          {t("confirmAdoption")}
                        </Button>
                      </Group>
                    ) : (
                      <Text size="sm">{group.createdByUs ? t("groupCreated") : t("groupAdopted")}</Text>
                    )}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
    </SectionCard>
  );
};
