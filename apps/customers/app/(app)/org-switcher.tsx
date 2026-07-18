"use client";

import { Avatar, Group, Text, UnstyledButton } from "@mantine/core";
import { IconChevronDown } from "@tabler/icons-react";

/**
 * Placeholder organization switcher.
 * Will be wired to better-auth organizations later.
 */
export function OrgSwitcher() {
  return (
    <UnstyledButton w="100%" p="xs">
      <Group justify="space-between" wrap="nowrap">
        <Group gap="xs" wrap="nowrap">
          <Avatar radius="sm" color="blue">
            V
          </Avatar>
          <Text fw={500} size="sm" truncate>
            Vantigo AS
          </Text>
        </Group>
        <IconChevronDown size={16} stroke={1.5} />
      </Group>
    </UnstyledButton>
  );
}
