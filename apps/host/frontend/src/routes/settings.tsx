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
  Tabs,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconKey, IconLock, IconShieldLock, IconUser } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import {
  changePassword,
  disableMfa,
  enableMfa,
  enrollPasskey,
  getMfaStatus,
  getProfile,
  initializeMfa,
  listPasskeys,
  regenerateRecoveryCodes,
  removePasskey,
  removeProfilePhoto,
  updateProfile,
  uploadProfilePhoto,
} from "../api/account";
import { fetchSession, sessionQueryKey } from "../api/auth";

const profileKey = ["account", "profile"] as const;
const mfaKey = ["account", "mfa"] as const;
const passkeyKey = ["account", "passkeys"] as const;
const message = (e: unknown) => (e instanceof Error ? e.message : "The request could not be completed.");
const isOidcError = (e: unknown) => {
  const value = e as { code?: string };
  return value.code === "local_password_unavailable";
};
const notify = (title: string, e: unknown) => notifications.show({ title, message: message(e), color: "red" });
const saved = (text: string) => notifications.show({ title: "Saved", message: text, color: "teal" });

function ProfileTab() {
  const qc = useQueryClient();
  const query = useQuery({ queryKey: profileKey, queryFn: getProfile });
  const [file, setFile] = useState<File | null>(null);
  const form = useForm<{ displayName: string; preferredLanguage: "auto" | "en" }>({
    initialValues: { displayName: "", preferredLanguage: "auto" },
    validate: { displayName: (v) => (v.trim() ? null : "Enter your name") },
  });
  useEffect(() => {
    if (query.data)
      form.setValues({ displayName: query.data.displayName, preferredLanguage: query.data.preferredLanguage });
  }, [query.data]); // eslint-disable-line react-hooks/exhaustive-deps
  const save = useMutation({
    mutationFn: updateProfile,
    onSuccess: (v) => {
      qc.setQueryData(profileKey, v);
      void qc.invalidateQueries({ queryKey: sessionQueryKey });
      saved("Your profile was updated.");
    },
    onError: (e) => notify("Profile could not be saved", e),
  });
  const upload = useMutation({
    mutationFn: uploadProfilePhoto,
    onSuccess: () => {
      setFile(null);
      void qc.invalidateQueries({ queryKey: profileKey });
      void qc.invalidateQueries({ queryKey: sessionQueryKey });
      saved("Your profile photo was updated.");
    },
    onError: (e) => notify("Photo could not be uploaded", e),
  });
  const remove = useMutation({
    mutationFn: removeProfilePhoto,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: profileKey });
      saved("Your profile photo was removed.");
    },
    onError: (e) => notify("Photo could not be removed", e),
  });
  if (query.isPending) return <Text c="dimmed">Loading profile…</Text>;
  if (query.isError)
    return (
      <Alert color="red" title="Profile could not be loaded">
        {message(query.error)}
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
              <Text fw={600}>Profile photo</Text>
              <Text size="sm" c="dimmed">
                JPEG or PNG, up to 5 MB.
              </Text>
            </div>
          </Group>
          <Group align="end">
            <FileInput flex={1} label="Choose a photo" accept="image/jpeg,image/png" value={file} onChange={setFile} />
            <Button disabled={!file} loading={upload.isPending} onClick={() => file && upload.mutate(file)}>
              Upload photo
            </Button>
            {value.avatarUrl && (
              <Button color="red" variant="subtle" loading={remove.isPending} onClick={() => remove.mutate()}>
                Remove
              </Button>
            )}
          </Group>
        </Stack>
      </Card>
      <Card withBorder>
        <form onSubmit={form.onSubmit((v) => save.mutate(v))}>
          <Stack>
            <TextInput label="Name" {...form.getInputProps("displayName")} />
            <TextInput
              label="Email"
              value={value.email ?? ""}
              readOnly
              description="Your email is managed by your sign-in provider."
            />
            <Select
              label="Preferred language"
              data={[
                { value: "auto", label: "Automatic" },
                { value: "en", label: "English" },
              ]}
              {...form.getInputProps("preferredLanguage")}
              description="App translation is coming later. This preference is saved for your account."
            />
            <Button type="submit" loading={save.isPending} w="fit-content">
              Save profile
            </Button>
          </Stack>
        </form>
      </Card>
    </Stack>
  );
}

