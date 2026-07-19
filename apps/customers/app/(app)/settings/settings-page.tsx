"use client";

import { Stack, Tabs, Title } from "@mantine/core";
import { IconShieldLock, IconUserCircle } from "@tabler/icons-react";
import { resolveSettingsTab } from "@vantigo/customers/lib/settings/tabs";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { PasswordSection } from "./password-section";
import { ProfileSection } from "./profile-section";
import { SessionsSection } from "./sessions-section";

export interface SettingsUser {
  name: string;
  email: string;
  image: string | null;
  preferredCustomerType: string | null;
}

export interface SettingsSession {
  id: string;
  token: string;
  createdAt: string;
  expiresAt: string;
  ipAddress: string | null;
  userAgent: string | null;
  current: boolean;
}

export function SettingsPage({
  user,
  sessions,
}: Readonly<{ user: SettingsUser; sessions: SettingsSession[] }>) {
  const t = useTranslations("settings");
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  const activeTab = resolveSettingsTab(searchParams.get("tab"));

  const handleTabChange = (value: string | null) => {
    const tab = resolveSettingsTab(value);
    const params = new URLSearchParams(searchParams);
    params.set("tab", tab);
    router.replace(`${pathname}?${params.toString()}`, { scroll: false });
  };

  return (
    <Stack gap="lg">
      <Title order={2}>{t("title")}</Title>

      <Tabs value={activeTab} onChange={handleTabChange} keepMounted={false}>
        <Tabs.List>
          <Tabs.Tab value="general" leftSection={<IconUserCircle size={16} stroke={1.5} />}>
            {t("tabs.general")}
          </Tabs.Tab>
          <Tabs.Tab value="security" leftSection={<IconShieldLock size={16} stroke={1.5} />}>
            {t("tabs.security")}
          </Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="general" pt="lg">
          <ProfileSection user={user} />
        </Tabs.Panel>

        <Tabs.Panel value="security" pt="lg">
          <Stack gap="lg">
            <PasswordSection />
            <SessionsSection sessions={sessions} />
          </Stack>
        </Tabs.Panel>
      </Tabs>
    </Stack>
  );
}
