import {
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Divider,
  Group,
  Loader,
  Modal,
  MultiSelect,
  Select,
  Stack,
  Tabs,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { useDisclosure } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconLock, IconPlus, IconShield, IconTrash, IconUsers } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, redirect } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AuthorizationRole,
  assignUserRoles,
  createAuthorizationRole,
  createDelegation,
  type DelegationInput,
  deleteAuthorizationRole,
  getAuthorizationMe,
  getUserAccess,
  listAuthorizationRoles,
  listAuthorizationUsers,
  listDelegations,
  listPermissionCatalog,
  type RoleInput,
  revokeDelegation,
  updateAuthorizationRole,
} from "../../api/authorization";
import {
  translateHostPermissionCategory,
  translateHostPermissionDescription,
  translateHostPermissionKey,
  translateHostPermissionModule,
  translateHostRole,
  translateHostRoleDescription,
} from "../../i18n";
import "../../i18n";

const errorText = (error: unknown, requestFailed: string) => (error instanceof Error ? error.message : requestFailed);

const RolesPage = () => {
  const { t } = useI18n("host");
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ["authorization", "me"], queryFn: getAuthorizationMe });
  const manageable = me.data?.canManageAuthorization === true;
  const scope = me.data?.administrationScope;
  const isOwner = scope?.isOwner === true;
  const scopes = scope?.delegationScopes ?? [];
  const catalog = useQuery({
    queryKey: ["authorization", "catalog"],
    queryFn: listPermissionCatalog,
    enabled: manageable,
  });
  const roles = useQuery({
    queryKey: ["authorization", "roles"],
    queryFn: listAuthorizationRoles,
    enabled: manageable,
  });
  const users = useQuery({
    queryKey: ["authorization", "users"],
    queryFn: listAuthorizationUsers,
    enabled: manageable,
  });
  const delegations = useQuery({
    queryKey: ["authorization", "delegations"],
    queryFn: listDelegations,
    enabled: manageable && isOwner,
  });
  const [activeSection, setActiveSection] = useState("roles");
  const [selectedRole, setSelectedRole] = useState<AuthorizationRole | null>(null);
  const [selectedUser, setSelectedUser] = useState<string | null>(null);
  const [selectedScopeId, setSelectedScopeId] = useState<string | null>(null);
  const [assignmentScopeId, setAssignmentScopeId] = useState<string | null>(null);
  const [roleModal, { open: openRole, close: closeRole }] = useDisclosure(false);
  const [delegateModal, { open: openDelegate, close: closeDelegate }] = useDisclosure(false);
  const roleForm = useForm<RoleInput>({
    initialValues: { name: "", displayName: "", description: "", permissionKeys: [] },
    validate: {
      name: (value) => (/^[a-z][a-z0-9-]+$/.test(value) ? null : t("admin.nameValidation")),
      displayName: (value) => (value.trim() ? null : t("admin.displayNameValidation")),
      description: (value) => (value.trim() ? null : t("admin.descriptionValidation")),
      permissionKeys: (value) => (value.length ? null : t("admin.permissionValidation")),
    },
  });
  const delegateForm = useForm<DelegationInput>({
    initialValues: {
      granteeUserId: "",
      expiresAt: null,
      permissionKeys: [],
      stewardedRoleIds: [],
      canCreateRoles: false,
    },
  });
  const invalidate = () => void qc.invalidateQueries({ queryKey: ["authorization"] });
  const mutationError = (title: string) => (error: unknown) =>
    notifications.show({ color: "red", title, message: errorText(error, t("common.requestFailed")) });
  const scopeForCreation = scopes.find((item) => item.id === selectedScopeId && item.canCreateRoles);
  const stewardedRoleIds = new Set(scopes.flatMap((item) => item.stewardedRoleIds));
  const allRoles = roles.data ?? [];
  const customRoles = allRoles.filter((role) => !role.isSystem && !role.isBuiltIn);
  const assignmentScope = scopes.find((item) => item.id === assignmentScopeId && item.assignableRoleIds.length > 0);
  const assignableRoleIds = new Set(
    isOwner ? customRoles.map((role) => role.id) : (assignmentScope?.assignableRoleIds ?? []),
  );
  const displayRoles = allRoles.filter(
    (role) => role.isSystem || role.isBuiltIn || isOwner || stewardedRoleIds.has(role.id),
  );
  const editableRole = (role: AuthorizationRole) => isOwner || stewardedRoleIds.has(role.id);
  const assignableRoles = isOwner ? customRoles : customRoles.filter((role) => assignableRoleIds.has(role.id));
  const qualifyingEditScopes = selectedRole
    ? scopes.filter((item) => item.stewardedRoleIds.includes(selectedRole.id))
    : [];
  const coveringEditScopes = selectedRole
    ? qualifyingEditScopes.filter((item) =>
        selectedRole.permissions.every((key) => item.grantablePermissionKeys.includes(key)),
      )
    : [];
  const selectedEditScope = selectedRole ? coveringEditScopes.find((item) => item.id === selectedScopeId) : undefined;
  const allowedPermissionKeys = new Set(
    isOwner
      ? (catalog.data ?? []).map((item) => item.key)
      : selectedRole
        ? (selectedEditScope?.grantablePermissionKeys ?? [])
        : (scopeForCreation?.grantablePermissionKeys ?? []),
  );
  const groupedPermissions = new Map<string, typeof catalog.data>();
  for (const permission of (catalog.data ?? []).filter((item) => allowedPermissionKeys.has(item.key))) {
    const key = `${translateHostPermissionModule(permission.key, permission.module, t)} · ${translateHostPermissionCategory(
      permission.key,
      permission.category,
      t,
    )}`;
    groupedPermissions.set(key, [...(groupedPermissions.get(key) ?? []), permission]);
  }
  const saveRole = useMutation({
    mutationFn: (input: RoleInput) =>
      selectedRole
        ? updateAuthorizationRole(selectedRole.id, { ...input, concurrencyStamp: selectedRole.version })
        : createAuthorizationRole(input),
    onSuccess: () => {
      closeRole();
      invalidate();
      notifications.show({
        color: "teal",
        title: selectedRole ? t("admin.roleUpdated") : t("admin.roleCreated"),
        message: t("admin.roleSaved"),
      });
    },
    onError: mutationError(t("admin.roleSaveFailed")),
  });
  const removeRole = useMutation({
    mutationFn: (role: AuthorizationRole) => deleteAuthorizationRole(role.id, role.version),
    onSuccess: invalidate,
    onError: mutationError(t("admin.roleDeleteFailed")),
  });
  const assignment = useMutation({
    mutationFn: ({ id, roleIds, version }: { id: string; roleIds: string[]; version: string }) =>
      assignUserRoles(id, {
        roleIds,
        concurrencyStamp: version,
        ...(!isOwner && assignmentScope ? { delegationId: assignmentScope.id } : {}),
      }),
    onSuccess: () => {
      invalidate();
      notifications.show({
        color: "teal",
        title: t("admin.rolesAssigned"),
        message: t("admin.rolesOutsideScope"),
      });
    },
    onError: mutationError(t("admin.rolesAssignFailed")),
  });
  const saveDelegation = useMutation({
    mutationFn: createDelegation,
    onSuccess: () => {
      closeDelegate();
      invalidate();
      notifications.show({
        color: "teal",
        title: t("admin.delegationGranted"),
        message: t("admin.delegationGrantMessage"),
      });
    },
    onError: mutationError(t("admin.delegationGrantFailed")),
  });
  const revoke = useMutation({
    mutationFn: (item: { id: string; version: string }) => revokeDelegation(item.id, item.version),
    onSuccess: invalidate,
    onError: mutationError(t("admin.delegationRevokeFailed")),
  });
  const access = useQuery({
    queryKey: ["authorization", "user", selectedUser],
    queryFn: () => {
      if (!selectedUser) throw new Error(t("admin.selectUserBeforeAccess"));
      return getUserAccess(selectedUser);
    },
    enabled: !!selectedUser && manageable,
  });
  const openRoleEditor = (role?: AuthorizationRole) => {
    setSelectedRole(role ?? null);
    const qualifyingScopes = role ? scopes.filter((item) => item.stewardedRoleIds.includes(role.id)) : [];
    const defaultScope = role
      ? qualifyingScopes.find((item) => role.permissions.every((key) => item.grantablePermissionKeys.includes(key)))
      : undefined;
    setSelectedScopeId(role && !isOwner ? (defaultScope?.id ?? null) : null);
    roleForm.setValues(
      role
        ? {
            name: role.name,
            displayName: role.displayName,
            description: role.description,
            permissionKeys: role.permissions,
          }
        : { name: "", displayName: "", description: "", permissionKeys: [] },
    );
    openRole();
  };
  if (me.isPending) return <Loader />;
  if (me.isError)
    return (
      <Alert color="red" title={t("admin.authUnavailable")}>
        {errorText(me.error, t("common.requestFailed"))}
      </Alert>
    );
  if (!manageable)
    return (
      <Alert color="yellow" title={t("admin.accessUnavailable")}>
        {t("admin.cannotManageRoles")}
      </Alert>
    );
  const canCreateRole = isOwner || scopes.some((item) => item.canCreateRoles);
  const canEditRole = (role: AuthorizationRole) => isOwner || coveringScopesFor(role).length > 0;
  const coveringScopesFor = (role: AuthorizationRole) =>
    scopes.filter(
      (item) =>
        item.stewardedRoleIds.includes(role.id) &&
        role.permissions.every((key) => item.grantablePermissionKeys.includes(key)),
    );
  return (
    <Stack maw={1180} mx="auto" gap="xl">
      <Group justify="space-between" align="flex-end">
        <div>
          <Group gap="sm">
            <IconShield size={30} color="var(--mantine-color-vantigo-6)" />
            <Title order={2}>{t("admin.rolesAccess")}</Title>
          </Group>
          <Text c="dimmed" mt={5}>
            {t("admin.rolesDescription")}
          </Text>
        </div>
        <Group>
          {activeSection === "roles" && canCreateRole && (
            <Button leftSection={<IconPlus size={16} />} onClick={() => openRoleEditor()}>
              {t("admin.createCustomRole")}
            </Button>
          )}
        </Group>
      </Group>
      <Tabs value={activeSection} onChange={(value) => setActiveSection(value ?? "roles")} keepMounted>
        <Tabs.List aria-label={t("admin.rolesAdministration")}>
          <Tabs.Tab value="roles">{t("admin.roles")}</Tabs.Tab>
          <Tabs.Tab value="assignments">{t("admin.assignments")}</Tabs.Tab>
          <Tabs.Tab value="delegations">{t("admin.delegations")}</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="roles" pt="xl">
          <Card withBorder>
            <Stack>
              <Group justify="space-between">
                <div>
                  <Title order={3}>{t("admin.rolePermissions")}</Title>
                  <Text size="sm" c="dimmed" mt={4}>
                    {t("admin.rolePermissionsDescription")}
                  </Text>
                </div>
                <Badge variant="light">
                  {displayRoles.filter((role) => !role.isSystem && !role.isBuiltIn).length} {t("admin.managedCustom")}
                </Badge>
              </Group>
              {roles.isPending || catalog.isPending ? (
                <Loader />
              ) : (
                <Stack>
                  {displayRoles.map((role) => {
                    const editable = editableRole(role) && canEditRole(role);
                    return (
                      <Card key={role.id} withBorder padding="sm">
                        <Group justify="space-between">
                          <div>
                            <Group gap="xs">
                              {(role.isSystem || role.isBuiltIn || !editable) && <IconLock size={15} />}
                              <Text fw={600}>
                                {role.isSystem || role.isBuiltIn
                                  ? translateHostRole(role.name, t, role.displayName)
                                  : role.displayName}
                              </Text>
                            </Group>
                            <Text size="xs" c="dimmed">
                              {role.isSystem || role.isBuiltIn
                                ? translateHostRoleDescription(role.name, role.description, t)
                                : role.description}
                            </Text>
                          </div>
                          {role.isSystem || role.isBuiltIn ? (
                            <Badge color="gray">{t("admin.protected")}</Badge>
                          ) : editable ? (
                            <Group>
                              <Button size="compact-sm" variant="subtle" onClick={() => openRoleEditor(role)}>
                                {t("admin.edit")}
                              </Button>
                              <Button
                                size="compact-sm"
                                color="red"
                                variant="subtle"
                                aria-label={t("admin.deleteRole")}
                                onClick={() =>
                                  modals.openConfirmModal({
                                    title: t("admin.deleteCustomRoleQuestion"),
                                    children: <Text size="sm">{t("admin.roleAssignmentsMayChange")}</Text>,
                                    labels: { confirm: t("admin.deleteRole"), cancel: t("common.cancel") },
                                    confirmProps: { color: "red" },
                                    onConfirm: () => removeRole.mutate(role),
                                  })
                                }
                              >
                                <IconTrash size={15} />
                              </Button>
                            </Group>
                          ) : (
                            <Badge color="gray">{t("admin.outsideDelegation")}</Badge>
                          )}
                        </Group>
                      </Card>
                    );
                  })}
                </Stack>
              )}
            </Stack>
          </Card>
        </Tabs.Panel>

        <Tabs.Panel value="assignments" pt="xl">
          <Card withBorder>
            <Stack>
              <Title order={3}>{t("admin.userAssignments")}</Title>
              <Text size="sm" c="dimmed">
                {t("admin.userAssignmentsDescription")}
              </Text>
              {!isOwner && (
                <Select
                  label={t("admin.assignmentScope")}
                  placeholder={t("admin.chooseOneScope")}
                  data={scopes
                    .filter((item) => item.assignableRoleIds.length > 0)
                    .map((item) => ({ value: item.id, label: t("admin.delegation", { id: item.id.slice(0, 8) }) }))}
                  value={assignmentScopeId}
                  onChange={setAssignmentScopeId}
                  required
                />
              )}
              <Select
                label={t("common.user")}
                placeholder={t("admin.selectUser")}
                searchable
                data={(users.data ?? [])
                  .filter((user) => user.id !== me.data?.id)
                  .map((user) => ({ value: user.id, label: user.displayName || user.email || user.id }))}
                value={selectedUser}
                onChange={setSelectedUser}
              />
              <Divider />
              {selectedUser && access.data && (isOwner || assignmentScope) ? (
                <>
                  <Group>
                    <Text fw={600}>{t("admin.currentRoles")}</Text>
                    {access.data.roles.map((role) => (
                      <Badge key={role} color={role === "Owner" ? "violet" : undefined}>
                        {translateHostRole(role, t)}
                      </Badge>
                    ))}
                  </Group>
                  <Text size="sm" c="dimmed">
                    {t("admin.effectivePermissions")}{" "}
                    {access.data.permissions.includes("*")
                      ? t("admin.allPermissions")
                      : access.data.permissions.map((key) => translateHostPermissionKey(key, t)).join(", ") ||
                        t("admin.none")}
                  </Text>
                  <MultiSelect
                    label={t("admin.assignableRoles")}
                    data={assignableRoles.map((role) => ({ value: role.id, label: role.displayName }))}
                    value={assignableRoles
                      .filter((role) => access.data?.roleIds?.includes(role.id))
                      .map((role) => role.id)}
                    onChange={(roleIds) =>
                      selectedUser && access.data
                        ? assignment.mutate({ id: selectedUser, roleIds, version: access.data.version })
                        : undefined
                    }
                    disabled={assignment.isPending}
                  />
                </>
              ) : selectedUser ? (
                <Loader />
              ) : (
                <Text size="sm" c="dimmed">
                  {t("admin.reviewAccess")}
                </Text>
              )}
            </Stack>
          </Card>
        </Tabs.Panel>

        <Tabs.Panel value="delegations" pt="xl">
          <Card withBorder>
            <Stack>
              <Group justify="space-between">
                <div>
                  <Title order={3}>{t("admin.delegatedAdministration")}</Title>
                  <Text size="sm" c="dimmed" mt={4}>
                    {t("admin.delegatedDescription")}
                  </Text>
                </div>
                {isOwner && (
                  <Button variant="light" leftSection={<IconUsers size={16} />} onClick={openDelegate}>
                    {t("admin.delegateAdministration")}
                  </Button>
                )}
              </Group>
              {!isOwner && (
                <Text size="sm" c="dimmed">
                  {t("admin.ownerManagesDelegations")}
                </Text>
              )}
              {isOwner && (
                <>
                  <Text size="sm" c="dimmed">
                    {t("admin.activeBoundaries")}
                  </Text>
                  {(delegations.data ?? [])
                    .filter((item) => !item.revokedAt)
                    .map((item) => (
                      <Group key={item.id} justify="space-between">
                        <Text>
                          {users.data?.find((user) => user.id === item.granteeUserId)?.displayName ||
                            item.granteeUserId}
                        </Text>
                        <Button
                          size="compact-sm"
                          color="red"
                          variant="subtle"
                          onClick={() =>
                            modals.openConfirmModal({
                              title: t("admin.revokeDelegationQuestion"),
                              children: <Text size="sm">{t("admin.removeDelegationBoundary")}</Text>,
                              labels: { confirm: t("admin.revoke"), cancel: t("common.cancel") },
                              confirmProps: { color: "red" },
                              onConfirm: () => revoke.mutate({ id: item.id, version: item.version }),
                            })
                          }
                        >
                          {t("admin.revoke")}
                        </Button>
                      </Group>
                    ))}
                </>
              )}
            </Stack>
          </Card>
        </Tabs.Panel>
      </Tabs>
      <Modal
        opened={roleModal}
        onClose={closeRole}
        title={selectedRole ? t("admin.editCustomRole") : t("admin.createCustomRole")}
        size="lg"
      >
        <form
          onSubmit={roleForm.onSubmit((values) => {
            const scopeForSubmit = selectedRole ? selectedEditScope : scopeForCreation;
            if (
              !isOwner &&
              (!scopeForSubmit ||
                (selectedRole &&
                  !selectedRole.permissions.every((key) => scopeForSubmit.grantablePermissionKeys.includes(key))))
            )
              return;
            saveRole.mutate({
              ...values,
              permissionKeys: selectedRole
                ? values.permissionKeys
                : values.permissionKeys.filter((key) => allowedPermissionKeys.has(key)),
              ...(!isOwner && !selectedRole && scopeForSubmit ? { delegationId: scopeForSubmit.id } : {}),
            });
          })}
        >
          <Stack>
            {!isOwner && (
              <Select
                label={t("admin.delegationScope")}
                placeholder={t("admin.chooseScope")}
                data={(selectedRole ? coveringEditScopes : scopes.filter((item) => item.canCreateRoles)).map(
                  (item) => ({ value: item.id, label: t("admin.delegation", { id: item.id.slice(0, 8) }) }),
                )}
                value={selectedScopeId}
                onChange={(value) => {
                  setSelectedScopeId(value);
                  if (!selectedRole) roleForm.setFieldValue("permissionKeys", []);
                }}
                required
              />
            )}
            {!isOwner && !selectedScopeId && (
              <Text size="sm" c="dimmed">
                {t("admin.chooseScopePermissions")}
              </Text>
            )}
            <TextInput label={t("admin.internalName")} disabled={!!selectedRole} {...roleForm.getInputProps("name")} />
            <TextInput label={t("common.displayName")} {...roleForm.getInputProps("displayName")} />
            <TextInput label={t("admin.description")} {...roleForm.getInputProps("description")} />
            {(isOwner || (selectedRole ? selectedEditScope : scopeForCreation)) &&
              [...groupedPermissions.entries()].map(([group, permissions]) => (
                <Stack key={group} gap="xs">
                  <Text fw={600}>{group}</Text>
                  {permissions?.map((permission) => (
                    <Checkbox
                      key={permission.key}
                      label={
                        <Group gap="xs">
                          <span>{translateHostPermissionKey(permission.key, t, permission.displayName)}</span>
                          {permission.sensitive && <Badge color="orange">{t("admin.sensitive")}</Badge>}
                        </Group>
                      }
                      description={translateHostPermissionDescription(permission.key, permission.description, t)}
                      checked={roleForm.values.permissionKeys.includes(permission.key)}
                      onChange={(event) =>
                        roleForm.setFieldValue(
                          "permissionKeys",
                          event.currentTarget.checked
                            ? [...roleForm.values.permissionKeys, permission.key]
                            : roleForm.values.permissionKeys.filter((key) => key !== permission.key),
                        )
                      }
                    />
                  ))}
                </Stack>
              ))}
            <Button
              type="submit"
              loading={saveRole.isPending}
              disabled={!isOwner && !(selectedRole ? selectedEditScope : scopeForCreation)}
            >
              {t("admin.saveRole")}
            </Button>
          </Stack>
        </form>
      </Modal>
      {isOwner && (
        <Modal opened={delegateModal} onClose={closeDelegate} title={t("admin.delegateRoleAdministration")}>
          <form onSubmit={delegateForm.onSubmit((values) => saveDelegation.mutate(values))}>
            <Stack>
              <Text size="sm" c="dimmed">
                {t("admin.delegateDescription")}
              </Text>
              <Select
                label={t("admin.administrator")}
                data={(users.data ?? [])
                  .filter((user) => user.id !== me.data?.id)
                  .map((user) => ({ value: user.id, label: user.displayName || user.email || user.id }))}
                {...delegateForm.getInputProps("granteeUserId")}
              />
              <MultiSelect
                label={t("admin.delegablePermissions")}
                data={(catalog.data ?? [])
                  .filter((item) => item.delegable)
                  .map((item) => ({
                    value: item.key,
                    label: translateHostPermissionKey(item.key, t, item.displayName),
                  }))}
                {...delegateForm.getInputProps("permissionKeys")}
              />
              <MultiSelect
                label={t("admin.stewardedRoles")}
                data={customRoles.map((role) => ({ value: role.id, label: role.displayName }))}
                {...delegateForm.getInputProps("stewardedRoleIds")}
              />
              <TextInput
                label={t("admin.expiryOptional")}
                placeholder={t("admin.expiryPlaceholder")}
                {...delegateForm.getInputProps("expiresAt")}
              />
              <Checkbox
                label={t("admin.mayCreateRoles")}
                {...delegateForm.getInputProps("canCreateRoles", { type: "checkbox" })}
              />
              <Button type="submit" loading={saveDelegation.isPending}>
                {t("admin.grantDelegation")}
              </Button>
            </Stack>
          </form>
        </Modal>
      )}
    </Stack>
  );
};

export const Route = createFileRoute("/admin/roles")({
  beforeLoad: async ({ context }) => {
    const access = await context.queryClient.fetchQuery({
      queryKey: ["authorization", "me"],
      queryFn: getAuthorizationMe,
    });
    if (!access.canManageAuthorization) throw redirect({ to: "/" });
  },
  component: RolesPage,
});
