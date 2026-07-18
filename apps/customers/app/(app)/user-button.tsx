"use client";

import { Avatar, Box, Group, Menu, Text, UnstyledButton } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconChevronRight, IconLogout } from "@tabler/icons-react";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { LanguageToggle } from "@vantigo/customers/lib/i18n/language-toggle";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useState } from "react";

export interface ShellUser {
  name: string;
  email: string;
  image?: string;
}

export function UserButton({ user }: Readonly<{ user: ShellUser }>) {
  const t = useTranslations("userMenu");
  const router = useRouter();
  const [isSigningOut, setIsSigningOut] = useState(false);

  const handleSignOut = async () => {
    if (isSigningOut) return;
    setIsSigningOut(true);

    const { error } = await authClient.signOut();

    if (error) {
      setIsSigningOut(false);
      notifications.show({
        color: "red",
        message: error.message ?? t("signOutError"),
      });
      return;
    }

    router.push("/sign-in");
    router.refresh();
  };

  return (
    <Menu width={260} position="right-end" withinPortal>
      <Menu.Target>
        <UnstyledButton w="100%" p="xs">
          <Group wrap="nowrap">
            <Avatar src={user.image} name={user.name} color="initials" radius="xl" />
            <Box flex={1} miw={0}>
              <Text size="sm" fw={500} truncate>
                {user.name}
              </Text>
              <Text size="xs" c="dimmed" truncate>
                {user.email}
              </Text>
            </Box>
            <IconChevronRight size={14} stroke={1.5} />
          </Group>
        </UnstyledButton>
      </Menu.Target>

      <Menu.Dropdown>
        <Menu.Label>{t("language")}</Menu.Label>
        <Box px="xs" pb="xs">
          <LanguageToggle />
        </Box>

        <Menu.Divider />

        <Menu.Item
          color="red"
          leftSection={<IconLogout size={16} stroke={1.5} />}
          disabled={isSigningOut}
          onClick={handleSignOut}
        >
          {t("signOut")}
        </Menu.Item>
      </Menu.Dropdown>
    </Menu>
  );
}
