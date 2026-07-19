"use client";

import { ActionIcon, Badge, Button, Card, Group, Stack, Text, Title, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
  IconDeviceDesktop,
  IconDeviceLaptop,
  IconDeviceMobile,
  IconTrash,
} from "@tabler/icons-react";
import { authClient } from "@vantigo/customers/lib/auth-client";
import { summarizeUserAgent } from "@vantigo/customers/lib/settings/user-agent";
import { useRouter } from "next/navigation";
import { useFormatter, useTranslations } from "next-intl";
import { useState } from "react";
import type { SettingsSession } from "./settings-page";

function DeviceIcon({ os }: Readonly<{ os: string | null }>) {
  if (os === "iOS" || os === "Android") return <IconDeviceMobile size={20} stroke={1.5} />;
  if (os === "macOS" || os === "Windows") return <IconDeviceLaptop size={20} stroke={1.5} />;
  return <IconDeviceDesktop size={20} stroke={1.5} />;
}

export function SessionsSection({ sessions }: Readonly<{ sessions: SettingsSession[] }>) {
  const t = useTranslations("settings.sessions");
  const format = useFormatter();
  const router = useRouter();
  const [busyToken, setBusyToken] = useState<string | null>(null);
  const [isRevokingOthers, setIsRevokingOthers] = useState(false);

  const hasOtherSessions = sessions.some((s) => !s.current);

  const handleRevoke = async (token: string) => {
    setBusyToken(token);
    const { error } = await authClient.revokeSession({ token });
    setBusyToken(null);

    if (error) {
      notifications.show({ color: "red", message: error.message ?? t("revokeError") });
      return;
    }
    notifications.show({ color: "green", message: t("revoked") });
    router.refresh();
  };

  const handleRevokeOthers = async () => {
    setIsRevokingOthers(true);
    const { error } = await authClient.revokeOtherSessions();
    setIsRevokingOthers(false);

    if (error) {
      notifications.show({ color: "red", message: error.message ?? t("revokeError") });
      return;
    }
    notifications.show({ color: "green", message: t("revokedOthers") });
    router.refresh();
  };

  return (
    <Card withBorder maw={560}>
      <Stack gap="md">
        <Group justify="space-between">
          <Title order={4}>{t("title")}</Title>
          {hasOtherSessions && (
            <Button
              variant="light"
              color="red"
              size="xs"
              loading={isRevokingOthers}
              onClick={handleRevokeOthers}
            >
              {t("revokeOthers")}
            </Button>
          )}
        </Group>

        {sessions.map((session) => {
          const { browser, os } = summarizeUserAgent(session.userAgent);
          const device = [browser, os].filter(Boolean).join(" · ") || t("unknownDevice");

          return (
            <Group key={session.id} wrap="nowrap" justify="space-between">
              <Group wrap="nowrap" gap="sm">
                <DeviceIcon os={os} />
                <div>
                  <Group gap="xs">
                    <Text size="sm" fw={500}>
                      {device}
                    </Text>
                    {session.current && (
                      <Badge size="xs" color="teal" variant="light">
                        {t("current")}
                      </Badge>
                    )}
                  </Group>
                  <Text size="xs" c="dimmed">
                    {session.ipAddress ? `${session.ipAddress} · ` : ""}
                    {t("signedInAt", {
                      date: format.dateTime(new Date(session.createdAt), {
                        dateStyle: "medium",
                        timeStyle: "short",
                      }),
                    })}
                  </Text>
                </div>
              </Group>

              {!session.current && (
                <Tooltip label={t("revoke")}>
                  <ActionIcon
                    variant="subtle"
                    color="red"
                    loading={busyToken === session.token}
                    onClick={() => handleRevoke(session.token)}
                    aria-label={t("revoke")}
                  >
                    <IconTrash size={16} stroke={1.5} />
                  </ActionIcon>
                </Tooltip>
              )}
            </Group>
          );
        })}
      </Stack>
    </Card>
  );
}
