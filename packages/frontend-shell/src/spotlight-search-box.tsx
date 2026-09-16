import { Kbd, Text, TextInput } from "@mantine/core";
import { useOs } from "@mantine/hooks";
import { spotlight } from "@mantine/spotlight";
import { IconSearch } from "@tabler/icons-react";
import { registerCatalog, useI18n } from "./i18n";
import { shellCatalog } from "./i18n/catalogs/shell";

registerCatalog("shell", shellCatalog);

/**
 * A search-box-styled button opening the global spotlight, with the platform's
 * keyboard shortcut as a hint. Place it in the header via the AppShellLayout
 * `headerCenter` slot; the app provides its own Spotlight.Root.
 */
export const SpotlightSearchBox = () => {
  const os = useOs();
  const { t } = useI18n("shell");
  const modKey = os === "macos" ? t("macCommandKey") : t("controlKey");

  return (
    <TextInput
      component="button"
      type="button"
      onClick={spotlight.open}
      leftSection={<IconSearch size={16} stroke={1.5} />}
      rightSection={<Kbd size="xs">{t("shortcutHint", { modKey })}</Kbd>}
      rightSectionWidth={70}
      aria-label={t("search")}
      styles={{ input: { cursor: "pointer" } }}
    >
      <Text size="sm" c="dimmed" component="span">
        {t("search")}
      </Text>
    </TextInput>
  );
};