function SecurityTab() {
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
      newPassword: (v) => (v.length < 12 ? "Use at least 12 characters" : null),
      confirm: (v, values) => (v !== values.newPassword ? "Passwords do not match" : null),
    },
  });
  const mfaForm = useForm({ initialValues: { password: "", code: "" } });
  const reauth = useForm({ initialValues: { password: "", code: "" } });
  const passkeyForm = useForm({
    initialValues: { name: "", currentPassword: "" },
    validate: {
      name: (v) => (v.trim() ? null : "Enter a name"),
      currentPassword: (v) => (v ? null : "Enter your current password"),
    },
  });
  const onMfaError = (title: string) => (e: unknown) => {
    if (isOidcError(e)) setOidcOnly(true);
    notify(title, e);
  };
  const change = useMutation({
    mutationFn: changePassword,
    onSuccess: () => {
      password.reset();
      saved("Your password was changed.");
    },
    onError: onMfaError("Password could not be changed"),
  });
  const start = useMutation({
    mutationFn: initializeMfa,
    onSuccess: setSetup,
    onError: onMfaError("Authenticator setup could not start"),
  });
  const enable = useMutation({
    mutationFn: enableMfa,
    onSuccess: (v) => {
      setRecovery(v.recoveryCodes);
      setSetup(null);
      mfaForm.reset();
      void qc.invalidateQueries({ queryKey: mfaKey });
    },
    onError: onMfaError("Authenticator code was not accepted"),
  });
  const disable = useMutation({
    mutationFn: disableMfa,
    onSuccess: () => {
      setDisableOpen(false);
      reauth.reset();
      void qc.invalidateQueries({ queryKey: mfaKey });
      saved("Authenticator sign-in was disabled.");
    },
    onError: onMfaError("Authenticator sign-in could not be disabled"),
  });
  const regenerate = useMutation({
    mutationFn: regenerateRecoveryCodes,
    onSuccess: (v) => {
      setRecovery(v.recoveryCodes);
      setRecoveryOpen(false);
      reauth.reset();
    },
    onError: onMfaError("Recovery codes could not be regenerated"),
  });
  const remove = useMutation({
    mutationFn: ({ id, password: currentPassword }: { id: string; password: string }) =>
      removePasskey(id, currentPassword),
    onSuccess: () => {
      setRemoveId(null);
      reauth.reset();
      void qc.invalidateQueries({ queryKey: passkeyKey });
      saved("Passkey removed.");
    },
    onError: onMfaError("Passkey could not be removed"),
  });
  const enroll = useMutation({
    mutationFn: enrollPasskey,
    onSuccess: () => {
      passkeyForm.reset();
      void qc.invalidateQueries({ queryKey: passkeyKey });
      saved("Passkey added.");
    },
    onError: (e) => notify("Passkey could not be added", e),
  });
  return (
    <Stack gap="lg">
      <Card withBorder>
        <Stack>
          <Group justify="space-between">
            <div>
              <Title order={4}>Password</Title>
              <Text size="sm" c="dimmed">
                Use a unique password you do not reuse elsewhere.
              </Text>
            </div>
            <IconLock size={22} />
          </Group>
          {oidcOnly && (
            <Alert color="blue" title="Local password unavailable">
              This account signs in with an organization identity provider. Manage your password and security there.
            </Alert>
          )}
          <form
            onSubmit={password.onSubmit((v) =>
              change.mutate({ currentPassword: v.currentPassword, newPassword: v.newPassword }),
            )}
          >
            <Stack>
              <PasswordInput
                label="Current password"
                disabled={oidcOnly}
                {...password.getInputProps("currentPassword")}
              />
              <PasswordInput label="New password" disabled={oidcOnly} {...password.getInputProps("newPassword")} />
              <PasswordInput label="Confirm new password" disabled={oidcOnly} {...password.getInputProps("confirm")} />
              <Button type="submit" disabled={oidcOnly} loading={change.isPending} w="fit-content">
                Change password
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
              <Title order={4}>Authenticator app</Title>
              <Text size="sm" c="dimmed">
                Add a second step when you sign in.
              </Text>
            </div>
          </Group>
          {mfa.data?.twoFactorEnabled ? (
            <>
              <Badge color="teal" w="fit-content">
                Enabled
              </Badge>
              <Group>
                <Button color="red" variant="light" disabled={oidcOnly} onClick={() => setDisableOpen(true)}>
                  Disable
                </Button>
                <Button variant="subtle" disabled={oidcOnly} onClick={() => setRecoveryOpen(true)}>
                  Regenerate recovery codes
                </Button>
              </Group>
            </>
          ) : (
            <Button disabled={oidcOnly} loading={start.isPending} onClick={() => mfaForm.reset()} w="fit-content">
              Set up authenticator app
            </Button>
          )}
          {!mfa.data?.twoFactorEnabled && (
            <form onSubmit={mfaForm.onSubmit((v) => start.mutate(v.password))}>
              <Stack>
                <PasswordInput
                  label="Current password to begin setup"
                  disabled={oidcOnly}
                  {...mfaForm.getInputProps("password")}
                />
                <Button type="submit" loading={start.isPending} disabled={oidcOnly} w="fit-content">
                  Begin setup
                </Button>
              </Stack>
            </form>
          )}
          {setup && (
            <Stack>
              <Text size="sm">Scan this setup URI in your authenticator app, then confirm with a six-digit code.</Text>
              <TextInput label="Setup URI" value={setup.authenticatorUri ?? "Unavailable"} readOnly />
              <form onSubmit={mfaForm.onSubmit((v) => enable.mutate({ code: v.code, password: v.password }))}>
                <PasswordInput label="Current password" {...mfaForm.getInputProps("password")} />
                <TextInput label="Authenticator code" {...mfaForm.getInputProps("code")} />
                <Button type="submit" loading={enable.isPending}>
                  Enable
                </Button>
              </form>
            </Stack>
          )}
          {recovery.length > 0 && (
            <Alert color="yellow" title="Save your recovery codes now">
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
              <Title order={4}>Passkeys</Title>
              <Text size="sm" c="dimmed">
                Use a device or security key as an alternative sign-in.
              </Text>
            </div>
          </Group>
          {passkeys.isError ? (
            <Alert color="red">{message(passkeys.error)}</Alert>
          ) : passkeys.data?.length ? (
            passkeys.data.map((key) => (
              <Group key={key.credentialId} justify="space-between">
                <Text>{key.name}</Text>
                <Button color="red" variant="subtle" onClick={() => setRemoveId(key.credentialId)}>
                  Remove
                </Button>
              </Group>
            ))
          ) : (
            <Text c="dimmed">No passkeys registered.</Text>
          )}
          <form onSubmit={passkeyForm.onSubmit((v) => enroll.mutate(v))}>
            <Stack>
              <TextInput label="Passkey name" placeholder="e.g. MacBook" {...passkeyForm.getInputProps("name")} />
              <PasswordInput label="Current password" {...passkeyForm.getInputProps("currentPassword")} />
              <Button type="submit" loading={enroll.isPending} variant="light">
                Add a passkey
              </Button>
            </Stack>
          </form>
          <Text size="xs" c="dimmed">
            Your browser or device will ask you to verify with the passkey.
          </Text>
        </Stack>
      </Card>
      <Modal opened={disableOpen} onClose={() => setDisableOpen(false)} title="Disable authenticator sign-in">
        <form onSubmit={reauth.onSubmit((v) => disable.mutate(v.password))}>
          <Stack>
            <Text size="sm">This is a sensitive change. Confirm with your current password.</Text>
            <PasswordInput label="Current password" {...reauth.getInputProps("password")} />
            <Button color="red" type="submit" loading={disable.isPending}>
              Disable authenticator
            </Button>
          </Stack>
        </form>
      </Modal>
      <Modal opened={recoveryOpen} onClose={() => setRecoveryOpen(false)} title="Regenerate recovery codes">
        <form onSubmit={reauth.onSubmit((v) => regenerate.mutate({ password: v.password, code: v.code }))}>
          <Stack>
            <PasswordInput label="Current password" {...reauth.getInputProps("password")} />
            <TextInput label="Authenticator code" {...reauth.getInputProps("code")} />
            <Button type="submit" loading={regenerate.isPending}>
              Regenerate codes
            </Button>
          </Stack>
        </form>
      </Modal>
      <Modal opened={removeId !== null} onClose={() => setRemoveId(null)} title="Remove passkey">
        <form
          onSubmit={reauth.onSubmit((v) => {
            if (removeId) remove.mutate({ id: removeId, password: v.password });
          })}
        >
          <Stack>
            <Text>This cannot be undone. Confirm with your current password.</Text>
            <PasswordInput label="Current password" {...reauth.getInputProps("password")} />
            <Button color="red" type="submit" loading={remove.isPending}>
              Remove passkey
            </Button>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
}
const SettingsPage = () => (
  <Stack maw={920} mx="auto" gap="xl">
    <div>
      <Title order={2}>Settings</Title>
      <Text c="dimmed" mt={4}>
        Manage your personal details and sign-in methods.
      </Text>
    </div>
    <Tabs defaultValue="profile" keepMounted={false}>
      <Tabs.List aria-label="Personal settings">
        <Tabs.Tab value="profile" leftSection={<IconUser size={16} />}>
          Profile
        </Tabs.Tab>
        <Tabs.Tab value="security" leftSection={<IconShieldLock size={16} />}>
          Security
        </Tabs.Tab>
      </Tabs.List>
      <Tabs.Panel value="profile" pt="xl">
        <ProfileTab />
      </Tabs.Panel>
      <Tabs.Panel value="security" pt="xl">
        <SecurityTab />
      </Tabs.Panel>
    </Tabs>
  </Stack>
);
export const Route = createFileRoute("/settings")({
  beforeLoad: async ({ context }) => {
    await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
  },
  component: SettingsPage,
});
