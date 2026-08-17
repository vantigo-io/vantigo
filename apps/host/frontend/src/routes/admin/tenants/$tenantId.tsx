import { Alert, Badge, Button, Card, Group, Modal, Stack, Text, TextInput, Title } from "@mantine/core";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  getOffboarding,
  getSystemTenant,
  reactivateSystemTenant,
  requestExport,
  requestPurge,
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
  const offboarding = useQuery({ queryKey: ["offboarding", tenantId], queryFn: () => getOffboarding(tenantId) });
  const [confirm, setConfirm] = useState(false);
  const [purge, setPurge] = useState("");
  const [token, setToken] = useState("");
  const exportMutation = useMutation({
    mutationFn: () => requestExport(tenantId),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["offboarding", tenantId] }),
  });
  const suspend = useMutation({
    mutationFn: () =>
      tenant.data?.status === "Active" ? suspendSystemTenant(tenantId) : reactivateSystemTenant(tenantId),
    onSuccess: () => {
      setConfirm(false);
      void qc.invalidateQueries({ queryKey: ["system-tenant", tenantId] });
    },
  });
  const purgeMutation = useMutation({
    mutationFn: () =>
      requestPurge(tenantId, {
        exportRequestId: offboarding.data?.exportId ?? "",
        purgeToken: token,
        confirmation: purge,
      }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["offboarding", tenantId] }),
  });
  if (tenant.isError) return <Alert color="red">{systemTenantError(tenant.error)}</Alert>;
  if (!tenant.data) return <Text>{t("systemAdmin.loadingTenant")}</Text>;
  const exact = purge === `PURGE ${tenant.data.slug}`;
  const statusKey = tenant.data.status === "Active" ? "systemAdmin.activeStatus" : "systemAdmin.suspendedStatus";
  const sso = tenant.data.ssoConfigured ? t("systemAdmin.configured") : t("systemAdmin.notConfigured");
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
          <Text>{t("systemAdmin.membersAndSso", { members: tenant.data.membershipsCount, sso })}</Text>
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
      <Card withBorder>
        <Stack>
          <Title order={3}>{t("systemAdmin.dangerZone")}</Title>
          <Text c="dimmed">{t("systemAdmin.offboardingDescription")}</Text>
          {offboarding.data?.status === "not_requested" && (
            <Button onClick={() => exportMutation.mutate()} loading={exportMutation.isPending} w="fit-content">
              {t("systemAdmin.requestExport")}
            </Button>
          )}
          {offboarding.data?.purgeToken && (
            <Alert color="yellow" title={t("systemAdmin.purgeToken")}>
              {t("systemAdmin.saveToken", { token: offboarding.data.purgeToken })}
            </Alert>
          )}
          {offboarding.data?.status !== "not_requested" && (
            <>
              <TextInput
                label={t("systemAdmin.typePurge", { confirmation: `PURGE ${tenant.data.slug}` })}
                value={purge}
                onChange={(e) => setPurge(e.currentTarget.value)}
              />
              <TextInput
                label={t("systemAdmin.purgeToken")}
                value={token}
                onChange={(e) => setToken(e.currentTarget.value)}
              />
              <Button
                color="red"
                disabled={!exact || !token}
                loading={purgeMutation.isPending}
                onClick={() => purgeMutation.mutate()}
                w="fit-content"
              >
                {t("systemAdmin.requestPurge")}
              </Button>
            </>
          )}
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
