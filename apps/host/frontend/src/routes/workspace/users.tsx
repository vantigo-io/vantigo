import {
  ActionIcon,
  Alert,
  Anchor,
  Avatar,
  Badge,
  Button,
  Card,
  Collapse,
  Group,
  Menu,
  Modal,
  PasswordInput,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { useDisclosure } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconBriefcase,
  IconBriefcaseOff,
  IconCircleCheck,
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
import { DirectoryMatches, type DirectoryPick, DirectoryPicker } from "@vantigo/entra-ui";
import { appUrl, ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useMemo, useState } from "react";
import { createInvitation, listInvitations } from "../../api/account-lifecycle";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { ApiValidationError } from "../../api/request";
import {
  announceSsoLink,
  createUser,
  deleteUser,
  disableUser,
  enableUser,
  listUsers,
  type ManagedUser,
  manageUser,
  removeSsoLink,
  sendPasswordReset,
  setUserPassword,
  type UserRole,
  unmanageUser,
  updateUser,
} from "../../api/users";
import { translateHostRole } from "../../i18n";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import "../../i18n";

const emailPattern = /^\S+@\S+$/;

const roles = ["User", "Owner"] as const;

/** Staff's employee-number alphabet, as identity checks it once the number is trimmed. */
const employeeNumberPattern = /^[A-Za-z0-9-]{1,16}$/;

/**
 * The words for identity's refusals about Staff-managed and pending users;
 * any other refusal keeps the server's own message.
 */
const refusalKeys: Record<string, string> = {
  account_privileged: "admin.refusalAccountPrivileged",
  employee_number_taken: "admin.refusalEmployeeNumberTaken",
  account_managed: "admin.deleteManagedReason",
  account_pending: "admin.refusalAccountPending",
  sso_not_configured: "admin.refusalSsoNotConfigured",
  account_linked: "admin.refusalAccountLinked",
  sso_link_taken: "admin.refusalSsoLinkTaken",
};

const refusalMessage = (error: unknown, t: (key: string) => string) => {
  const key = refusalKeys[(error as { code?: string }).code ?? ""];
  if (key) return t(key);
  return error instanceof Error ? error.message : t("common.requestFailed");
};

/**
 * Why identity refused announcing a link, in the Owner's words: a field's
 * sentence for a refused value, the coded refusals by their words.
 */
const ssoLinkRefusal = (error: unknown, t: (key: string) => string) =>
  error instanceof ApiValidationError
    ? (Object.values(error.fieldErrors)[0] ?? error.message)
    : refusalMessage(error, t);

/** A menu item's label with, when it cannot be used, the reason beneath it. */
const withReason = (label: string, reason?: string) =>
  reason ? (
    <Stack gap={0}>
      <span>{label}</span>
      <Text size="xs" c="dimmed" maw={260}>
        {reason}
      </Text>
    </Stack>
  ) : (
    label
  );

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
    message: refusalMessage(error, t),
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
  const [adoptUser, setAdoptUser] = useState<ManagedUser | null>(null);
  const [connectUser, setConnectUser] = useState<ManagedUser | null>(null);
  const [connectMore, setConnectMore] = useState(false);
  // The account last picked in the directory: its two keys are one unit, so
  // editing either away from it, or searching again, forgets the pick.
  const [connectPick, setConnectPick] = useState<DirectoryPick | null>(null);
  const [matchesOpen, setMatchesOpen] = useState(false);
  // The Entra module's directory picker and review, while the module is enabled (plan reading 7).
  const entraOn = enabledModuleKeys().includes("entra");
  const form = useForm({
    initialValues: { displayName: "", email: "", role: "User" as UserRole, password: "" },
    validate: {
      email: (v) => (emailPattern.test(v) ? undefined : t("auth.validEmail")),
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
  const adoptForm = useForm({
    initialValues: { employeeNumber: "" },
    validate: {
      employeeNumber: (v) => (employeeNumberPattern.test(v.trim()) ? undefined : t("admin.employeeNumberInvalid")),
    },
  });
  const connectForm = useForm({
    initialValues: { userPrincipalName: "", objectId: "" },
    validate: {
      userPrincipalName: (v, values) =>
        v.trim() === "" && values.objectId.trim() === ""
          ? t("admin.ssoLinkNeedOne")
          : v.trim() !== "" && !emailPattern.test(v.trim())
            ? t("auth.validEmail")
            : undefined,
    },
  });
  const refresh = () => void qc.invalidateQueries({ queryKey: ["owner-users"] });
  const self = (u: ManagedUser) => u.id === session?.user.id;
  const mutationError = (title: string) => (error: unknown) =>
    notifications.show({
      title,
      message: refusalMessage(error, t),
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
    onError: (e) =>
      // Identity refuses promoting a pending user account_pending: said as a refused promotion.
      editing?.pending && form.values.role === "Owner" && (e as { code?: string }).code === "account_pending"
        ? notifications.show({
            color: "red",
            title: t("admin.userSaveFailed"),
            message: t("admin.refusalPendingPromotion"),
          })
        : showUserFormError(e, form, t("admin.userSaveFailed"), t),
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
  const adopt = useMutation({
    mutationFn: ({ id, employeeNumber }: { id: string; employeeNumber: string }) => manageUser(id, employeeNumber),
    onSuccess: () => {
      setAdoptUser(null);
      adoptForm.reset();
      refresh();
      notifications.show({ title: t("admin.adopted"), message: t("admin.adoptedMessage"), color: "teal" });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError)
        adoptForm.setErrors({ employeeNumber: t("admin.employeeNumberInvalid") });
      else if ((error as { code?: string }).code === "employee_number_taken")
        adoptForm.setErrors({ employeeNumber: t("admin.refusalEmployeeNumberTaken") });
      else mutationError(t("admin.adoptFailed"))(error);
    },
  });
  const unmanage = useMutation({
    mutationFn: (u: ManagedUser) => unmanageUser(u.id),
    onSuccess: () => {
      refresh();
      notifications.show({ title: t("admin.stoppedManaging"), message: t("admin.saveChange"), color: "teal" });
    },
    onError: mutationError(t("admin.stopManagingFailed")),
  });
  const confirmUnmanage = (u: ManagedUser) =>
    modals.openConfirmModal({
      title: t("admin.stopManagingQuestion"),
      children: <Text size="sm">{t("admin.stopManagingWarning", { user: u.displayName || u.email })}</Text>,
      labels: { confirm: t("admin.stopManaging"), cancel: t("common.cancel") },
      onConfirm: () => unmanage.mutate(u),
    });
  const announce = useMutation({
    mutationFn: ({ id, body }: { id: string; body: { userPrincipalName?: string; objectId?: string } }) =>
      announceSsoLink(id, body),
    onSuccess: () => {
      setConnectUser(null);
      connectForm.reset();
      refresh();
      notifications.show({
        title: t("admin.ssoLinkAnnounced"),
        message: t("admin.ssoLinkAnnouncedMessage"),
        color: "teal",
      });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        connectForm.setErrors(error.fieldErrors);
        if (error.fieldErrors.objectId) setConnectMore(true);
      } else mutationError(t("admin.ssoLinkAnnounceFailed"))(error);
    },
  });
  const removeLink = useMutation({
    mutationFn: (u: ManagedUser) => removeSsoLink(u.id),
    onSuccess: () => {
      refresh();
      notifications.show({ title: t("admin.ssoLinkRemoved"), message: t("admin.saveChange"), color: "teal" });
    },
    onError: mutationError(t("admin.ssoLinkRemoveFailed")),
  });
  const openConnect = (u: ManagedUser) => {
    const link = u.ssoLink;
    const expected = link?.state === "expected";
    connectForm.setValues({
      userPrincipalName: expected ? (link.userPrincipalName ?? "") : (u.email ?? ""),
      objectId: expected ? (link.objectId ?? "") : "",
    });
    connectForm.resetDirty();
    connectForm.clearErrors();
    setConnectMore(expected && !!link.objectId);
    setConnectPick(null);
    setConnectUser(u);
  };
  const confirmRemoveLink = (u: ManagedUser, kind: "disconnect" | "cancel") =>
    modals.openConfirmModal({
      title: t(kind === "disconnect" ? "admin.ssoLinkDisconnectQuestion" : "admin.ssoLinkCancelQuestion"),
      children: (
        <Text size="sm">
          {t(kind === "disconnect" ? "admin.ssoLinkDisconnectWarning" : "admin.ssoLinkCancelWarning", {
            user: u.displayName || u.email,
          })}
        </Text>
      ),
      labels: {
        confirm: t(kind === "disconnect" ? "admin.ssoLinkDisconnect" : "admin.ssoLinkCancel"),
        cancel: t("common.cancel"),
      },
      onConfirm: () => removeLink.mutate(u),
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
  // A pending user is listed disabled: they are waiting for their invitation,
  // not shut out — unless an Owner disabled them, which Staff never undoes.
  const access = (u: ManagedUser) =>
    u.pending
      ? [t("admin.userPending"), u.disabledBy === "owner" ? t("admin.disabledByOwner") : ""].filter(Boolean).join(" · ")
      : !u.disabled
        ? t("common.active")
        : u.disabledBy === "staff"
          ? t("admin.disabledByStaff")
          : u.disabledBy === "owner"
            ? t("admin.disabledByOwner")
            : t("admin.administratorDisabled");
  const status = (u: ManagedUser) => [access(u), u.lockedOut ? t("admin.lockedOut") : ""].filter(Boolean).join(" · ");
  const statusColor = (u: ManagedUser) => (u.pending ? "blue" : u.disabled ? "gray" : u.lockedOut ? "orange" : "teal");
  const staffManaged = (u: ManagedUser) => u.managedBy === "staff";
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
            disabled={
              action.isPending || reset.isPending || adopt.isPending || unmanage.isPending || removeLink.isPending
            }
          >
            <IconDots size={18} />
          </ActionIcon>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Item onClick={() => openEdit(u)}>{t("admin.editDetails")}</Menu.Item>
          <Menu.Item
            leftSection={<IconMail size={15} />}
            onClick={() => reset.mutate(u.id)}
            disabled={reset.isPending || u.pending}
          >
            {withReason(t("admin.sendResetEmail"), u.pending ? t("admin.enablePendingReason") : undefined)}
          </Menu.Item>
          <Menu.Item
            leftSection={<IconLock size={15} />}
            onClick={() => {
              setPasswordUser(u);
              passwordForm.reset();
            }}
            disabled={u.pending}
          >
            {withReason(t("admin.setInitialPassword"), u.pending ? t("admin.enablePendingReason") : undefined)}
          </Menu.Item>
          {staffManaged(u) ? (
            <Menu.Item
              leftSection={<IconBriefcaseOff size={15} />}
              onClick={() => confirmUnmanage(u)}
              disabled={unmanage.isPending}
            >
              {t("admin.stopManaging")}
            </Menu.Item>
          ) : (
            u.role !== "Owner" && (
              <Menu.Item
                leftSection={<IconBriefcase size={15} />}
                onClick={() => {
                  adoptForm.reset();
                  setAdoptUser(u);
                }}
              >
                {t("admin.adoptForStaff")}
              </Menu.Item>
            )
          )}
          {u.ssoLink?.state === "none" && !u.pending && (
            <Menu.Item onClick={() => openConnect(u)}>{t("admin.ssoLinkConnectItem")}</Menu.Item>
          )}
          {u.ssoLink?.state === "expected" && (
            <>
              <Menu.Item onClick={() => openConnect(u)}>{t("admin.ssoLinkChangeItem")}</Menu.Item>
              <Menu.Item onClick={() => confirmRemoveLink(u, "cancel")}>{t("admin.ssoLinkCancelItem")}</Menu.Item>
            </>
          )}
          {u.ssoLink?.state === "linked" && (
            <Menu.Item onClick={() => confirmRemoveLink(u, "disconnect")}>{t("admin.ssoLinkDisconnectItem")}</Menu.Item>
          )}
          <Menu.Divider />
          {u.pending ? (
            <>
              <Menu.Item leftSection={<IconUserCheck size={15} />} disabled>
                {withReason(t("admin.enableAccess"), t("admin.enablePendingReason"))}
              </Menu.Item>
              {/* Staff creates a pending user disabled by Staff; an Owner may still disable them, which Staff never undoes. */}
              {u.disabledBy !== "owner" && (
                <Menu.Item
                  leftSection={<IconUserOff size={15} />}
                  onClick={() => confirm(u, "disable")}
                  disabled={action.isPending}
                >
                  {t("admin.disableAccess")}
                </Menu.Item>
              )}
            </>
          ) : u.disabled ? (
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
            disabled={action.isPending || staffManaged(u)}
          >
            {withReason(t("admin.deleteUser"), staffManaged(u) ? t("admin.deleteManagedReason") : undefined)}
          </Menu.Item>
        </Menu.Dropdown>
      </Menu>
    );
  // The server omits ssoLink while OIDC is off, and the column goes with it.
  const ssoOn = users.data?.some((u) => u.ssoLink !== undefined) ?? false;
  const ssoCell = (u: ManagedUser) => {
    const link = u.ssoLink;
    if (link?.state === "linked")
      return (
        <Group gap={5} wrap="nowrap">
          <IconCircleCheck size={15} color="var(--mantine-color-teal-6)" />
          <Text size="sm">{t("admin.ssoLinkConnected")}</Text>
        </Group>
      );
    if (link?.state === "expected")
      return (
        <Stack gap={2} align="flex-start">
          <Badge variant="light" color="blue">
            {t("admin.ssoLinkAwaiting")}
          </Badge>
          <Text size="xs" c="dimmed">
            {link.userPrincipalName || link.objectId}
          </Text>
        </Stack>
      );
    if (link?.state === "none" && !self(u) && !u.pending)
      return (
        <Button variant="light" size="xs" onClick={() => openConnect(u)}>
          {t("admin.ssoLinkConnect")}
        </Button>
      );
    return null;
  };
  const userInfo = (u: ManagedUser) => (
    <>
      <Text fw={600}>{u.displayName || t("admin.unnamedUser")}</Text>
      <Text size="sm" c="dimmed">
        {u.email || t("admin.noEmail")}
      </Text>
      {staffManaged(u) && (
        <Badge variant="light" color="grape" size="sm" mt={4}>
          {t("admin.managedByStaff", { number: u.employeeNumber ?? "" })}
        </Badge>
      )}
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
    <Stack gap="xl">
      <PageHeader
        title={t("admin.users")}
        description={t("admin.usersDescription")}
        actions={
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
        }
      />
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
        <Anchor component={Link} to="/workspace/invitations" style={{ textDecoration: "none", color: "inherit" }}>
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
          <Group gap="md">
            {ssoOn && entraOn && (
              <Button variant="default" onClick={() => setMatchesOpen(true)}>
                {t("admin.ssoLinkFindMatches")}
              </Button>
            )}
            <Text size="sm" c="dimmed">
              {formatters.formatNumber(filtered.length)}{" "}
              {filtered.length === 1 ? t("common.userSingular") : t("common.users")}
            </Text>
          </Group>
        </Group>
        {users.isError && (
          <Alert m="md" color="red" title={t("admin.usersLoadFailed")}>
            {t("common.tryAgain")}
          </Alert>
        )}
        {users.isPending ? (
          <ContentSkeleton rows={3} rowHeight={52} p="md" />
        ) : filtered.length === 0 ? (
          <EmptyState
            icon={IconUsers}
            title={
              search
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
                  : t("admin.noUsers")
            }
          />
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
                    {ssoOn && <Table.Th>{t("admin.ssoLinkColumn")}</Table.Th>}
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
                        {ssoOn && <Table.Td>{ssoCell(u)}</Table.Td>}
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
                      {ssoOn && ssoCell(u)}
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
            {editing && staffManaged(editing) && form.values.role === "Owner" && editing.role !== "Owner" && (
              <Text size="sm" c="orange">
                {t("admin.promotionStopsManaging")}
              </Text>
            )}
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
        opened={!!adoptUser}
        onClose={() => setAdoptUser(null)}
        title={t("admin.adoptTitle", { user: adoptUser?.displayName || adoptUser?.email || t("common.user") })}
        centered
      >
        <form
          onSubmit={adoptForm.onSubmit((v) => {
            if (adoptUser) adopt.mutate({ id: adoptUser.id, employeeNumber: v.employeeNumber.trim() });
          })}
        >
          <Stack>
            <Text size="sm" c="dimmed">
              {t("admin.adoptDescription")}
            </Text>
            <TextInput
              label={t("admin.employeeNumber")}
              description={t("admin.employeeNumberHelp")}
              maxLength={16}
              withAsterisk
              {...adoptForm.getInputProps("employeeNumber")}
            />
            <Button type="submit" loading={adopt.isPending}>
              {t("admin.adopt")}
            </Button>
          </Stack>
        </form>
      </Modal>
      <Modal
        opened={!!connectUser}
        onClose={() => setConnectUser(null)}
        title={t("admin.ssoLinkConnectTitle", {
          user: connectUser?.displayName || connectUser?.email || t("common.user"),
        })}
        centered
      >
        <form
          onSubmit={connectForm.onSubmit((v) => {
            if (!connectUser) return;
            const userPrincipalName = v.userPrincipalName.trim();
            const objectId = v.objectId.trim();
            announce.mutate({
              id: connectUser.id,
              body: {
                ...(userPrincipalName && { userPrincipalName }),
                ...(objectId && { objectId }),
              },
            });
          })}
        >
          <Stack>
            <Text size="sm" c="dimmed">
              {t("admin.ssoLinkConnectDescription")}
            </Text>
            {entraOn && connectUser && (
              <DirectoryPicker
                forUserId={connectUser.id}
                userName={(id) => {
                  const holder = users.data?.find((u) => u.id === id);
                  return holder ? holder.displayName || holder.email || undefined : undefined;
                }}
                onPick={(pick) => {
                  connectForm.setValues(pick);
                  connectForm.clearErrors();
                  setConnectPick(pick);
                  setConnectMore(true);
                }}
                onClear={() => {
                  if (!connectPick) return;
                  connectForm.setValues({ userPrincipalName: "", objectId: "" });
                  setConnectPick(null);
                }}
              />
            )}
            <TextInput
              label={t("admin.ssoLinkUpn")}
              description={t("admin.ssoLinkUpnHelp")}
              {...connectForm.getInputProps("userPrincipalName")}
              onChange={(event) => {
                const value = event.currentTarget.value;
                connectForm.getInputProps("userPrincipalName").onChange(value);
                // A principal name edited away from the picked account's no
                // longer names it: its object id goes with the pick.
                if (connectPick && value.trim() !== connectPick.userPrincipalName) {
                  connectForm.setFieldValue("objectId", "");
                  setConnectPick(null);
                }
              }}
            />
            <Anchor component="button" type="button" size="sm" onClick={() => setConnectMore((m) => !m)}>
              {t("admin.ssoLinkMore")}
            </Anchor>
            <Collapse expanded={connectMore}>
              <TextInput
                label={t("admin.ssoLinkObjectId")}
                description={t("admin.ssoLinkObjectIdHelp")}
                {...connectForm.getInputProps("objectId")}
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  connectForm.getInputProps("objectId").onChange(value);
                  // An object id typed over the pick's makes the pair the Owner's own.
                  if (connectPick && value.trim() !== connectPick.objectId) setConnectPick(null);
                }}
              />
            </Collapse>
            <Button type="submit" loading={announce.isPending}>
              {t("admin.ssoLinkSubmit")}
            </Button>
          </Stack>
        </form>
      </Modal>
      <Modal
        opened={matchesOpen}
        onClose={() => setMatchesOpen(false)}
        title={t("admin.ssoLinkMatchesTitle")}
        size="xl"
        centered
      >
        {matchesOpen && (
          <DirectoryMatches
            users={(users.data ?? []).map((u) => ({
              id: u.id,
              displayName: u.displayName,
              email: u.email,
              owner: u.role === "Owner",
            }))}
            connect={async (userId, pick) => {
              try {
                await announceSsoLink(userId, pick);
              } catch (error) {
                throw new Error(ssoLinkRefusal(error, t), { cause: error });
              }
            }}
            onDone={refresh}
          />
        )}
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
export const Route = createFileRoute("/workspace/users")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
  },
  component: UsersPage,
});
