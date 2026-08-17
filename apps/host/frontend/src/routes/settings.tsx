import {
  Alert,
  Avatar,
  Badge,
  Button,
  Card,
  FileInput,
  Group,
  Modal,
  PasswordInput,
  Select,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconKey, IconLock, IconShieldLock, IconUser } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Outlet } from "@tanstack/react-router";
import { setLanguagePreference, useTranslation } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import "../i18n";
import {
  changePassword,
  disableMfa,
  enableMfa,
  enrollPasskey,
  getMfaStatus,
  getProfile,
  initializeMfa,
  isPasskeyClientError,
  listPasskeys,
  profileQueryKey,
  regenerateRecoveryCodes,
  removePasskey,
  removeProfilePhoto,
  updateProfile,
  uploadProfilePhoto,
} from "../api/account";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { SettingsLayout } from "../components/settings-layout";

const mfaKey = ["account", "mfa"] as const;
const passkeyKey = ["account", "passkeys"] as const;
const message = (e: unknown, fallback = "The request could not be completed.") =>
  e instanceof Error ? e.message : fallback;
const isOidcError = (e: unknown) => {
  const value = e as { code?: string };
  return value.code === "local_password_unavailable";
};
const notify = (title: string, e: unknown, fallback?: string) =>
  notifications.show({ title, message: message(e, fallback), color: "red" });

export function ProfileTab() {
  const { t } = useTranslation("settings");
  const qc = useQueryClient();
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
  const userId = session.data?.user.id;
  const query = useQuery({ queryKey: profileQueryKey(userId ?? "unknown"), queryFn: getProfile, enabled: !!userId });
  const [file, setFile] = useState<File | null>(null);
  const form = useForm<{ displayName: string; preferredLanguage: "auto" | "en" | "nb" }>({
    initialValues: { displayName: "", preferredLanguage: "auto" },
    validate: { displayName: (v) => (v.trim() ? null : t("enterYourName")) },
  });
  useEffect(() => {
    if (query.data)
      form.setValues({ displayName: query.data.displayName, preferredLanguage: query.data.preferredLanguage });
  }, [query.data]); // eslint-disable-line react-hooks/exhaustive-deps
  const save = useMutation({
    mutationFn: updateProfile,
    onSuccess: (v) => {
      if (userId) qc.setQueryData(profileQueryKey(userId), v);
      void qc.invalidateQueries({ queryKey: sessionQueryKey });
      setLanguagePreference(v.preferredLanguage);
      notifications.show({ title: t("saved"), message: t("profileUpdated"), color: "teal" });
    },
    onError: (e) => notify(t("profileCouldNotSave"), e, t("requestCouldNotComplete")),
  });
  const upload = useMutation({
    mutationFn: uploadProfilePhoto,
    onSuccess: () => {
      setFile(null);
      if (userId) void qc.invalidateQueries({ queryKey: profileQueryKey(userId) });
      void qc.invalidateQueries({ queryKey: sessionQueryKey });
      notifications.show({ title: t("saved"), message: t("photoUpdated"), color: "teal" });
    },
    onError: (e) => notify(t("photoCouldNotUpload"), e, t("requestCouldNotComplete")),
  });
  const remove = useMutation({
    mutationFn: removeProfilePhoto,
    onSuccess: () => {
      if (userId) void qc.invalidateQueries({ queryKey: profileQueryKey(userId) });
      void qc.invalidateQueries({ queryKey: sessionQueryKey });
      notifications.show({ title: t("saved"), message: t("photoRemoved"), color: "teal" });
    },
    onError: (e) => notify(t("photoCouldNotRemove"), e, t("requestCouldNotComplete")),
  });
  if (query.isPending) return <Text c="dimmed">{t("loadingProfile")}</Text>;
  if (query.isError)
    return (
      <Alert color="red" title={t("profileCouldNotLoad")}>
        {message(query.error, t("requestCouldNotComplete"))}
      </Alert>
    );
  const value = query.data;
  return (
    <Stack gap="lg">
      <Card withBorder>
        <Stack>
          <Group>
            <Avatar src={value.avatarUrl} size="lg" radius="xl">
              <IconUser />
            </Avatar>
            <div>
              <Text fw={600}>{t("profilePhoto")}</Text>
              <Text size="sm" c="dimmed">
                {t("photoRequirements")}
              </Text>
            </div>
          </Group>
          <Group align="end">
            <FileInput
              flex={1}
              label={t("choosePhoto")}
              accept="image/jpeg,image/png"
              value={file}
              onChange={setFile}
            />
            <Button disabled={!file} loading={upload.isPending} onClick={() => file && upload.mutate(file)}>
              {t("uploadPhoto")}
            </Button>
            {value.avatarUrl && (
              <Button color="red" variant="subtle" loading={remove.isPending} onClick={() => remove.mutate()}>
                {t("remove")}
              </Button>
            )}
          </Group>
        </Stack>
      </Card>
      <Card withBorder>
        <form onSubmit={form.onSubmit((v) => save.mutate(v))}>
          <Stack>
            <TextInput label={t("name")} {...form.getInputProps("displayName")} />
            <TextInput label={t("email")} value={value.email ?? ""} readOnly description={t("emailDescription")} />
            <Select
              label={t("preferredLanguage")}
              data={[
                { value: "auto", label: t("automatic") },
                { value: "nb", label: t("norwegian") },
                { value: "en", label: t("english") },
              ]}
              {...form.getInputProps("preferredLanguage")}
              description={t("languageDescription")}
            />
            <Button type="submit" loading={save.isPending} w="fit-content">
              {t("saveProfile")}
            </Button>
          </Stack>
        </form>
      </Card>
    </Stack>
  );
}

