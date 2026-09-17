import { Alert, Anchor, Button, Card, Center, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router";
import { appConfig, appUrl, SupportContactLine, useI18n } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import { isPasskeyClientError, loginWithPasskey } from "../api/account";
import { completeTwoFactor, fetchOidcProvider } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey, signIn } from "../api/auth";
import { mfaEnrolmentDestination } from "../lib/mfa-enrolment-gate";
import "../i18n";

const SignInPage = () => {
  const { t } = useI18n("host");
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const search = Route.useSearch();
  const [mfa, setMfa] = useState(false);
  const showingMfa = mfa;
  const [code, setCode] = useState("");
  const [oidcProvider, setOidcProvider] = useState<{ displayName: string } | null>(null);
  const [oidcProviderLoading, setOidcProviderLoading] = useState(true);
  const [oidcProviderError, setOidcProviderError] = useState<string | null>(null);
  useEffect(() => {
    fetchOidcProvider()
      .then((result) => setOidcProvider(result.oidc))
      .catch((error: unknown) =>
        setOidcProviderError(error instanceof Error ? error.message : t("auth.oidcUnavailable")),
      )
      .finally(() => setOidcProviderLoading(false));
  }, [t]);
  const form = useForm({ initialValues: { email: "", password: "" } });
  const errorMessage = (error: unknown) => {
    if (isPasskeyClientError(error)) {
      if (error.code === "unsupported") return t("auth.passkeyUnsupported");
      if (error.code === "cancelled") return t("auth.passkeySignInCancelled");
      return t("auth.passkeySignInFailed");
    }
    return error instanceof Error ? error.message : t("common.requestFailed");
  };
  const login = useMutation({
    mutationFn: (values: typeof form.values) => signIn(values.email, values.password),
    onSuccess: (data) => {
      if ((data as { requiresTwoFactor?: boolean }).requiresTwoFactor) setMfa(true);
      else {
        queryClient.setQueryData(sessionQueryKey, data);
        if (data.mfaEnrollmentRequired) window.location.assign(appUrl(mfaEnrolmentDestination));
        else void navigate({ to: "/", search: {}, replace: true });
      }
    },
  });
  const passkeyLogin = useMutation({
    mutationFn: () => loginWithPasskey(form.values.email.trim()),
    onSuccess: async (data) => {
      queryClient.setQueryData(sessionQueryKey, data);
      await queryClient.refetchQueries({ queryKey: sessionQueryKey });
      if (data.mfaEnrollmentRequired) window.location.assign(appUrl(mfaEnrolmentDestination));
      else void navigate({ to: "/", search: {}, replace: true });
    },
  });
  const verify = useMutation({
    mutationFn: () => completeTwoFactor(code),
    onSuccess: (data) => {
      queryClient.setQueryData(sessionQueryKey, data);
      if (data.mfaEnrollmentRequired) window.location.assign(appUrl(mfaEnrolmentDestination));
      else void navigate({ to: "/", search: {}, replace: true });
    },
  });
  return (
    <Center mih="100vh" bg="gray.0" p="md">
      <Stack maw={440} w="100%" align="center">
        <Card withBorder shadow="sm" p="xl" w="100%">
          <Stack>
            <Title order={2}>{showingMfa ? t("auth.verifySignIn") : t("auth.welcomeBack")}</Title>
            <Text c="dimmed">
              {showingMfa ? t("auth.authenticatorRecoveryCode") : t("auth.signInContinue", { app: appConfig().title })}
            </Text>
            {(login.error || verify.error || passkeyLogin.error || search.error) && (
              <Alert icon={<IconAlertCircle size={18} />} color="red">
                {search.error
                  ? t("auth.identityProviderError")
                  : errorMessage(login.error || verify.error || passkeyLogin.error)}
              </Alert>
            )}
            {showingMfa ? (
              <form
                onSubmit={(event) => {
                  event.preventDefault();
                  verify.mutate();
                }}
              >
                <Stack>
                  <TextInput
                    label={t("auth.securityCode")}
                    value={code}
                    onChange={(event) => setCode(event.currentTarget.value)}
                  />
                  <Button type="submit" loading={verify.isPending}>
                    {t("auth.verify")}
                  </Button>
                  <Button
                    type="button"
                    variant="subtle"
                    onClick={() => {
                      setMfa(false);
                      setCode("");
                      void navigate({ to: "/sign-in", search: { error: undefined }, replace: true });
                    }}
                  >
                    {t("auth.useAnotherMethod")}
                  </Button>
                </Stack>
              </form>
            ) : (
              <form onSubmit={form.onSubmit((values) => login.mutate(values))}>
                <Stack>
                  <TextInput label={t("common.email")} autoComplete="email" {...form.getInputProps("email")} />
                  <PasswordInput
                    label={t("common.password")}
                    autoComplete="current-password"
                    {...form.getInputProps("password")}
                  />
                  <Button type="submit" fullWidth loading={login.isPending}>
                    {t("auth.signIn")}
                  </Button>
                  <Button
                    type="button"
                    variant="default"
                    fullWidth
                    loading={passkeyLogin.isPending}
                    disabled={!form.values.email.trim()}
                    onClick={() => passkeyLogin.mutate()}
                  >
                    {t("auth.signInWithPasskey")}
                  </Button>
                  <Anchor component="a" href={appUrl("/forgot-password")} size="sm">
                    {t("auth.forgotPassword")}
                  </Anchor>
                  {oidcProviderLoading && (
                    <Text size="sm" c="dimmed" ta="center">
                      {t("auth.checkingMethods")}
                    </Text>
                  )}
                  {oidcProvider && (
                    <Button variant="default" component="a" href={appUrl("/api/v1/identity/oidc/challenge")}>
                      {t("auth.continueWith", { provider: oidcProvider.displayName })}
                    </Button>
                  )}
                  {oidcProviderError && (
                    <Text size="sm" c="dimmed" ta="center">
                      {oidcProviderError}
                    </Text>
                  )}
                </Stack>
              </form>
            )}
          </Stack>
        </Card>
        <SupportContactLine />
      </Stack>
    </Center>
  );
};
export const Route = createFileRoute("/sign-in")({
  validateSearch: (search: Record<string, unknown>) => ({
    // Only server-issued OIDC errors are accepted by the sign-in route.
    error:
      typeof search.error === "string" &&
      [
        "account_locked",
        "local_mfa_required",
        "oidc_authentication_failed",
        "oidc_email_conflict",
        "oidc_external_identity_missing",
        "oidc_identity_invalid",
        "oidc_local_sign_in_failed",
        "oidc_remote_failure",
        "oidc_sign_in_unavailable",
      ].includes(search.error)
        ? search.error
        : undefined,
  }),
  beforeLoad: async () => {
    const session = await fetchSession();
    if (session) throw redirect({ to: "/" });
  },
  component: SignInPage,
});
