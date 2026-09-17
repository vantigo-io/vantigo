import { Alert, Badge, Button, Card, Group, Modal, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconKey, IconLock, IconShieldLock } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { PageHeader, useTranslation } from "@vantigo/frontend-shell";
import { useState } from "react";
import "../../i18n";
import {
  changePassword,
  disableMfa,
  enableMfa,
  enrollPasskey,
  getMfaStatus,
  initializeMfa,
  isPasskeyClientError,
  listPasskeys,
  regenerateRecoveryCodes,
  removePasskey,
} from "../../api/account";
import { sessionQueryKey } from "../../api/auth";
import { MfaEnrolmentNotice } from "../../components/mfa-enrolment-notice";
import { isOidcError, message, mfaKey, notify, passkeyKey } from "./-helpers";

function SecurityForm() {
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
      // Enabling marks this session MFA-verified, so the session's
      // enrolment flag clears and the root layout's gate lifts on refetch.
      void qc.invalidateQueries({ queryKey: sessionQueryKey });
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
      {mfa.data?.mfaEnrollmentRequired && <MfaEnrolmentNotice />}
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

const SecurityPage = () => {
  const { t } = useTranslation("settings");
  const { t: hostT } = useTranslation("host");
  return (
    <Stack maw={1180} mx="auto" gap="xl">
      <PageHeader eyebrow={hostT("navigation.settings")} title={t("security")} description={t("securityDescription")} />
      <SecurityForm />
    </Stack>
  );
};

export const Route = createFileRoute("/settings/security")({ component: SecurityPage });
