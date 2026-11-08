import { Alert, Anchor, Badge, Button, Group, Loader, Stack, Table, Text, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconCircleCheck, IconLock } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import { putPermission, setupQueryKey, setupQueryOptions } from "../api/agents";
import type { Agent, AgentSetupItem } from "../api/types";
import { permissionGap, permissionTone } from "../lib/levels";
import { IDENTITY_ROLES_ROUTE_PATH } from "../lib/routes";
import { useErrorText } from "./use-error-text";

/** The levels and Identity roles the agent's roles need, and which are missing (V12). */
export const PermissionsTab = ({ agent }: { agent: Agent }) => {
  const { t } = useI18n("agents");
  const errorText = useErrorText();
  const queryClient = useQueryClient();
  const setup = useQuery(setupQueryOptions(agent.id));

  const raise = useMutation({
    mutationFn: (item: AgentSetupItem) => putPermission(agent.id, item.permissionKey, item.needed),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: setupQueryKey(agent.id) });
      notifications.show({ color: "teal", title: t("raised"), message: "" });
    },
    onError: (error) => notifications.show({ color: "red", title: t("raiseFailed"), message: errorText(error) }),
  });

  if (setup.isPending) return <Loader size="sm" />;
  if (setup.isError || !setup.data)
    return (
      <Alert color="red">
        <Group>
          <Text size="sm">{t("setupLoadFailed")}</Text>
          <Button size="xs" variant="light" onClick={() => void setup.refetch()}>
            {t("tryAgain")}
          </Button>
        </Group>
      </Alert>
    );
  const { items, hasRoles, ready } = setup.data;
  if (!hasRoles || items.length === 0) return <EmptyState title={t("setupNoRoles")} />;
  const needsRoles = items.some((item) => !item.heldByRoles);

  return (
    <Stack gap="md">
      <Text size="sm" c="dimmed" maw={640}>
        {t("permissionsDescription")}
      </Text>
      {ready && (
        <Alert color="teal" icon={<IconCircleCheck size={16} />} title={t("setupReadyTitle")}>
          {t("setupReadyBody")}
        </Alert>
      )}
      <Table.ScrollContainer minWidth={640}>
        <Table verticalSpacing="sm">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("permission")}</Table.Th>
              <Table.Th>{t("needsLevel")}</Table.Th>
              <Table.Th>{t("currentLevel")}</Table.Th>
              <Table.Th>{t("permissionStatus")}</Table.Th>
              <Table.Th />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {items.map((item) => {
              const gap = permissionGap(item);
              return (
                <Table.Tr key={item.permissionKey}>
                  <Table.Td>
                    <Group gap={6} wrap="nowrap">
                      {item.locked && (
                        <Tooltip label={t("permissionLockedHelp")}>
                          <IconLock size={14} aria-label={t("permissionLockedLabel")} />
                        </Tooltip>
                      )}
                      <Text size="sm">{item.display}</Text>
                    </Group>
                  </Table.Td>
                  <Table.Td>{t(`level.${item.needed}`)}</Table.Td>
                  <Table.Td>{t(`level.${item.level}`)}</Table.Td>
                  <Table.Td>
                    <Group gap={4}>
                      {item.satisfied ? (
                        <Badge color="teal" variant="light">
                          {t("permissionOk")}
                        </Badge>
                      ) : (
                        <>
                          {gap.locked && (
                            <Badge color="red" variant="light">
                              {t("permissionLockedLabel")}
                            </Badge>
                          )}
                          {gap.needsRaise && !gap.locked && (
                            <Badge color={permissionTone(item)} variant="light">
                              {t("permissionNeedsRaise")}
                            </Badge>
                          )}
                          {gap.needsRoles && (
                            <Badge color="yellow" variant="light">
                              {t("permissionNeedsRoles")}
                            </Badge>
                          )}
                        </>
                      )}
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    {gap.needsRaise && !gap.locked && (
                      <Button
                        size="xs"
                        variant="light"
                        loading={raise.isPending && raise.variables?.permissionKey === item.permissionKey}
                        onClick={() => raise.mutate(item)}
                      >
                        {t("raiseTo", { level: t(`level.${item.needed}`) })}
                      </Button>
                    )}
                  </Table.Td>
                </Table.Tr>
              );
            })}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      {needsRoles && (
        <Alert color="yellow">
          <Stack gap={4}>
            <Text size="sm">{t("identityRolesHint", { id: agent.userId })}</Text>
            <Anchor size="sm" renderRoot={(props) => <Link to={IDENTITY_ROLES_ROUTE_PATH} {...props} />}>
              {t("identityRolesLink")}
            </Anchor>
          </Stack>
        </Alert>
      )}
    </Stack>
  );
};
