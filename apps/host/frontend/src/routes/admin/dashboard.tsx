import { Alert, Anchor, Badge, Card, Group, SimpleGrid, Stack, Text, ThemeIcon, Title } from "@mantine/core";
import { IconActivity, IconCloudLock, IconDatabase, IconUsers } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, redirect } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getIdentitySystemStatus } from "../../api/system-status";

const date = (value: string | null) =>
  value
    ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value))
    : null;
const providerName = (value: string | null) => {
  if (!value) return "Disabled";
  if (value.toLowerCase().includes("entra") || value.toLowerCase().includes("azure")) return "Microsoft Entra ID";
  if (value.toLowerCase().includes("google")) return "Google Workspace";
  return "Enabled";
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
  const status = useQuery({ queryKey: ["owner-system-status"], queryFn: getIdentitySystemStatus });
  const loading = status.isPending;
  const failed = status.isError;
  const ssoLastUsed = status.data && date(status.data.lastStaticOidcSignInAtUtc);
  const scimLastUsed = status.data && date(status.data.lastAuthenticatedScimRequestAtUtc);

  return (
    <Stack maw={1100} mx="auto" gap="xl">
      <div>
        <Title order={2}>Admin dashboard</Title>
        <Text c="dimmed" mt={4}>
          A read-only overview of accounts and identity integrations.
        </Text>
      </div>
      {failed && (
        <Alert color="red" title="Status could not be loaded">
          Try refreshing the page. No configuration details are shown here.
        </Alert>
      )}
      {loading && <Text c="dimmed">Loading system status…</Text>}
      {!loading && !failed && status.data && (
        <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="lg">
          <StatusCard title="User accounts" icon={IconUsers}>
            <SimpleGrid cols={{ base: 2, xs: 4 }}>
              <Metric label="Total" value={status.data.total} />
              <Metric label="Active" value={status.data.active} />
              <Metric label="Disabled" value={status.data.disabled} />
              <Metric label="Pending invitations" value={status.data.pendingInvitations} />
            </SimpleGrid>
            <Anchor component={Link} to="/admin/users" size="sm" mt="lg" display="block">
              View users
            </Anchor>
          </StatusCard>
          <StatusCard title="Identity integrations" icon={IconCloudLock}>
            <Stack gap="sm">
              <Group justify="space-between">
                <Text>SSO</Text>
                <Badge color={status.data.staticOidcEnabled ? "teal" : "gray"}>
                  {providerName(status.data.staticOidcProvider)}
                </Badge>
              </Group>
              <Group justify="space-between">
                <Text>Client authentication</Text>
                <Text size="sm" c="dimmed">
                  Managed by deployment
                </Text>
              </Group>
              <Group justify="space-between">
                <Text>Last successful SSO use</Text>
                <Text size="sm" c="dimmed">
                  {ssoLastUsed ?? "No successful sign-ins yet"}
                </Text>
              </Group>
            </Stack>
          </StatusCard>
          <StatusCard title="SCIM provisioning" icon={IconDatabase}>
            <Stack gap="sm">
              <Group justify="space-between">
                <Text>Status</Text>
                <Badge color={status.data.staticScimEnabled ? "teal" : "gray"}>
                  {status.data.staticScimEnabled ? "Enabled" : "Disabled"}
                </Badge>
              </Group>
              <Group justify="space-between">
                <Text>Last authenticated request</Text>
                <Text size="sm" c="dimmed">
                  {scimLastUsed ?? "No authenticated requests yet"}
                </Text>
              </Group>
            </Stack>
          </StatusCard>
          <StatusCard title="Operational activity" icon={IconActivity}>
            <Text c="dimmed">
              This overview reports only non-sensitive identity activity. Credentials, authorities, and provider
              configuration details are intentionally not displayed.
            </Text>
          </StatusCard>
        </SimpleGrid>
      )}
    </Stack>
  );
};

export const Route = createFileRoute("/admin/dashboard")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
  },
  component: AdminDashboardPage,
});
