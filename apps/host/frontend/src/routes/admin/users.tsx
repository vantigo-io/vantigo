import {
  ActionIcon,
  Alert,
  Anchor,
  Avatar,
  Badge,
  Button,
  Card,
  Group,
  Menu,
  Modal,
  PasswordInput,
  SegmentedControl,
  Select,
  SimpleGrid,
  Skeleton,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { useDisclosure } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconDots,
  IconLock,
  IconMail,
  IconPlus,
  IconShieldCheck,
  IconTrash,
  IconUserCheck,
  IconUserOff,
  IconUsers,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, redirect } from "@tanstack/react-router";
import { appUrl } from "@vantigo/frontend-shell";
import { useMemo, useState } from "react";
import { createInvitation, listInvitations } from "../../api/account-lifecycle";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import {
  createUser,
  deleteUser,
  disableUser,
  enableUser,
  listUsers,
  type ManagedUser,
  sendPasswordReset,
  setUserPassword,
  type UserRole,
  updateUser,
} from "../../api/users";
import { showLifecycleFormError } from "../../lib/lifecycle-form-errors";

const roles = ["User", "Owner"];
const passwordHelp = "12+ characters with upper and lower case letters and a digit. The server remains the authority.";

const UsersPage = () => {
  const qc = useQueryClient();
  const session = qc.getQueryData<Awaited<ReturnType<typeof fetchSession>>>(sessionQueryKey);
  const users = useQuery({ queryKey: ["owner-users"], queryFn: listUsers });
  const invitations = useQuery({ queryKey: ["owner-invitations"], queryFn: listInvitations });
  const [search, setSearch] = useState("");
  const [metricFilter, setMetricFilter] = useState<string | null>(null);
  const [mode, setMode] = useState<"invite" | "initial">("invite");
  const [opened, { open, close }] = useDisclosure(false);
  const [editing, setEditing] = useState<ManagedUser | null>(null);
  const [passwordUser, setPasswordUser] = useState<ManagedUser | null>(null);
  const form = useForm({
    initialValues: { displayName: "", email: "", role: "User" as UserRole, password: "" },
    validate: {
      email: (v) => (/^\S+@\S+$/.test(v) ? undefined : "Enter a valid email"),
      password: (v) =>
        !editing && mode === "initial" && !passwordValid(v)
          ? "Use 12+ characters with upper and lower case letters and a digit"
          : undefined,
    },
  });
  const passwordForm = useForm({
    initialValues: { password: "" },
    validate: {
      password: (v) =>
        passwordValid(v) ? undefined : "Use 12+ characters with upper and lower case letters and a digit",
    },
  });
  const refresh = () => void qc.invalidateQueries({ queryKey: ["owner-users"] });
  const self = (u: ManagedUser) => u.id === session?.user.id;
  const mutationError = (title: string) => (error: unknown) =>
    notifications.show({
      title,
      message: error instanceof Error ? error.message : "The request could not be completed.",
      color: "red",
    });
  const change = useMutation({
    mutationFn: async (values: typeof form.values) =>
      editing
        ? updateUser(editing.id, { displayName: values.displayName, email: values.email, role: values.role })
        : mode === "invite"
          ? createInvitation({ displayName: values.displayName, email: values.email, role: values.role })
          : createUser(values),
    onSuccess: () => {
      close();
      form.reset();
      setEditing(null);
      refresh();
      void qc.invalidateQueries({ queryKey: ["owner-invitations"] });
      notifications.show({
        title: editing ? "User updated" : mode === "invite" ? "Invitation sent" : "User created",
        message: "The change was saved successfully.",
        color: "teal",
      });
    },
    onError: (e) => showLifecycleFormError(e, form, "User could not be saved"),
  });
  const action = useMutation({
    mutationFn: ({ user, kind }: { user: ManagedUser; kind: "disable" | "enable" | "delete" }) =>
      kind === "disable" ? disableUser(user.id) : kind === "enable" ? enableUser(user.id) : deleteUser(user.id),
    onSuccess: (_, v) => {
      refresh();
      notifications.show({
        title: v.kind === "delete" ? "User deleted" : "Access updated",
        message: "The change was saved successfully.",
        color: "teal",
      });
    },
    onError: mutationError("Access change failed"),
  });
  const reset = useMutation({
    mutationFn: sendPasswordReset,
    onSuccess: () =>
      notifications.show({
        title: "Reset email sent",
        message: "The user will receive instructions shortly.",
        color: "teal",
      }),
    onError: mutationError("Reset email could not be sent"),
  });
  const savePassword = useMutation({
    mutationFn: ({ id, password }: { id: string; password: string }) => setUserPassword(id, password),
    onSuccess: () => {
      setPasswordUser(null);
      passwordForm.reset();
      notifications.show({
        title: "Initial password saved",
        message: "The administrator-chosen password is active now.",
        color: "teal",
      });
    },
    onError: (e) => showLifecycleFormError(e, passwordForm, "Initial password could not be saved"),
  });
  const filtered = useMemo(
    () =>
      (users.data ?? []).filter(
        (u) =>
          `${u.displayName ?? ""} ${u.email ?? ""}`.toLowerCase().includes(search.toLowerCase()) &&
          (!metricFilter ||
            (metricFilter === "active"
              ? u.active
              : metricFilter === "disabled"
                ? u.disabled
                : metricFilter === "sso"
                  ? u.ssoEnabled
                  : u.role === "Owner")),
      ),
    [users.data, search, metricFilter],
  );
  const openEdit = (u: ManagedUser) => {
    setMode("invite");
    setEditing(u);
    form.setValues({ displayName: u.displayName ?? "", email: u.email ?? "", role: u.role, password: "" });
    open();
  };
  const confirm = (u: ManagedUser, kind: "disable" | "enable" | "delete") =>
    modals.openConfirmModal({
      title: kind === "delete" ? "Delete this user?" : kind === "disable" ? "Disable access?" : "Enable access?",
      children: (
        <Text size="sm">
          {kind === "delete"
            ? "This permanently removes the account and cannot be undone."
            : `${u.displayName || u.email} will ${kind === "disable" ? "no longer be able to sign in" : "be able to sign in again"}.`}
        </Text>
      ),
      labels: { confirm: kind === "delete" ? "Delete user" : kind[0].toUpperCase() + kind.slice(1), cancel: "Cancel" },
      confirmProps: { color: kind === "delete" ? "red" : undefined },
      onConfirm: () => action.mutate({ user: u, kind }),
    });
  const status = (u: ManagedUser) =>
    [u.disabled ? "Administrator-disabled" : "Active", u.lockedOut ? "Temporarily locked out" : ""]
      .filter(Boolean)
      .join(" · ");
  const statusColor = (u: ManagedUser) => (u.disabled ? "gray" : u.lockedOut ? "orange" : "teal");
  const actions = (u: ManagedUser) =>
    self(u) ? (
      <Text size="sm" c="dimmed">
        This is your account. Manage it in{" "}
        <Anchor component={Link} to="/settings">
          Account settings
        </Anchor>
        .
      </Text>
    ) : (
      <Menu position="bottom-end">
        <Menu.Target>
          <ActionIcon
            variant="subtle"
            aria-label={`Actions for ${u.displayName || u.email || "user"}`}
            disabled={action.isPending || reset.isPending}
          >
            <IconDots size={18} />
          </ActionIcon>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Item onClick={() => openEdit(u)}>Edit details</Menu.Item>
          <Menu.Item leftSection={<IconMail size={15} />} onClick={() => reset.mutate(u.id)} disabled={reset.isPending}>
            Send reset email
          </Menu.Item>
          <Menu.Item
            leftSection={<IconLock size={15} />}
            onClick={() => {
              setPasswordUser(u);
              passwordForm.reset();
            }}
          >
            Set initial password
          </Menu.Item>
          <Menu.Divider />
          {u.disabled ? (
            <Menu.Item
              leftSection={<IconUserCheck size={15} />}
              onClick={() => confirm(u, "enable")}
              disabled={action.isPending}
            >
              Enable access
            </Menu.Item>
          ) : (
            <Menu.Item
              leftSection={<IconUserOff size={15} />}
              onClick={() => confirm(u, "disable")}
              disabled={action.isPending}
            >
              Disable access
            </Menu.Item>
          )}
          <Menu.Item
            color="red"
            leftSection={<IconTrash size={15} />}
            onClick={() => confirm(u, "delete")}
            disabled={action.isPending}
          >
            Delete user
          </Menu.Item>
        </Menu.Dropdown>
      </Menu>
    );
  const userInfo = (u: ManagedUser) => (
    <>
      <Text fw={600}>{u.displayName || "Unnamed user"}</Text>
      <Text size="sm" c="dimmed">
        {u.email || "No email address"}
      </Text>
    </>
  );
  const avatar = (u: ManagedUser) => (
    <Avatar
      src={u.avatarUrl ? appUrl(u.avatarUrl) : null}
      name={u.displayName || u.email || "User"}
      color="initials"
      radius="xl"
    />
  );
  return (
    <Stack maw={1100} mx="auto" gap="xl">
      <Group justify="space-between" align="flex-end">
        <div>
          <Group gap="sm">
            <IconUsers size={30} color="var(--mantine-color-vantigo-6)" />
            <Title order={2}>Users</Title>
          </Group>
          <Text c="dimmed" mt={5}>
            Manage access, roles, and sign-in security for your team.
          </Text>
        </div>
        <Button
          leftSection={<IconPlus size={16} />}
          onClick={() => {
            setEditing(null);
            setMode("invite");
            form.reset();
            open();
          }}
        >
          Add user
        </Button>
      </Group>
      <SimpleGrid className="admin-metrics" cols={{ base: 2, sm: 5 }} spacing="sm">
        {[
          ["Active", (users.data ?? []).filter((u) => u.active), "teal"],
          ["Disabled", (users.data ?? []).filter((u) => u.disabled), "gray"],
          ["SSO", (users.data ?? []).filter((u) => u.ssoEnabled), "blue"],
          ["Admins", (users.data ?? []).filter((u) => u.role === "Owner"), "violet"],
        ].map(([label, value, color]) => (
          <Card
            key={label as string}
            withBorder
            radius="md"
            padding="md"
            component="button"
            type="button"
            aria-pressed={metricFilter === label?.toString().toLowerCase()}
            aria-label={`Filter users by ${label}`}
            onClick={() =>
              setMetricFilter(metricFilter === label?.toString().toLowerCase() ? null : label?.toString().toLowerCase())
            }
            style={{ textAlign: "left", cursor: "pointer" }}
            styles={{
              root: {
                borderColor:
                  metricFilter === label?.toString().toLowerCase() ? "var(--mantine-color-vantigo-6)" : undefined,
                backgroundColor:
                  metricFilter === label?.toString().toLowerCase() ? "var(--mantine-color-vantigo-0)" : undefined,
              },
            }}
          >
            <Text size="sm" c="dimmed">
              {label as string}
            </Text>
            <Text fz={25} fw={700} c={color as string}>
              {(value as ManagedUser[]).length}
            </Text>
          </Card>
        ))}
        <Anchor component={Link} to="/admin/invitations" style={{ textDecoration: "none", color: "inherit" }}>
          <Card withBorder radius="md" padding="md">
            <Text size="sm" c="dimmed">
              Pending invitations
            </Text>
            <Text fz={25} fw={700}>
              {
                (invitations.data ?? []).filter(
                  (i) => !i.revokedAt && !i.acceptedAt && new Date(i.expiresAt) > new Date(),
                ).length
              }
            </Text>
          </Card>
        </Anchor>
      </SimpleGrid>
      <Card withBorder radius="md" p={0} style={{ overflow: "hidden" }}>
        <Group p="md" justify="space-between">
          <TextInput
            label="Search users"
            placeholder="Search by name or email"
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
            style={{ flex: "1 1 280px" }}
          />
          <Text size="sm" c="dimmed">
            {filtered.length} {filtered.length === 1 ? "user" : "users"}
          </Text>
        </Group>
        {users.isError && (
          <Alert m="md" color="red" title="Users could not be loaded">
            Try refreshing the page.
          </Alert>
        )}
        {users.isPending ? (
          <Stack p="md">
            {[1, 2, 3].map((n) => (
              <Skeleton key={n} height={52} radius="sm" />
            ))}
          </Stack>
        ) : filtered.length === 0 ? (
          <Stack align="center" p={50}>
            <IconUsers size={38} color="var(--mantine-color-gray-5)" />
            <Text c="dimmed">
              {search
                ? "No users match your search."
                : metricFilter
                  ? `No ${metricFilter} users found.`
                  : "No users yet."}
            </Text>
          </Stack>
        ) : (
          <>
            <div className="users-desktop-table">
              <Table verticalSpacing="md" highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Avatar</Table.Th>
                    <Table.Th>User</Table.Th>
                    <Table.Th>Role</Table.Th>
                    <Table.Th>Access</Table.Th>
                    <Table.Th>2FA</Table.Th>
                    <Table.Th>
                      <span className="sr-only">Actions</span>
                    </Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {filtered.map((u) => {
                    const label = status(u);
                    return (
                      <Table.Tr key={u.id}>
                        <Table.Td>{avatar(u)}</Table.Td>
                        <Table.Td>{userInfo(u)}</Table.Td>
                        <Table.Td>
                          <Badge variant="light">{u.role}</Badge>
                        </Table.Td>
                        <Table.Td>
                          <Badge color={statusColor(u)} variant="dot">
                            {label}
                          </Badge>
                        </Table.Td>
                        <Table.Td>
                          {u.twoFactorEnabled ? (
                            <Group gap={5}>
                              <IconShieldCheck size={15} color="var(--mantine-color-teal-6)" />
                              <Text size="sm">On</Text>
                            </Group>
                          ) : (
                            <Text size="sm" c="dimmed">
                              Off
                            </Text>
                          )}
                        </Table.Td>
                        <Table.Td ta="right">{actions(u)}</Table.Td>
                      </Table.Tr>
                    );
                  })}
                </Table.Tbody>
              </Table>
            </div>
            <SimpleGrid className="users-mobile-list" p="md">
              {filtered.map((u) => {
                const label = status(u);
                return (
                  <Card key={u.id} withBorder>
                    <Stack gap="sm">
                      <Group justify="space-between" align="flex-start">
                        <Group gap="sm" wrap="nowrap">
                          {avatar(u)}
                          {userInfo(u)}
                        </Group>
                        {actions(u)}
                      </Group>
                      <Group gap="xs">
                        <Badge variant="light">{u.role}</Badge>
                        <Badge color={statusColor(u)} variant="dot">
                          {label}
                        </Badge>
                        <Badge variant="light">2FA {u.twoFactorEnabled ? "On" : "Off"}</Badge>
                      </Group>
                    </Stack>
                  </Card>
                );
              })}
            </SimpleGrid>
          </>
        )}
      </Card>
      <Modal opened={opened} onClose={close} title={editing ? "Edit user" : "Add a user"} centered>
        <form onSubmit={form.onSubmit((v) => change.mutate(v))}>
          <Stack>
            <Text size="sm" c="dimmed">
              {editing ? "Update this account’s details and role." : "Choose how this person should get started."}
            </Text>
            {!editing && (
              <SegmentedControl
                fullWidth
                value={mode}
                onChange={(v) => setMode(v as typeof mode)}
                data={[
                  { label: "Send invitation", value: "invite" },
                  { label: "Set initial password", value: "initial" },
                ]}
              />
            )}
            {!editing && mode === "initial" && (
              <Text size="sm" c="dimmed">
                The initial password is chosen by the administrator.
              </Text>
            )}
            <TextInput label="Display name" {...form.getInputProps("displayName")} />
            <TextInput label="Email" {...form.getInputProps("email")} />
            <Select label="Role" data={roles} {...form.getInputProps("role")} />
            {!editing && mode === "initial" && (
              <PasswordInput label="Initial password" description={passwordHelp} {...form.getInputProps("password")} />
            )}
            <Button type="submit" loading={change.isPending}>
              Save
            </Button>
          </Stack>
        </form>
      </Modal>
      <Modal opened={!!passwordUser} onClose={() => setPasswordUser(null)} title="Set initial password" centered>
        <form
          onSubmit={passwordForm.onSubmit((v) => {
            if (passwordUser) savePassword.mutate({ id: passwordUser.id, password: v.password });
          })}
        >
          <Stack>
            <Text size="sm" c="dimmed">
              Choose the initial password as an administrator. {passwordHelp}
            </Text>
            <PasswordInput label="Initial password" {...passwordForm.getInputProps("password")} />
            <Button type="submit" loading={savePassword.isPending}>
              Save initial password
            </Button>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
};

function passwordValid(value: string) {
  return value.length >= 12 && /[a-z]/.test(value) && /[A-Z]/.test(value) && /\d/.test(value);
}
export const Route = createFileRoute("/admin/users")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
  },
  component: UsersPage,
});
