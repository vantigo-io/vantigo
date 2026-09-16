import { ActionIcon } from "@mantine/core";
import { spotlight } from "@mantine/spotlight";
import { IconSearch } from "@tabler/icons-react";
import { registerCatalog, useI18n } from "./i18n";
import { shellCatalog } from "./i18n/catalogs/shell";

registerCatalog("shell", shellCatalog);

/**
 * The narrow-viewport search trigger: an icon in the header's action group
 * that opens the same spotlight the desktop search box does. Hidden from the
 * `sm` breakpoint up, where the search box is visible instead.
 */
export const SpotlightSearchButton = () => {
  const { t } = useI18n("shell");
  return (
    <ActionIcon
      variant="subtle"
      color="gray"
      size="lg"
      radius="xl"
      hiddenFrom="sm"
      aria-label={t("openSearch")}
      onClick={spotlight.open}
    >
      <IconSearch size={20} stroke={1.5} />
    </ActionIcon>
  );
};