export function SecurityTab() {
  const { t } = useTranslation("settings");
  const { t: hostT } = useTranslation("host");
  const qc = useQueryClient();
  const mfa = useQuery({ queryKey: mfaKey, queryFn: getMfaStatus });
  const passkeys = useQuery({ queryKey: passkeyKey, queryFn: listPasskeys });
  const [oidcOnly, setOidcOnly] = useState(false);
  const [setup, setSetup] = useState<{ sharedKey: string | null; authenticatorUri: string | null } | null>(null);
  const [recovery, setRecovery] = useState<string[]>([]);
  const [removeId, setRemoveId] = useState<string | null>(null);
  const [disableOpen, setDisableOpen] = useState(false);
  const [recoveryOpen, setRecoveryOpen] = useState(false);
  const password = useForm({
    initialValues: { currentPassword: "", newPassword: "", confirm: "" },
    validate: {
      newPassword: (v) => (v.length < 12 ? t("useAtLeast12Characters") : null),
      confirm: (v, values) => (v !== values.newPassword ? t("passwordsDoNotMatch") : null),
    },
  });
  const mfaForm = useForm({ initialValues: { password: "", code: "" } });
  const reauth = useForm({ initialValues: { password: "", code: "" } });
  const passkeyForm = useForm({
    initialValues: { name: "", currentPassword: "" },
    validate: {
      name: (v) => (v.trim() ? null : t("enterYourName")),
      currentPassword: (v) => (v ? null : t("currentPassword")),
    },
  });
  const onMfaError = (title: string) => (e: unknown) => {
    if (isOidcError(e)) setOidcOnly(true);
    notify(title, e, t("requestCouldNotComplete"));
  };
  const change = useMutation({
    mutationFn: changePassword,
    onSuccess: () => {
      password.reset();
      notifications.show({ title: t("saved"), message: t("passwordChanged"), color: "teal" });
    },
    onError: onMfaError(t("passwordCouldNotBeChanged")),
  });
  const start = useMutation({
    mutationFn: initializeMfa,
    onSuccess: setSetup,
    onError: onMfaError(t("authenticatorSetupCouldNotStartTitle")),
  });
  const enable = useMutation({
    mutationFn: enableMfa,
    onSuccess: (v) => {
      setRecovery(v.recoveryCodes);
      setSetup(null);
      mfaForm.reset();
      void qc.invalidateQueries({ queryKey: mfaKey });
    },
    onError: onMfaError(t("authenticatorCodeNotAcceptedTitle")),
  });
  const disable = useMutation({
    mutationFn: disableMfa,
    onSuccess: () => {
      setDisableOpen(false);
      reauth.reset();
      void qc.invalidateQueries({ queryKey: mfaKey });
      notifications.show({ title: t("saved"), message: t("authenticatorDisabled"), color: "teal" });
    },
    onError: onMfaError(t("authenticatorCouldNotDisable")),
  });
  const regenerate = useMutation({
    mutationFn: regenerateRecoveryCodes,
    onSuccess: (v) => {
      setRecovery(v.recoveryCodes);
      setRecoveryOpen(false);
      reauth.reset();
    },
    onError: onMfaError(t("recoveryCodesCouldNotRegenerate")),
  });
  const remove = useMutation({
    mutationFn: ({ id, password: currentPassword }: { id: string; password: string }) =>
      removePasskey(id, currentPassword),
    onSuccess: () => {
      setRemoveId(null);
      reauth.reset();
      void qc.invalidateQueries({ queryKey: passkeyKey });
      notifications.show({ title: t("saved"), message: t("passkeyRemoved"), color: "teal" });
    },
    onError: onMfaError(t("passwordToRemovePasskey")),
  });
  const enroll = useMutation({
    mutationFn: enrollPasskey,
    onSuccess: () => {
      passkeyForm.reset();
      void qc.invalidateQueries({ queryKey: passkeyKey });
      notifications.show({ title: t("saved"), message: t("passkeyAdded"), color: "teal" });
    },
    onError: (e) =>
      notifications.show({
        title: t("passkeyCouldNotAdd"),
        message: isPasskeyClientError(e)
          ? e.code === "unsupported"
            ? hostT("settings.passkeyUnsupported")
            : e.code === "cancelled"
              ? hostT("settings.passkeyEnrollmentCancelled")
              : hostT("settings.passkeyEnrollmentFailed")
          : message(e, t("requestCouldNotComplete")),
        color: "red",
      }),
  });
  return (
    <Stack gap="lg">
      <Card withBorder>
        <Stack>
          <Group justify="space-between">
            <div>
              <Title order={4}>{t("password")}</Title>
              <Text size="sm" c="dimmed">
                {t("passwordDescription")}
              </Text>
            </div>
            <IconLock size={22} />
          </Group>
          {oidcOnly && (
            <Alert color="blue" title={t("localPasswordUnavailable")}>
              {t("localPasswordUnavailableMessage")}
            </Alert>
          )}
          <form
            onSubmit={password.onSubmit((v) =>
              change.mutate({ currentPassword: v.currentPassword, newPassword: v.newPassword }),
            )}
          >
            <Stack>
              <PasswordInput
                label={t("currentPassword")}
                disabled={oidcOnly}
                {...password.getInputProps("currentPassword")}
              />
              <PasswordInput label={t("newPassword")} disabled={oidcOnly} {...password.getInputProps("newPassword")} />
              <PasswordInput
                label={t("confirmNewPassword")}
                disabled={oidcOnly}
                {...password.getInputProps("confirm")}
              />
              <Button type="submit" disabled={oidcOnly} loading={change.isPending} w="fit-content">
                {t("changePassword")}
              </Button>
            </Stack>
          </form>
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Group>
            <IconShieldLock size={22} />
            <div>
              <Title order={4}>{t("authenticatorApp")}</Title>
              <Text size="sm" c="dimmed">
                {t("authenticatorDescription")}
              </Text>
            </div>
          </Group>
          {mfa.data?.twoFactorEnabled ? (
            <>
              <Badge color="teal" w="fit-content">
                {t("enabled")}
              </Badge>
              <Group>
                <Button color="red" variant="light" disabled={oidcOnly} onClick={() => setDisableOpen(true)}>
                  {t("disable")}
                </Button>
                <Button variant="subtle" disabled={oidcOnly} onClick={() => setRecoveryOpen(true)}>
                  {t("regenerateRecoveryCodes")}
                </Button>
              </Group>
            </>
          ) : (
            <Button disabled={oidcOnly} loading={start.isPending} onClick={() => mfaForm.reset()} w="fit-content">
              {t("setUpAuthenticator")}
            </Button>
          )}
          {!mfa.data?.twoFactorEnabled && (
            <form onSubmit={mfaForm.onSubmit((v) => start.mutate(v.password))}>
              <Stack>
                <PasswordInput
                  label={t("currentPasswordToBegin")}
                  disabled={oidcOnly}
                  {...mfaForm.getInputProps("password")}
                />
                <Button type="submit" loading={start.isPending} disabled={oidcOnly} w="fit-content">
                  {t("beginSetup")}
                </Button>
              </Stack>
            </form>
          )}
          {setup && (
            <Stack>
              <Text size="sm">{t("scanSetupUri")}</Text>
              <TextInput label={t("setupUri")} value={setup.authenticatorUri ?? t("unavailable")} readOnly />
              <form onSubmit={mfaForm.onSubmit((v) => enable.mutate({ code: v.code, password: v.password }))}>
                <PasswordInput label={t("currentPassword")} {...mfaForm.getInputProps("password")} />
                <TextInput label={t("authenticatorCode")} {...mfaForm.getInputProps("code")} />
                <Button type="submit" loading={enable.isPending}>
                  {t("enable")}
                </Button>
              </form>
            </Stack>
          )}
          {recovery.length > 0 && (
            <Alert color="yellow" title={t("saveRecoveryCodesNow")}>
              {recovery.join(" · ")}
            </Alert>
          )}
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Group>
            <IconKey size={22} />
            <div>
              <Title order={4}>{t("passkeys")}</Title>
              <Text size="sm" c="dimmed">
                {t("passkeysDescription")}
              </Text>
            </div>
          </Group>
          {passkeys.isError ? (
            <Alert color="red">{message(passkeys.error, t("requestCouldNotComplete"))}</Alert>
          ) : passkeys.data?.length ? (
            passkeys.data.map((key) => (
              <Group key={key.credentialId} justify="space-between">
                <Text>{key.name}</Text>
                <Button color="red" variant="subtle" onClick={() => setRemoveId(key.credentialId)}>
                  {t("remove")}
                </Button>
              </Group>
            ))
          ) : (
            <Text c="dimmed">{t("noPasskeys")}</Text>
          )}
          <form onSubmit={passkeyForm.onSubmit((v) => enroll.mutate(v))}>
            <Stack>
              <TextInput
                label={t("passkeyName")}
                placeholder={t("passkeyNamePlaceholder")}
                {...passkeyForm.getInputProps("name")}
              />
              <PasswordInput label={t("currentPassword")} {...passkeyForm.getInputProps("currentPassword")} />
              <Button type="submit" loading={enroll.isPending} variant="light">
                {t("addPasskey")}
              </Button>
            </Stack>
          </form>
          <Text size="xs" c="dimmed">
            {t("passkeyVerification")}
          </Text>
        </Stack>
      </Card>
      <Modal opened={disableOpen} onClose={() => setDisableOpen(false)} title={t("disableAuthenticatorTitle")}>
        <form onSubmit={reauth.onSubmit((v) => disable.mutate(v.password))}>
          <Stack>
            <Text size="sm">{t("sensitiveChangeDescription")}</Text>
            <PasswordInput label={t("currentPassword")} {...reauth.getInputProps("password")} />
            <Button color="red" type="submit" loading={disable.isPending}>
              {t("disableAuthenticator")}
            </Button>
          </Stack>
        </form>
      </Modal>
      <Modal opened={recoveryOpen} onClose={() => setRecoveryOpen(false)} title={t("regenerateRecoveryCodes")}>
        <form onSubmit={reauth.onSubmit((v) => regenerate.mutate({ password: v.password, code: v.code }))}>
          <Stack>
            <PasswordInput label={t("currentPassword")} {...reauth.getInputProps("password")} />
            <TextInput label={t("authenticatorCode")} {...reauth.getInputProps("code")} />
            <Button type="submit" loading={regenerate.isPending}>
              {t("regenerateCodes")}
            </Button>
          </Stack>
        </form>
      </Modal>
      <Modal opened={removeId !== null} onClose={() => setRemoveId(null)} title={t("removePasskeyTitle")}>
        <form
          onSubmit={reauth.onSubmit((v) => {
            if (removeId) remove.mutate({ id: removeId, password: v.password });
          })}
        >
          <Stack>
            <Text>{t("cannotUndoDescription")}</Text>
            <PasswordInput label={t("currentPassword")} {...reauth.getInputProps("password")} />
            <Button color="red" type="submit" loading={remove.isPending}>
              {t("removePasskey")}
            </Button>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
}
const SettingsPage = () => <SettingsContent />;

function SettingsContent() {
  const { t } = useTranslation("settings");
  return (
    <SettingsLayout
      title={t("settings")}
      description={t("settingsDescription")}
      sections={[
        { label: t("profile"), to: "/settings/profile", icon: IconUser },
        { label: t("security"), to: "/settings/security", icon: IconShieldLock },
      ]}
    >
      <Outlet />
    </SettingsLayout>
  );
}
export const Route = createFileRoute("/settings")({
  beforeLoad: async ({ context }) => {
    await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
  },
  component: SettingsPage,
});
