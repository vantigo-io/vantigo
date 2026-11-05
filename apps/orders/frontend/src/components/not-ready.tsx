import { Stack } from "@mantine/core";
import { IconTools } from "@tabler/icons-react";
import { EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";

/** A page of the app whose screen is not built yet: its title, and a word that it is coming (orders design O11). */
export const NotReadyPage = ({ titleKey, descriptionKey }: { titleKey: string; descriptionKey?: string }) => {
  const { t } = useI18n("orders");
  return (
    <Stack gap="lg">
      <PageHeader title={t(titleKey)} description={descriptionKey ? t(descriptionKey) : undefined} />
      <EmptyState icon={IconTools} title={t("notReadyTitle")} description={t("notReadyDescription")} />
    </Stack>
  );
};
