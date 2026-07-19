"use client";

import { AppShell, Burger, Group, NavLink, Text } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { IconAddressBook, IconLayoutDashboard, IconUsers } from "@tabler/icons-react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useTranslations } from "next-intl";
import { OrgSwitcher } from "./org-switcher";
import { type ShellUser, UserButton } from "./user-button";

export function Shell({
  user,
  children,
}: Readonly<{
  user: ShellUser;
  children: React.ReactNode;
}>) {
  const [opened, { toggle, close }] = useDisclosure();
  const pathname = usePathname();
  const t = useTranslations();

  const navItems = [
    { href: "/", label: t("nav.dashboard"), icon: IconLayoutDashboard, active: pathname === "/" },
    {
      href: "/customers",
      label: t("nav.customers"),
      icon: IconUsers,
      active: pathname.startsWith("/customers"),
    },
    {
      href: "/contacts",
      label: t("nav.contacts"),
      icon: IconAddressBook,
      active: pathname.startsWith("/contacts"),
    },
  ];

  return (
    <AppShell
      header={{ height: 60 }}
      navbar={{ width: 280, breakpoint: "sm", collapsed: { mobile: !opened } }}
      padding="md"
    >
      <AppShell.Header>
        <Group h="100%" px="md">
          <Burger opened={opened} onClick={toggle} hiddenFrom="sm" size="sm" />
          <Text fw={700}>{t("common.appName")}</Text>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="md">
        <OrgSwitcher />

        <AppShell.Section grow mt="md">
          {navItems.map((item) => (
            <NavLink
              key={item.href}
              component={Link}
              href={item.href}
              label={item.label}
              leftSection={<item.icon size={18} stroke={1.5} />}
              active={item.active}
              onClick={close}
            />
          ))}
        </AppShell.Section>

        <AppShell.Section>
          <UserButton user={user} />
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main>{children}</AppShell.Main>
    </AppShell>
  );
}
