import { Alert, Badge, Button, Card, Group, SimpleGrid, Stack, Table, Text, TextInput, Title } from "@mantine/core";
import { IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { listSystemTenants, systemTenantError } from "../api/system-tenants";
import { translateSystemModule } from "../i18n";
import "../i18n";

const Status = ({ status, t }: { status: string; t: (key: string) => string }) => (
  <Badge color={status === "Active" ? "teal" : "yellow"}>
    {t(status === "Active" ? "systemAdmin.activeStatus" : "systemAdmin.suspendedStatus")}
  </Badge>
);

export const Route = createFileRoute("/admin")({
  beforeLoad: async ({ context }) => {
    const s = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!s?.isSystemAdmin) throw (await import("@tanstack/react-router")).redirect({ to: "/" });
  },
  component: AdminOverview,
});

function AdminOverview() {
  const { t } = useI18n("host");
  const tenants = useQuery({ queryKey: ["system-tenants"], queryFn: listSystemTenants });
  const [search, setSearch] = useState("");
  if (tenants.isError)
    return (
      <Alert color="yellow" title={t("systemAdmin.tenantControlPlaneUnavailable")}>
        {systemTenantError(tenants.error)}
      </Alert>
    );
  const rows = (tenants.data ?? []).filter((tenant) =>
    `${tenant.name} ${tenant.slug}`.toLowerCase().includes(search.toLowerCase()),
  );
  const active = rows.filter((tenant) => tenant.status === "Active").length;
  return (
    <Stack gap="xl">
      <Group justify="space-between">
        <div>
          <Text tt="uppercase" size="xs" fw={700} c="dimmed">
            {t("systemAdmin.controlPlane")}
          </Text>
          <Title order={1}>{t("systemAdmin.title")}</Title>
          <Text c="dimmed">{t("systemAdmin.description")}</Text>
        </div>
        <Button component={Link} to="/admin/tenants/new" leftSection={<IconPlus size={16} />}>
          {t("systemAdmin.newTenant")}
        </Button>
      </Group>
      <SimpleGrid cols={{ base: 1, sm: 3 }}>
        <Card withBorder>
          <Text c="dimmed">{t("systemAdmin.totalTenants")}</Text>
          <Title>{tenants.data?.length ?? 0}</Title>
        </Card>
        <Card withBorder>
          <Text c="dimmed">{t("systemAdmin.active")}</Text>
          <Title c="teal">{active}</Title>
        </Card>
        <Card withBorder>
          <Text c="dimmed">{t("systemAdmin.suspended")}</Text>
          <Title c="yellow">{(tenants.data ?? []).length - active}</Title>
        </Card>
      </SimpleGrid>
      <Card withBorder>
        <Group justify="space-between" mb="md">
          <Title order={3}>{t("systemAdmin.tenants")}</Title>
          <TextInput
            placeholder={t("systemAdmin.searchNameSlug")}
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
          />
        </Group>
        <Table.ScrollContainer minWidth={700}>
          <Table striped highlightOnHover>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("systemAdmin.name")}</Table.Th>
                <Table.Th>{t("systemAdmin.slug")}</Table.Th>
                <Table.Th>{t("systemAdmin.status")}</Table.Th>
                <Table.Th>{t("systemAdmin.modules")}</Table.Th>
                <Table.Th>{t("systemAdmin.members")}</Table.Th>
                <Table.Th>{t("systemAdmin.sso")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows.map((tenant) => (
                <Table.Tr key={tenant.id}>
                  <Table.Td>
                    <Link to="/admin/tenants/$tenantId" params={{ tenantId: tenant.id }}>
                      {tenant.name}
                    </Link>
                  </Table.Td>
                  <Table.Td>
                    <Text ff="monospace" size="sm">
                      {tenant.slug}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Status status={tenant.status} t={t} />
                  </Table.Td>
                  <Table.Td>
                    {tenant.enabledModules.map((module) => (
                      <Badge key={module} variant="light" mr={4}>
                        {translateSystemModule(module, t)}
                      </Badge>
                    ))}
                  </Table.Td>
                  <Table.Td>{tenant.membershipsCount}</Table.Td>
                  <Table.Td>{tenant.ssoConfigured ? t("systemAdmin.configured") : "—"}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      </Card>
    </Stack>
  );
}
