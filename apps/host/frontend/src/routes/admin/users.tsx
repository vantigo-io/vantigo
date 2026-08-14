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
import { appUrl, useI18n } from "@vantigo/frontend-shell";
import { useMemo, useState } from "react";
import { createInvitation, listInvitations } from "../../api/account-lifecycle";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { ApiValidationError } from "../../api/request";
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
import { translateHostRole } from "../../i18n";
import "../../i18n";

const roles = ["User", "Owner"] as const;

const showUserFormError = (
  error: unknown,
  form: { setErrors: (errors: Record<string, string>) => void },
  title: string,
  t: (key: string) => string,
) => {
  const apiError = error as { code?: string };
  if (error instanceof ApiValidationError) form.setErrors(error.fieldErrors);
  else if (apiError.code === "account_exists")
    form.setErrors({
      email: error instanceof Error ? error.message : t("admin.accountExists"),
    });
  notifications.show({
    color: "red",
    title,
    message: error instanceof Error ? error.message : t("common.requestFailed"),
  });
};

const UsersPage = () => {
  const { t, formatters } = useI18n("host");
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
      email: (v) => (/^\S+@\S+$/.test(v) ? undefined : t("auth.validEmail")),
      password: (v) =>
        !editing && mode === "initial" && !passwordValid(v) ? t("admin.passwordValidation") : undefined,
    },
  });
  const passwordForm = useForm({
    initialValues: { password: "" },
    validate: {
      password: (v) => (passwordValid(v) ? undefined : t("admin.passwordValidation")),
    },
  });
  const refresh = () => void qc.invalidateQueries({ queryKey: ["owner-users"] });
  const self = (u: ManagedUser) => u.id === session?.user.id;
  const mutationError = (title: string) => (error: unknown) =>
    notifications.show({
      title,
      message: error instanceof Error ? error.message : t("common.requestFailed"),
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
        title: editing
          ? t("admin.userUpdated")
          : mode === "invite"
            ? t("admin.invitationSent")
            : t("admin.userCreated"),
        message: t("admin.saveChange"),
        color: "teal",
      });
    },
    onError: (e) => showUserFormError(e, form, t("admin.userSaveFailed"), t),
  });
  const action = useMutation({
    mutationFn: ({ user, kind }: { user: ManagedUser; kind: "disable" | "enable" | "delete" }) =>
      kind === "disable" ? disableUser(user.id) : kind === "enable" ? enableUser(user.id) : deleteUser(user.id),
    onSuccess: (_, v) => {
      refresh();
      notifications.show({
        title: v.kind === "delete" ? t("admin.userDeleted") : t("admin.accessUpdated"),
        message: t("admin.saveChange"),
        color: "teal",
      });
    },
    onError: mutationError(t("admin.accessChangeFailed")),
  });
  const reset = useMutation({
    mutationFn: sendPasswordReset,
    onSuccess: () =>
      notifications.show({
        title: t("admin.resetSent"),
        message: t("admin.resetInstructions"),
        color: "teal",
      }),
    onError: mutationError(t("admin.resetFailed")),
  });
  const savePassword = useMutation({
    mutationFn: ({ id, password }: { id: string; password: string }) => setUserPassword(id, password),
    onSuccess: () => {
      setPasswordUser(null);
      passwordForm.reset();
      notifications.show({
        title: t("admin.initialPasswordSaved"),
        message: t("admin.initialPasswordActive"),
        color: "teal",
      });
    },
    onError: (e) => showUserFormError(e, passwordForm, t("admin.initialPasswordSaveFailed"), t),
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
      title:
        kind === "delete"
          ? t("admin.deleteUserQuestion")
          : kind === "disable"
            ? t("admin.disableQuestion")
            : t("admin.enableQuestion"),
      children: (
        <Text size="sm">
          {kind === "delete"
            ? t("admin.deleteWarning")
            : kind === "disable"
              ? t("admin.willNoLongerSignIn", { user: u.displayName || u.email })
              : t("admin.willSignInAgain", { user: u.displayName || u.email })}
        </Text>
      ),
      labels: {
        confirm:
          kind === "delete"
            ? t("admin.deleteUser")
            : kind === "disable"
              ? t("admin.disableAccess")
              : t("admin.enableAccess"),
        cancel: t("common.cancel"),
      },
      confirmProps: { color: kind === "delete" ? "red" : undefined },
      onConfirm: () => action.mutate({ user: u, kind }),
    });
  const status = (u: ManagedUser) =>
    [u.disabled ? t("admin.administratorDisabled") : t("common.active"), u.lockedOut ? t("admin.lockedOut") : ""]
      .filter(Boolean)
      .join(" · ");
  const statusColor = (u: ManagedUser) => (u.disabled ? "gray" : u.lockedOut ? "orange" : "teal");
  const actions = (u: ManagedUser) =>
    self(u) ? (
      <Text size="sm" c="dimmed">
        {t("admin.accountSelf")}{" "}
        <Anchor component={Link} to="/settings">
          {t("admin.accountSettings")}
        </Anchor>
        .
      </Text>
    ) : (
      <Menu position="bottom-end">
        <Menu.Target>
          <ActionIcon
            variant="subtle"
            aria-label={t("admin.actionsFor", { user: u.displayName || u.email || t("common.user") })}
            disabled={action.isPending || reset.isPending}
          >
            <IconDots size={18} />
          </ActionIcon>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Item onClick={() => openEdit(u)}>{t("admin.editDetails")}</Menu.Item>
          <Menu.Item leftSection={<IconMail size={15} />} onClick={() => reset.mutate(u.id)} disabled={reset.isPending}>
            {t("admin.sendResetEmail")}
          </Menu.Item>
          <Menu.Item
            leftSection={<IconLock size={15} />}
            onClick={() => {
              setPasswordUser(u);
              passwordForm.reset();
            }}
          >
            {t("admin.setInitialPassword")}
          </Menu.Item>
          <Menu.Divider />
          {u.disabled ? (
            <Menu.Item
              leftSection={<IconUserCheck size={15} />}
              onClick={() => confirm(u, "enable")}
              disabled={action.isPending}
            >
              {t("admin.enableAccess")}
            </Menu.Item>
          ) : (
            <Menu.Item
              leftSection={<IconUserOff size={15} />}
              onClick={() => confirm(u, "disable")}
              disabled={action.isPending}
            >
              {t("admin.disableAccess")}
            </Menu.Item>
          )}
          <Menu.Item
            color="red"
            leftSection={<IconTrash size={15} />}
            onClick={() => confirm(u, "delete")}
            disabled={action.isPending}
          >
            {t("admin.deleteUser")}
          </Menu.Item>
        </Menu.Dropdown>
      </Menu>
    );
  const userInfo = (u: ManagedUser) => (
    <>
      <Text fw={600}>{u.displayName || t("admin.unnamedUser")}</Text>
      <Text size="sm" c="dimmed">
        {u.email || t("admin.noEmail")}
      </Text>
    </>
  );
  const avatar = (u: ManagedUser) => (
    <Avatar
      src={u.avatarUrl ? appUrl(u.avatarUrl) : null}
      name={u.displayName || u.email || t("common.user")}
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
            <Title order={2}>{t("admin.users")}</Title>
          </Group>
          <Text c="dimmed" mt={5}>
            {t("admin.usersDescription")}
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
          {t("admin.addUser")}
        </Button>
      </Group>
      <SimpleGrid className="admin-metrics" cols={{ base: 2, sm: 5 }} spacing="sm">
        {[
          { key: "active", label: t("admin.active"), value: (users.data ?? []).filter((u) => u.active), color: "teal" },
          {
            key: "disabled",
            label: t("common.disabled"),
            value: (users.data ?? []).filter((u) => u.disabled),
            color: "gray",
          },
          {
            key: "sso",
            label: t("admin.ssoShort"),
            value: (users.data ?? []).filter((u) => u.ssoEnabled),
            color: "blue",
          },
          {
            key: "admins",
            label: t("admin.admins"),
            value: (users.data ?? []).filter((u) => u.role === "Owner"),
            color: "violet",
          },
        ].map(({ key, label, value, color }) => (
          <Card
            key={label as string}
            withBorder
            radius="md"
            padding="md"
            component="button"
            type="button"
            aria-pressed={metricFilter === key}
            aria-label={t("admin.filterUsers", { label })}
            onClick={() => setMetricFilter(metricFilter === key ? null : key)}
            style={{ textAlign: "left", cursor: "pointer" }}
            styles={{
              root: {
                borderColor: metricFilter === key ? "var(--mantine-color-vantigo-6)" : undefined,
                backgroundColor: metricFilter === key ? "var(--mantine-color-vantigo-0)" : undefined,
              },
            }}
          >
            <Text size="sm" c="dimmed">
              {label}
            </Text>
            <Text fz={25} fw={700} c={color as string}>
              {formatters.formatNumber(value.length)}
            </Text>
          </Card>
        ))}
        <Anchor component={Link} to="/admin/invitations" style={{ textDecoration: "none", color: "inherit" }}>
          <Card withBorder radius="md" padding="md">
            <Text size="sm" c="dimmed">
              {t("admin.pendingInvitations")}
            </Text>
            <Text fz={25} fw={700}>
              {formatters.formatNumber(
                (invitations.data ?? []).filter(
                  (i) => !i.revokedAt && !i.acceptedAt && new Date(i.expiresAt) > new Date(),
                ).length,
              )}
            </Text>
          </Card>
        </Anchor>
      </SimpleGrid>
      <Card withBorder radius="md" p={0} style={{ overflow: "hidden" }}>
        <Group p="md" justify="space-between">
          <TextInput
            label={t("admin.searchUsers")}
            placeholder={t("admin.searchNameEmail")}
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
            style={{ flex: "1 1 280px" }}
          />
          <Text size="sm" c="dimmed">
            {formatters.formatNumber(filtered.length)}{" "}
            {filtered.length === 1 ? t("common.userSingular") : t("common.users")}
          </Text>
        </Group>
        {users.isError && (
          <Alert m="md" color="red" title={t("admin.usersLoadFailed")}>
            {t("common.tryAgain")}
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
                ? t("admin.noUsersSearch")
                : metricFilter
                  ? t("admin.noFilteredUsers", {
                      filter:
                        metricFilter === "active"
                          ? t("admin.active")
                          : metricFilter === "disabled"
                            ? t("common.disabled")
                            : metricFilter === "sso"
                              ? t("admin.ssoShort")
                              : t("admin.admins"),
                    })
                  : t("admin.noUsers")}
            </Text>
          </Stack>
        ) : (
          <>
            <div className="users-desktop-table">
              <Table verticalSpacing="md" highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("admin.avatar")}</Table.Th>
                    <Table.Th>{t("common.user")}</Table.Th>
                    <Table.Th>{t("common.role")}</Table.Th>
                    <Table.Th>{t("admin.access")}</Table.Th>
                    <Table.Th>{t("admin.twoFactor")}</Table.Th>
                    <Table.Th>
                      <span className="sr-only">{t("admin.actions")}</span>
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
                          <Badge variant="light">{translateHostRole(u.role, t)}</Badge>
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
                              <Text size="sm">{t("common.on")}</Text>
                            </Group>
                          ) : (
                            <Text size="sm" c="dimmed">
                              {t("common.off")}
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
                        <Badge variant="light">{translateHostRole(u.role, t)}</Badge>
                        <Badge color={statusColor(u)} variant="dot">
                          {label}
                        </Badge>
                        <Badge variant="light">
                          {t("admin.twoFactor")} {u.twoFactorEnabled ? t("common.on") : t("common.off")}
                        </Badge>
                      </Group>
                    </Stack>
                  </Card>
                );
              })}
            </SimpleGrid>
          </>
        )}
      </Card>
      <Modal opened={opened} onClose={close} title={editing ? t("admin.editUser") : t("admin.addAUser")} centered>
        <form onSubmit={form.onSubmit((v) => change.mutate(v))}>
          <Stack>
            <Text size="sm" c="dimmed">
              {editing ? t("admin.updateAccount") : t("admin.chooseStart")}
            </Text>
            {!editing && (
              <SegmentedControl
                fullWidth
                value={mode}
                onChange={(v) => setMode(v as typeof mode)}
                data={[
                  { label: t("admin.sendInvitation"), value: "invite" },
                  { label: t("admin.initialPasswordMode"), value: "initial" },
                ]}
              />
            )}
            {!editing && mode === "initial" && (
              <Text size="sm" c="dimmed">
                {t("admin.initialPasswordChosen")}
              </Text>
            )}
            <TextInput label={t("common.displayName")} {...form.getInputProps("displayName")} />
            <TextInput label={t("common.email")} {...form.getInputProps("email")} />
            <Select
              label={t("common.role")}
              data={roles.map((role) => ({
                value: role,
                label: role === "Owner" ? t("admin.ownerRole") : t("admin.userRole"),
              }))}
              {...form.getInputProps("role")}
            />
            {!editing && mode === "initial" && (
              <PasswordInput
                label={t("admin.initialPassword")}
                description={t("admin.passwordHelp")}
                {...form.getInputProps("password")}
              />
            )}
            <Button type="submit" loading={change.isPending}>
              {t("common.save")}
            </Button>
          </Stack>
        </form>
      </Modal>
      <Modal
        opened={!!passwordUser}
        onClose={() => setPasswordUser(null)}
        title={t("admin.setInitialPassword")}
        centered
      >
        <form
          onSubmit={passwordForm.onSubmit((v) => {
            if (passwordUser) savePassword.mutate({ id: passwordUser.id, password: v.password });
          })}
        >
          <Stack>
            <Text size="sm" c="dimmed">
              {t("admin.chooseInitialPassword", { help: t("admin.passwordHelp") })}
            </Text>
            <PasswordInput label={t("admin.initialPassword")} {...passwordForm.getInputProps("password")} />
            <Button type="submit" loading={savePassword.isPending}>
              {t("admin.saveInitialPassword")}
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
