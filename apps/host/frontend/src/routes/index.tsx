import { Button, Card, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import { IconMessage, IconPackage, IconUsers } from "@tabler/icons-react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";

const modules = [
  { title: "dashboard.customers", description: "dashboard.manageCustomers", to: "/customers", icon: IconUsers },
  {
    title: "dashboard.communications",
    description: "dashboard.reviewMessages",
    to: "/messages",
    icon: IconMessage,
  },
  { title: "dashboard.products", description: "dashboard.manageProducts", to: "/products", icon: IconPackage },
] as const;

const DashboardPage = () => {
  const { t } = useI18n("host");
  return (
    <Stack gap="lg">
      <Title order={2}>{t("dashboard.title")}</Title>
      <Text c="dimmed">{t("dashboard.chooseModule")}</Text>
      <SimpleGrid cols={{ base: 1, sm: 3 }}>
        {modules.map((module) => (
          <Card key={module.to} withBorder>
            <Stack>
              <module.icon size={28} />
              <Title order={4}>{t(module.title)}</Title>
              <Text c="dimmed" size="sm">
                {t(module.description)}
              </Text>
              <Button component={Link} to={module.to}>
                {t("dashboard.open")}
              </Button>
            </Stack>
          </Card>
        ))}
      </SimpleGrid>
    </Stack>
  );
};
export const Route = createFileRoute("/")({ component: DashboardPage });
