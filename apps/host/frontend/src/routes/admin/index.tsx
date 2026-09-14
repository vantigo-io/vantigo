import { Alert, Badge, Button, Card, Group, SimpleGrid, Stack, Table, Text, TextInput, Title } from "@mantine/core";
import { IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { translateSystemModule } from "../../i18n";
import { MaintenanceControls } from "./-maintenance-controls";
import "../../i18n";

// The system-tenants API was deleted (task 2 of the frontend de-tenanting
// plan — every /api/v1/identity/admin/tenants* endpoint is absent from both
// the contract and the Go server). The tenant list below is kept structurally
// in place for task 6, which trims this page to its maintenance-controls
// half; until then it always reports the control plane as unavailable
// instead of porting the deleted endpoint back.
interface SystemTenant {
  id: string;
  name: string;
  slug: string;
  status: string;
  enabledModules: string[];
  membershipsCount: number;
}
const systemTenantError = (error: unknown) =>
  error instanceof Error ? error.message : "The request could not be completed.";

const Status = ({ status, t }: { status: string; t: (key: string) => string }) => (
  <Badge color={status === "Active" ? "teal" : "yellow"}>
    {t(status === "Active" ? "systemAdmin.activeStatus" : "systemAdmin.suspendedStatus")}
  </Badge>
);

export const Route = createFileRoute("/admin/")({
  component: AdminOverview,
});

export function AdminOverview() {
  const { t } = useI18n("host");
  const tenants = useQuery<SystemTenant[]>({
    queryKey: ["system-tenants"],
    queryFn: () => Promise.reject(new Error("Tenant administration is not available.")),
    retry: false,
  });
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
        <Button leftSection={<IconPlus size={16} />} disabled>
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
      <MaintenanceControls />
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
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows.map((tenant) => (
                <Table.Tr key={tenant.id}>
                  <Table.Td>{tenant.name}</Table.Td>
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
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      </Card>
    </Stack>
  );
}
