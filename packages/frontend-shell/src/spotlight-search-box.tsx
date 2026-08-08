import { Kbd, Text, TextInput } from "@mantine/core";
import { useOs } from "@mantine/hooks";
import { spotlight } from "@mantine/spotlight";
import { IconSearch } from "@tabler/icons-react";

/**
 * A search-box-styled button opening the global spotlight, with the platform's
 * keyboard shortcut as a hint. Place it at the top of the sidebar via the
 * AppShellLayout `navbarTop` slot; the app provides its own Spotlight.Root.
 */
export const SpotlightSearchBox = () => {
  const os = useOs();
  const modKey = os === "macos" ? "\u2318" : "Ctrl";

  return (
    <TextInput
      component="button"
      type="button"
      onClick={spotlight.open}
      leftSection={<IconSearch size={16} stroke={1.5} />}
      rightSection={<Kbd size="xs">{modKey} + K</Kbd>}
      rightSectionWidth={70}
      aria-label="Search"
      styles={{ input: { cursor: "pointer" } }}
    >
      <Text size="sm" c="dimmed" component="span">
        Search
      </Text>
    </TextInput>
  );
};
