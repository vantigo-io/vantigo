import { Card, Divider, Group, Stack, Text } from "@mantine/core";
import { useTranslation } from "react-i18next";

interface ProfileCardProps {
  user: {
    name: string;
    email: string;
  };
}

export function ProfileCard({ user }: ProfileCardProps) {
  const { t } = useTranslation();

  return (
    <Card withBorder shadow="sm" radius="md" p="xl">
      <Text fw={700} size="lg" mb="md">
        {t("profileInfo")}
      </Text>
      <Divider mb="lg" />
      <Stack gap="md">
        <Group justify="space-between">
          <Text fw={500}>{t("name")}</Text>
          <Text color="dimmed">{user.name}</Text>
        </Group>
        <Group justify="space-between">
          <Text fw={500}>{t("email")}</Text>
          <Text color="dimmed">{user.email}</Text>
        </Group>
      </Stack>
    </Card>
  );
}
