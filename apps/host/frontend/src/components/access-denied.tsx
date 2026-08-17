import { Alert, Center, Stack, Text, Title } from "@mantine/core";
import { IconLock } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";

/** In-place page shown when a destination's module is disabled or permissions are missing. */
export const AccessDenied = () => {
  const { t } = useI18n("host");
  return (
    <Center mih="50vh" p="xl">
      <Stack align="center" maw={440} ta="center">
        <IconLock size={32} stroke={1.5} />
        <Title order={2}>{t("accessDeniedTitle")}</Title>
        <Text c="dimmed">{t("accessDeniedBody")}</Text>
        <Alert color="gray" variant="light">
          {t("tenantContactAdmin")}
        </Alert>
      </Stack>
    </Center>
  );
};
