import { Alert, Badge, Button, Card, Group, Modal, Stack, Text, Title } from "@mantine/core";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  getSystemTenant,
  reactivateSystemTenant,
  suspendSystemTenant,
  systemTenantError,
} from "../../../api/system-tenants";
import { translateSystemModule } from "../../../i18n";
import "../../../i18n";

export const Route = createFileRoute("/admin/tenants/$tenantId")({ component: TenantDetail });

function TenantDetail() {
  const { t } = useI18n("host");
  const { tenantId } = Route.useParams();
  const qc = useQueryClient();
  const tenant = useQuery({ queryKey: ["system-tenant", tenantId], queryFn: () => getSystemTenant(tenantId) });
  const [confirm, setConfirm] = useState(false);
  const suspend = useMutation({
    mutationFn: () =>
      tenant.data?.status === "Active" ? suspendSystemTenant(tenantId) : reactivateSystemTenant(tenantId),
    onSuccess: () => {
      setConfirm(false);
      void qc.invalidateQueries({ queryKey: ["system-tenant", tenantId] });
    },
  });
  if (tenant.isError) return <Alert color="red">{systemTenantError(tenant.error)}</Alert>;
  if (!tenant.data) return <Text>{t("systemAdmin.loadingTenant")}</Text>;
  const statusKey = tenant.data.status === "Active" ? "systemAdmin.activeStatus" : "systemAdmin.suspendedStatus";
  return (
    <Stack maw={850}>
      <Group justify="space-between">
        <div>
          <Text c="dimmed" size="sm">
            {t("systemAdmin.tenant")}
          </Text>
          <Title>{tenant.data.name}</Title>
          <Text ff="monospace">{tenant.data.slug}</Text>
        </div>
        <Badge color={tenant.data.status === "Active" ? "teal" : "yellow"}>{t(statusKey)}</Badge>
      </Group>
      <Card withBorder>
        <Stack>
          <Title order={3}>{t("systemAdmin.general")}</Title>
          <Text>
            {t("systemAdmin.enabledModules", {
              modules:
                tenant.data.enabledModules.map((module) => translateSystemModule(module, t)).join(", ") ||
                t("systemAdmin.none"),
            })}
          </Text>
          <Text>{t("systemAdmin.membersCount", { members: tenant.data.membershipsCount })}</Text>
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Title order={3}>{t("systemAdmin.status")}</Title>
          <Text c="dimmed">{t("systemAdmin.statusDescription")}</Text>
          <Button
            color={tenant.data.status === "Active" ? "yellow" : "teal"}
            onClick={() => setConfirm(true)}
            w="fit-content"
          >
            {t(tenant.data.status === "Active" ? "systemAdmin.suspendTenant" : "systemAdmin.reactivateTenant")}
          </Button>
        </Stack>
      </Card>
      <Modal opened={confirm} onClose={() => setConfirm(false)} title={t("systemAdmin.confirmStatusChange")}>
        <Text mb="md">{t("systemAdmin.changeStatusQuestion")}</Text>
        <Button
          color={tenant.data.status === "Active" ? "yellow" : "teal"}
          loading={suspend.isPending}
          onClick={() => suspend.mutate()}
        >
          {t("systemAdmin.confirm")}
        </Button>
      </Modal>
    </Stack>
  );
}
