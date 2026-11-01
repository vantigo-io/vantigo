import { Stack } from "@mantine/core";
import { AssistantSettingsCard, type PermissionsTranslation } from "@vantigo/assistant-ui";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import {
  translateHostPermissionCategory,
  translateHostPermissionDescription,
  translateHostPermissionKey,
  translateHostPermissionModule,
} from "../../catalogs/admin";
import "../../i18n";

/**
 * Workspace admin › Assistant: the package's Owner card, with the host's words for the permission
 * catalog (the same names the role editor shows, so the table reads as Roles & access does).
 */
export const AssistantSettingsPage = () => {
  const { t } = useI18n("host");
  const translate: PermissionsTranslation = {
    module: (group) => translateHostPermissionModule(group.permissions[0]?.key ?? "", group.module, t),
    category: (group) => translateHostPermissionCategory(group.permissions[0]?.key ?? "", group.category, t),
    permission: (permission) => ({
      display: translateHostPermissionKey(permission.key, t, permission.display),
      description: permission.description
        ? translateHostPermissionDescription(permission.key, permission.description, t)
        : undefined,
    }),
  };
  return (
    <Stack gap="md">
      <PageHeader title={t("navigation.assistantSettings")} />
      <AssistantSettingsCard translate={translate} />
    </Stack>
  );
};
