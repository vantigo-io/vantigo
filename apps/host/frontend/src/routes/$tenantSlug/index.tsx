import { Alert, Button, Card, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import { IconBolt, IconMessage, IconPackage, IconUsers } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, useParams } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import "../../i18n";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import {
  enabledModuleKeys,
  fetchTenantCapabilities,
  type ModuleKey,
  tenantCapabilitiesQueryKey,
} from "../../api/tenant-capabilities";
import { hasPermissions } from "../../navigation";

const moduleCards = [
  {
    title: "dashboard.customers",
    description: "dashboard.manageCustomers",
    path: "/customers",
    icon: IconUsers,
    module: "customers" as ModuleKey,
    requiredPermissions: ["customers:view"],
  },
  {
    title: "dashboard.communications",
    description: "dashboard.reviewMessages",
    path: "/inbox",
    icon: IconMessage,
    module: "communications" as ModuleKey,
    requiredPermissions: ["communications:conversations-view"],
  },
  {
    title: "dashboard.products",
    description: "dashboard.manageProducts",
    path: "/products",
    icon: IconPackage,
    module: "products" as ModuleKey,
    requiredPermissions: [
      "products:products-view",
      "products:variants-view",
      "products:pricing-view",
      "products:categories-view",
      "products:tax-categories-view",
    ],
  },
  {
    title: "dashboard.energy",
    description: "dashboard.manageEnergy",
    path: "/energy/metering-points",
    icon: IconBolt,
    module: "energy" as ModuleKey,
    requiredPermissions: ["energy:metering-points-view", "energy:meters-view"],
  },
] as const;

const DashboardPage = () => {
  const { t } = useI18n("host");
  const { tenantSlug } = useParams({ from: "/$tenantSlug" });
  const { data: session } = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const capabilities = useQuery({
    queryKey: tenantCapabilitiesQueryKey(session?.activeTenantId ?? undefined),
    queryFn: fetchTenantCapabilities,
    enabled: !!session?.activeTenantId,
    retry: false,
    staleTime: 300_000,
  });
  const authorization = useQuery({
    queryKey: ["authorization", "me", session?.activeTenantId ?? "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session,
    retry: false,
    staleTime: 300_000,
  });
  const enabledModules = enabledModuleKeys(capabilities.data) ?? [];
  const permissions = authorization.data?.permissions;
  const modules = moduleCards.filter(
    (card) => enabledModules.includes(card.module) && hasPermissions(permissions, card.requiredPermissions),
  );
  const settled = !capabilities.isPending && !authorization.isPending;

  return (
    <Stack gap="lg">
      <Title order={2}>{t("dashboard.title")}</Title>
      {modules.length > 0 && <Text c="dimmed">{t("dashboard.chooseModule")}</Text>}
      {settled && modules.length === 0 && (
        <Alert color="gray" variant="light" title={t("noAccessTitle")}>
          {t("noAccessBody")}
        </Alert>
      )}
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
