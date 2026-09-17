import {
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  Group,
  SimpleGrid,
  Skeleton,
  Stack,
  Text,
  ThemeIcon,
  Title,
} from "@mantine/core";
import { IconActivity, IconCloudLock, IconDatabase, IconUsers } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, redirect } from "@tanstack/react-router";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getIdentitySystemStatus } from "../../api/system-status";
import "../../i18n";

const providerName = (value: string | null, t: (key: string) => string) => {
  if (!value) return t("common.disabled");
  if (value.toLowerCase().includes("entra") || value.toLowerCase().includes("azure")) return "Microsoft Entra ID";
  if (value.toLowerCase().includes("google")) return "Google Workspace";
  return t("common.enabled");
};

const StatusCard = ({
  title,
  icon: Icon,
  children,
}: {
  title: string;
  icon: typeof IconUsers;
  children: ReactNode;
}) => (
  <Card withBorder radius="md" padding="lg">
    <Group gap="sm" mb="lg">
      <ThemeIcon variant="light" size="lg">
        <Icon size={20} />
      </ThemeIcon>
      <Title order={3} fz="h4">
        {title}
      </Title>
    </Group>
    {children}
  </Card>
);
const ManagementCard = ({
  title,
  icon: Icon,
  description,
  children,
}: {
  title: string;
  icon: typeof IconUsers;
  description: string;
  children: ReactNode;
}) => (
  <Card withBorder radius="md" padding="lg">
    <Group gap="sm" mb="sm">
      <ThemeIcon variant="light" size="lg">
        <Icon size={20} />
      </ThemeIcon>
      <Title order={3} fz="h4">
        {title}
      </Title>
    </Group>
    <Text size="sm" c="dimmed" mb="lg">
      {description}
    </Text>
    {children}
  </Card>
);
const Metric = ({ label, value }: { label: string; value: string | number }) => (
  <div>
    <Text size="sm" c="dimmed">
      {label}
    </Text>
    <Text size="xl" fw={700}>
      {value}
    </Text>
  </div>
);

const AdminDashboardPage = () => {
  const { t, formatters } = useI18n("host");
  const date = (value: string | null) =>
    value ? formatters.formatDate(new Date(value), { dateStyle: "medium", timeStyle: "short" }) : null;
  const status = useQuery({ queryKey: ["owner-system-status"], queryFn: getIdentitySystemStatus });
  const loading = status.isPending;
  const failed = status.isError;
  const ssoLastUsed = status.data && date(status.data.lastStaticOidcSignInAtUtc);
  const scimLastUsed = status.data && date(status.data.lastAuthenticatedScimRequestAtUtc);
  const retry = () => void status.refetch();

  return (
    <Stack maw={1100} mx="auto" gap="xl">
      <PageHeader
        eyebrow={t("navigation.workspaceAdmin")}
        title={t("admin.dashboard")}
        description={t("admin.overview")}
      />
      {failed && (
        <Alert color="red" title={t("admin.statusLoadFailed")}>
          <Group justify="space-between" align="center">
            <Text size="sm">{t("admin.noConfiguration")}</Text>
            <Button size="compact-sm" variant="light" onClick={retry}>
              {t("common.tryAgain")}
            </Button>
          </Group>
        </Alert>
      )}
      {loading && (
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="lg">
          {[1, 2, 3, 4].map((n) => (
            <Card key={n} withBorder padding="lg">
              <Skeleton height={24} width="55%" mb="lg" />
              <Skeleton height={16} mb="sm" />
              <Skeleton height={16} width="75%" />
            </Card>
          ))}
        </SimpleGrid>
      )}
      {!loading && (
        <Stack gap="lg">
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="lg">
            <ManagementCard title={t("admin.people")} icon={IconUsers} description={t("admin.peopleDescription")}>
              <SimpleGrid cols={{ base: 2, xs: 4 }}>
                <Metric label={t("admin.total")} value={status.data?.total ?? "—"} />
                <Metric label={t("admin.active")} value={status.data?.active ?? "—"} />
                <Metric label={t("common.disabled")} value={status.data?.disabled ?? "—"} />
                <Metric label={t("admin.pendingInvitations")} value={status.data?.pendingInvitations ?? "—"} />
              </SimpleGrid>
              <Anchor component={Link} to="/workspace/users" size="sm" mt="lg" display="block">
                {t("admin.viewUsers")}
              </Anchor>
            </ManagementCard>
            <ManagementCard
              title={t("admin.invitations")}
              icon={IconUsers}
              description={t("admin.invitationsDescription")}
            >
              <Group justify="space-between">
                <Metric label={t("admin.pendingInvitations")} value={status.data?.pendingInvitations ?? "—"} />
                <Anchor component={Link} to="/workspace/invitations" size="sm">
                  {t("admin.manageInvitations")}
                </Anchor>
              </Group>
            </ManagementCard>
            <ManagementCard
              title={t("admin.accessControl")}
              icon={IconUsers}
              description={t("admin.accessControlDescription")}
            >
              <Anchor component={Link} to="/workspace/roles" size="sm">
                {t("admin.manageRoles")}
              </Anchor>
            </ManagementCard>
            <StatusCard title={t("admin.identityIntegrations")} icon={IconCloudLock}>
              <Stack gap="sm">
                <Group justify="space-between">
                  <Text>{t("admin.sso")}</Text>
                  <Badge color={status.data?.staticOidcEnabled ? "teal" : "gray"}>
                    {providerName(status.data?.staticOidcProvider ?? null, t)}
                  </Badge>
                </Group>
                <Group justify="space-between">
                  <Text>{t("admin.clientAuthentication")}</Text>
                  <Text size="sm" c="dimmed">
                    {t("admin.managedByDeployment")}
                  </Text>
                </Group>
                <Group justify="space-between">
                  <Text>{t("admin.lastSsoUse")}</Text>
                  <Text size="sm" c="dimmed">
                    {ssoLastUsed ?? t("admin.noSuccessfulSignIns")}
                  </Text>
                </Group>
              </Stack>
            </StatusCard>
            <StatusCard title={t("admin.scim")} icon={IconDatabase}>
              <Stack gap="sm">
                <Group justify="space-between">
                  <Text>{t("admin.status")}</Text>
                  <Badge color={status.data?.staticScimEnabled ? "teal" : "gray"}>
                    {status.data?.staticScimEnabled ? t("common.enabled") : t("common.disabled")}
                  </Badge>
                </Group>
                <Group justify="space-between">
                  <Text>{t("admin.lastAuthenticatedRequest")}</Text>
                  <Text size="sm" c="dimmed">
                    {scimLastUsed ?? t("admin.noAuthenticatedRequests")}
                  </Text>
                </Group>
              </Stack>
            </StatusCard>
            <StatusCard title={t("admin.operationalActivity")} icon={IconActivity}>
              <Text c="dimmed">{t("admin.activityDescription")}</Text>
            </StatusCard>
          </SimpleGrid>
        </Stack>
      )}
    </Stack>
  );
};

export const Route = createFileRoute("/workspace/overview")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
  },
  component: AdminDashboardPage,
});
