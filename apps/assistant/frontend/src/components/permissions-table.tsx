import { Badge, Box, Button, Group, Loader, SegmentedControl, Select, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  isVersionConflict,
  permissionsQueryKey,
  permissionsQueryOptions,
  putPermission,
  settingsQueryKey,
} from "../api/assistant";
import type {
  AssistantPermission,
  AssistantPermissionGroup,
  AssistantPermissionLevel,
  AssistantPermissions,
  AssistantSettings,
} from "../api/types";
import { filterGroups, type PermissionFilter } from "../lib/permissions";

export interface PermissionsTranslation {
  /** The module's heading for a group; the group's own `module` key when omitted. */
  module?: (group: AssistantPermissionGroup) => string;
  /** The category's heading for a group. */
  category?: (group: AssistantPermissionGroup) => string;
  /** The permission's name and description in the person's language. */
  permission?: (permission: AssistantPermission) => { display: string; description?: string };
}

const levels: readonly AssistantPermissionLevel[] = ["off", "read", "change"];
const levelKey = { off: "levelOff", read: "levelRead", change: "levelChange" } as const;

/**
 * The catalog of the enabled modules, grouped by module and category, with a
 * level per permission (S16): Off, Read, or Read and change. Only overrides
 * are stored, so each row offers Reset to default. Read and change lets the assistant propose
 * changes, each of which waits for the person's confirmation. Every write names the settings' version
 * in If-Match; a stale one reloads the table and says so.
 */
export const PermissionsTable = ({ translate }: { translate?: PermissionsTranslation }) => {
  const { t } = useI18n("assistant");
  const queryClient = useQueryClient();
  const [filter, setFilter] = useState<PermissionFilter>("all");
  const query = useQuery(permissionsQueryOptions());

  const save = useMutation({
    mutationFn: ({ key, level }: { key: string; level: AssistantPermissionLevel | null }) =>
      putPermission(key, level, query.data?.version ?? 0),
    onSuccess: ({ version, permission }) => {
      queryClient.setQueryData<AssistantPermissions>(
        permissionsQueryKey,
        (current) =>
          current && {
            version,
            groups: current.groups.map((group) => ({
              ...group,
              permissions: group.permissions.map((one) => (one.key === permission.key ? permission : one)),
            })),
          },
      );
      queryClient.setQueryData<AssistantSettings>(settingsQueryKey, (current) => current && { ...current, version });
    },
    onError: (error) => {
      if (isVersionConflict(error)) {
        notifications.show({ color: "yellow", title: t("staleTitle"), message: t("staleBody") });
        void queryClient.invalidateQueries({ queryKey: permissionsQueryKey });
        void queryClient.invalidateQueries({ queryKey: settingsQueryKey });
        return;
      }
      notifications.show({
        color: "red",
        title: t("saveFailed"),
        message: error instanceof Error ? error.message : t("requestFailed"),
      });
    },
  });

  if (query.isPending) return <Loader size="sm" />;
  if (query.isError || !query.data)
    return (
      <Group>
        <Text size="sm" c="red">
          {t("permissionsFailed")}
        </Text>
        <Button size="xs" variant="light" onClick={() => void query.refetch()}>
          {t("tryAgain")}
        </Button>
      </Group>
    );

  const groups = filterGroups(query.data.groups, filter);
  const levelLabel = (level: AssistantPermissionLevel) => t(levelKey[level]);

  return (
    <Stack gap="sm">
      <Group justify="space-between" align="flex-end">
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            {t("filterLabel")}
          </Text>
          <SegmentedControl
            size="xs"
            value={filter}
            onChange={(value) => setFilter(value as PermissionFilter)}
            data={[
              { value: "all", label: t("filterAll") },
              { value: "sensitive", label: t("filterSensitive") },
              { value: "overridden", label: t("filterOverridden") },
            ]}
          />
        </Stack>
        <Stack gap={2} maw={460}>
          {levels.map((level) => (
            <Text key={level} size="xs" c="dimmed">
              <b>{levelLabel(level)}</b>
              {": "}
              {t(`${levelKey[level]}Help`)}
            </Text>
          ))}
        </Stack>
      </Group>
      {groups.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noPermissions")}
        </Text>
      )}
      {groups.map((group) => (
        <Box key={`${group.module}/${group.category}`}>
          <Text size="xs" fw={700} tt="uppercase" c="dimmed" mb={4}>
            {translate?.module?.(group) ?? group.module}
            {" › "}
            {translate?.category?.(group) ?? group.category}
          </Text>
          <Table.ScrollContainer minWidth={520}>
            <Table verticalSpacing="xs" withRowBorders>
              <Table.Tbody>
                {group.permissions.map((permission) => {
                  const words = translate?.permission?.(permission) ?? {
                    display: permission.display,
                    description: permission.description,
                  };
                  return (
                    <Table.Tr key={permission.key} data-permission={permission.key}>
                      <Table.Td>
                        <Group gap={6} wrap="nowrap">
                          <Text size="sm">{words.display}</Text>
                          {permission.sensitive && (
                            <Badge size="xs" color="orange" variant="light">
                              {t("sensitiveBadge")}
                            </Badge>
                          )}
                          {permission.override != null && (
                            <Badge size="xs" color="blue" variant="light">
                              {t("overriddenBadge")}
                            </Badge>
                          )}
                        </Group>
                        {words.description && (
                          <Text size="xs" c="dimmed">
                            {words.description}
                          </Text>
                        )}
                        <Text size="xs" c="dimmed">
                          {t("defaultLevel", { level: levelLabel(permission.defaultLevel) })}
                        </Text>
                      </Table.Td>
                      <Table.Td w={230}>
                        <Select
                          size="xs"
                          allowDeselect={false}
                          aria-label={t("levelFor", { permission: words.display })}
                          value={permission.effectiveLevel}
                          data={levels.map((level) => ({ value: level, label: levelLabel(level) }))}
                          disabled={save.isPending}
                          onChange={(value) =>
                            value && save.mutate({ key: permission.key, level: value as AssistantPermissionLevel })
                          }
                        />
                      </Table.Td>
                      <Table.Td w={170} ta="right">
                        <Button
                          size="compact-xs"
                          variant="subtle"
                          disabled={permission.override == null || save.isPending}
                          aria-label={`${t("resetToDefault")}: ${words.display}`}
                          onClick={() => save.mutate({ key: permission.key, level: null })}
                        >
                          {t("resetToDefault")}
                        </Button>
                      </Table.Td>
                    </Table.Tr>
                  );
                })}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        </Box>
      ))}
    </Stack>
  );
};
