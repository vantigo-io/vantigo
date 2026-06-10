import {
  ActionIcon,
  AppShell,
  Avatar,
  Box,
  Group,
  Menu,
  rem,
  SimpleGrid,
  Text,
  UnstyledButton,
} from "@mantine/core";
import {
  IconFingerprint,
  IconGridDots,
  IconLogout,
  IconUser,
} from "@tabler/icons-react";
import type React from "react";

export interface UserProfile {
  name: string;
  email: string;
  image?: string;
}

export interface AuthenticatedShellProps {
  children: React.ReactNode;
  user?: UserProfile;
  currentAppName?: string;
  logoUrl?: string;
  onSignOut?: () => void;
  // A callback when navigating to different apps in the switcher
  onNavigateApp?: (appKey: string, path: string) => void;
}

function getInitials(name?: string): string {
  if (!name) return "?";
  const parts = name.trim().split(/\s+/);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0].substring(0, 2).toUpperCase();
  return (parts[0].charAt(0) + parts[parts.length - 1].charAt(0)).toUpperCase();
}

export function AuthenticatedShell({
  children,
  user,
  currentAppName = "Identity Provider",
  logoUrl = "/idp/logo.png",
  onSignOut,
  onNavigateApp,
}: AuthenticatedShellProps) {
  const handleAppClick = (appKey: string, path: string) => {
    if (onNavigateApp) {
      onNavigateApp(appKey, path);
    } else {
      window.location.href = path;
    }
  };

  return (
    <AppShell
      header={{ height: 60 }}
      padding="md"
      styles={{
        main: {
          background: "var(--mantine-color-gray-0)",
        },
      }}
    >
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          {/* Left: Brand Logo & Title */}
          <Group>
            <Box
              style={{ display: "flex", alignItems: "center", gap: rem(10) }}
            >
              <img
                src={logoUrl}
                alt="Vantigo Logo"
                style={{ height: 32, width: "auto", objectFit: "contain" }}
                onError={(e) => {
                  // Fallback if logo is missing/fails to load
                  (e.target as HTMLElement).style.display = "none";
                }}
              />
              <Text size="lg" fw={700} style={{ letterSpacing: rem(0.5) }}>
                Vantigo{" "}
                <span
                  style={{
                    fontWeight: 400,
                    color: "var(--mantine-color-gray-6)",
                  }}
                >
                  | {currentAppName}
                </span>
              </Text>
            </Box>
          </Group>

          {/* Right: Actions (App Switcher & User Avatar Menu) */}
          <Group gap="sm">
            {/* Google-style App Switcher */}
            <Menu
              shadow="md"
              width={280}
              position="bottom-end"
              transitionProps={{ transition: "pop-top-right" }}
            >
              <Menu.Target>
                <ActionIcon
                  variant="subtle"
                  color="gray"
                  size="lg"
                  radius="xl"
                  title="Applications"
                >
                  <IconGridDots size={20} />
                </ActionIcon>
              </Menu.Target>

              <Menu.Dropdown p="xs">
                <Text size="xs" fw={700} color="dimmed" px="xs" py="xs">
                  Vantigo Apps
                </Text>
                <SimpleGrid cols={3} spacing="xs" p="xs">
                  {/* IDP Application */}
                  <UnstyledButton
                    onClick={() => handleAppClick("idp", "/idp/account")}
                    style={{
                      display: "flex",
                      flexDirection: "column",
                      alignItems: "center",
                      justifyContent: "center",
                      padding: rem(10),
                      borderRadius: rem(8),
                      textAlign: "center",
                      backgroundColor: "var(--mantine-color-blue-light)",
                    }}
                  >
                    <IconFingerprint
                      size={28}
                      color="var(--mantine-color-blue-filled)"
                    />
                    <Text size="xs" fw={500} mt={5} truncate>
                      Accounts
                    </Text>
                  </UnstyledButton>
                </SimpleGrid>
              </Menu.Dropdown>
            </Menu>

            {/* User Profile Menu */}
            {user && (
              <Menu shadow="md" width={200} position="bottom-end">
                <Menu.Target>
                  <UnstyledButton
                    style={{
                      borderRadius: "50%",
                      padding: 2,
                      display: "flex",
                      alignItems: "center",
                      justifyContent: "center",
                    }}
                  >
                    <Avatar
                      src={user.image}
                      radius="xl"
                      color="blue"
                      alt={user.name}
                    >
                      {getInitials(user.name)}
                    </Avatar>
                  </UnstyledButton>
                </Menu.Target>

                <Menu.Dropdown>
                  <Box px="xs" py="sm">
                    <Text size="sm" fw={500} truncate>
                      {user.name}
                    </Text>
                    <Text size="xs" color="dimmed" truncate>
                      {user.email}
                    </Text>
                  </Box>

                  <Menu.Divider />

                  <Menu.Item
                    leftSection={<IconUser size={14} />}
                    onClick={() => handleAppClick("idp", "/idp/account")}
                  >
                    My Account
                  </Menu.Item>

                  <Menu.Divider />

                  <Menu.Item
                    color="red"
                    leftSection={<IconLogout size={14} />}
                    onClick={onSignOut}
                  >
                    Sign out
                  </Menu.Item>
                </Menu.Dropdown>
              </Menu>
            )}
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Main>
        <Box py="lg" style={{ maxWidth: 1200, margin: "0 auto" }}>
          {children}
        </Box>
      </AppShell.Main>
    </AppShell>
  );
}
