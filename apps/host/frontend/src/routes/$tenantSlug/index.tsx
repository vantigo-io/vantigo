import { Button, Card, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import { IconMessage, IconPackage, IconUsers } from "@tabler/icons-react";
import { createFileRoute, Link, useParams } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import "../../i18n";

const DashboardPage = () => {
  const { t } = useI18n("host");
  const { tenantSlug } = useParams({ from: "/$tenantSlug" });
  const modules = [
    { title: "dashboard.customers", description: "dashboard.manageCustomers", path: "/customers", icon: IconUsers },
    { title: "dashboard.communications", description: "dashboard.reviewMessages", path: "/inbox", icon: IconMessage },
    { title: "dashboard.products", description: "dashboard.manageProducts", path: "/products", icon: IconPackage },
  ] as const;

  return (
    <Stack gap="lg">
      <Title order={2}>{t("dashboard.title")}</Title>
      <Text c="dimmed">{t("dashboard.chooseModule")}</Text>
      <SimpleGrid cols={{ base: 1, sm: 3 }}>
        {modules.map((module) => (
          <Card key={module.path} withBorder>
            <Stack>
              <module.icon size={28} />
              <Title order={4}>{t(module.title)}</Title>
              <Text c="dimmed" size="sm">
                {t(module.description)}
              </Text>
              <Button component={Link} to={`/${encodeURIComponent(tenantSlug)}${module.path}` as never}>
                {t("dashboard.open")}
              </Button>
            </Stack>
          </Card>
        ))}
      </SimpleGrid>
    </Stack>
  );
};

export const Route = createFileRoute("/$tenantSlug/")({ component: DashboardPage });
