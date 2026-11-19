import { Alert, Button, Group } from "@mantine/core";
import { useI18n } from "../i18n";
import { setBrandPreview, useBrand } from "./provider";

/**
 * "You are previewing Brand X — stop" (brands design B20): shown to the admin whose tab is previewing a
 * brand's draft ("preview as me"), and to nobody else. It never touches the server's state.
 */
export const BrandPreviewBanner = () => {
  const brand = useBrand();
  const { t } = useI18n("shell");
  if (brand.source !== "preview") return null;
  return (
    <Alert color="yellow" variant="light" radius={0} py="xs" role="status">
      <Group justify="space-between" wrap="nowrap">
        <span>{t("brandPreviewing", { name: brand.name })}</span>
        <Button size="compact-sm" variant="default" onClick={() => setBrandPreview(null)}>
          {t("brandPreviewStop")}
        </Button>
      </Group>
    </Alert>
  );
};
