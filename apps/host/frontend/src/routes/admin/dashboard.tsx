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
  const status = useQuery({ queryKey: ["owner-system-status"], queryFn: getIdentitySystemStatus });
  const loading = status.isPending;
  const failed = status.isError;
  const ssoLastUsed = status.data && date(status.data.lastStaticOidcSignInAtUtc);
  const scimLastUsed = status.data && date(status.data.lastAuthenticatedScimRequestAtUtc);
  const retry = () => void status.refetch();

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
          <Group justify="space-between" align="center">
            <Text size="sm">No configuration details are shown here.</Text>
            <Button size="compact-sm" variant="light" onClick={retry}>
              Try again
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
            <ManagementCard
              title="People"
              icon={IconUsers}
              description="Manage team accounts, access, and sign-in security."
            >
              <SimpleGrid cols={{ base: 2, xs: 4 }}>
                <Metric label="Total" value={status.data?.total ?? "—"} />
                <Metric label="Active" value={status.data?.active ?? "—"} />
                <Metric label="Disabled" value={status.data?.disabled ?? "—"} />
                <Metric label="Pending invitations" value={status.data?.pendingInvitations ?? "—"} />
              </SimpleGrid>
              <Anchor component={Link} to="/admin/users" size="sm" mt="lg" display="block">
                View users
              </Anchor>
            </ManagementCard>
            <ManagementCard
              title="Invitations"
              icon={IconUsers}
              description="Keep new team members moving through onboarding."
            >
              <Group justify="space-between">
                <Metric label="Pending invitations" value={status.data?.pendingInvitations ?? "—"} />
                <Anchor component={Link} to="/admin/invitations" size="sm">
                  Manage invitations
                </Anchor>
              </Group>
            </ManagementCard>
            <ManagementCard
              title="Access control"
              icon={IconUsers}
              description="Review roles, assignments, and delegated administration."
            >
              <Anchor component={Link} to="/admin/roles" size="sm">
                Manage roles &amp; access
              </Anchor>
            </ManagementCard>
            <StatusCard title="Identity integrations" icon={IconCloudLock}>
              <Stack gap="sm">
                <Group justify="space-between">
                  <Text>SSO</Text>
                  <Badge color={status.data?.staticOidcEnabled ? "teal" : "gray"}>
                    {providerName(status.data?.staticOidcProvider ?? null)}
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
                  <Badge color={status.data?.staticScimEnabled ? "teal" : "gray"}>
                    {status.data?.staticScimEnabled ? "Enabled" : "Disabled"}
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
        </Stack>
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
