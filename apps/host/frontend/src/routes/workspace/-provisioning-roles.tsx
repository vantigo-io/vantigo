import { Alert, Badge, Button, Card, Checkbox, Group, Stack, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { AuthorizationRole, PermissionDescriptor } from "../../api/authorization";
import { listProvisioningRoles, replaceProvisioningRoles } from "../../api/provisioning-roles";
import { translateHostPermissionKey } from "../../i18n";
import "../../i18n";

/**
 * The keys a role Staff may give must not carry, as identity refuses them
 * (`provisioningDeniedKeys`): identity:manage, Staff's administrative keys,
 * and every key of the catalog that cannot be delegated.
 */
const deniedKeys = (catalog: PermissionDescriptor[]) =>
  new Set([
    "identity:manage",
    "staff:approve",
    "staff:manage",
    "staff:personal-data",
    ...catalog.filter((p) => !p.delegable).map((p) => p.key),
  ]);

const codePoint = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

const provisioningRolesKey = ["owner", "provisioning-roles"] as const;

/**
 * Roles Staff may give: the custom roles every account Staff provisions is
 * given. An Owner's section of the roles page; the whole set is saved at once
 * with `PUT /owner/provisioning-roles`.
 */
export const ProvisioningRolesSection = ({
  customRoles,
  catalog,
}: {
  customRoles: AuthorizationRole[];
  catalog: PermissionDescriptor[];
}) => {
  const { t } = useI18n("host");
  const qc = useQueryClient();
  const current = useQuery({ queryKey: provisioningRolesKey, queryFn: listProvisioningRoles });
  // The Owner's edits, over the set as loaded; undefined until they tick or untick a role.
  const [edited, setEdited] = useState<string[] | undefined>(undefined);
  const saved = current.data?.roles.map((role) => role.id) ?? [];
  const selected = edited ?? saved;
  const changed = selected.length !== saved.length || selected.some((id) => !saved.includes(id));
  const denied = deniedKeys(catalog);
  // Identity names a role's first refused key in key order (plain code-point order, as Go sorts).
  const deniedKeyOf = (role: AuthorizationRole) => role.permissions.filter((key) => denied.has(key)).sort(codePoint)[0];
  const save = useMutation({
    mutationFn: (roleIds: string[]) => replaceProvisioningRoles(roleIds),
    onSuccess: (data) => {
      qc.setQueryData(provisioningRolesKey, data);
      setEdited(undefined);
      notifications.show({
        color: "teal",
        title: t("admin.provisioningRolesSaved"),
        message: t("admin.provisioningRolesSavedMessage"),
      });
    },
    onError: (error, roleIds) => {
      let message = error instanceof Error ? error.message : t("common.requestFailed");
      if ((error as { code?: string }).code === "role_not_assignable") {
        // Identity names the first role, by name, carrying a key it refuses; the
        // same is found here so the sentence is in the reader's language.
        const offending = customRoles
          .filter((role) => roleIds.includes(role.id) && deniedKeyOf(role))
          .sort((a, b) => codePoint(a.name, b.name))[0];
        const key = offending && deniedKeyOf(offending);
        message =
          offending && key
            ? t("admin.refusalRoleNotAssignableKey", {
                role: offending.displayName,
                permission: translateHostPermissionKey(key, t, catalog.find((p) => p.key === key)?.displayName ?? key),
                key,
              })
            : t("admin.refusalRoleNotAssignable");
      }
      notifications.show({ color: "red", title: t("admin.provisioningRolesSaveFailed"), message });
    },
  });
  const toggle = (id: string, checked: boolean) =>
    setEdited(checked ? [...selected, id] : selected.filter((roleId) => roleId !== id));
  return (
    <Card withBorder>
      <Stack>
        <div>
          <Title order={3}>{t("admin.provisioningRoles")}</Title>
          <Text size="sm" c="dimmed" mt={4}>
            {t("admin.provisioningRolesDescription")}
          </Text>
        </div>
        {current.isError ? (
          <Alert color="red" title={t("admin.provisioningRolesLoadFailed")}>
            {t("common.tryAgain")}
          </Alert>
        ) : current.isPending ? (
          <ContentSkeleton rows={2} rowHeight={40} />
        ) : (
          <>
            <Text fw={600} size="sm">
              {t("admin.provisioningRolesPicker")}
            </Text>
            {selected.length === 0 && (
              <Alert color="yellow" variant="light">
                {t("admin.provisioningRolesNone")}
              </Alert>
            )}
            {customRoles.map((role) => (
              <Stack key={role.id} gap={4} data-role-row>
                <Checkbox
                  label={role.displayName}
                  checked={selected.includes(role.id)}
                  // A role that grants administration is never newly given; one already in the set may be taken out.
                  disabled={!!deniedKeyOf(role) && !selected.includes(role.id)}
                  onChange={(event) => toggle(role.id, event.currentTarget.checked)}
                />
                <Group gap={4} pl={32}>
                  {deniedKeyOf(role) && (
                    <Badge color="orange" variant="light" size="sm">
                      {t("admin.provisioningRoleGrantsAdministration")}
                    </Badge>
                  )}
                  {role.permissions.map((key) => (
                    <Badge key={key} variant="outline" color="gray" size="sm" title={key}>
                      {translateHostPermissionKey(key, t, catalog.find((p) => p.key === key)?.displayName ?? key)}
                    </Badge>
                  ))}
                </Group>
              </Stack>
            ))}
            <Group>
              <Button onClick={() => save.mutate(selected)} loading={save.isPending} disabled={!changed}>
                {t("admin.saveProvisioningRoles")}
              </Button>
            </Group>
          </>
        )}
      </Stack>
    </Card>
  );
};
