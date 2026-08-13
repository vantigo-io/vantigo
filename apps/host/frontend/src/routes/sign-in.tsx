import {
  Alert,
  Anchor,
  Button,
  Card,
  Center,
  Divider,
  PasswordInput,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router";
import { appConfig, appUrl, SupportContactLine } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import { loginWithPasskey } from "../api/account";
import { completeTwoFactor } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey, signIn } from "../api/auth";
import {
  type FederationProviderDiscovery,
  federationChallengeUrl,
  fetchPublicSignInProviders,
} from "../api/federation";

const SignInPage = () => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const search = Route.useSearch();
  const federationMfa = search.mfa === "federation" || search.mfa === "required";
  const federationFailed = search.error === "federation_sign_in_failed";
  const [mfa, setMfa] = useState(false);
  const showingMfa = mfa || federationMfa;
  const [code, setCode] = useState("");
  const [providers, setProviders] = useState<FederationProviderDiscovery[]>([]);
  const [providerLoading, setProviderLoading] = useState(true);
  const [providerError, setProviderError] = useState<string | null>(null);
  useEffect(() => {
    fetchPublicSignInProviders()
      .then(setProviders)
      .catch((error: unknown) =>
        setProviderError(error instanceof Error ? error.message : "Sign-in providers are unavailable."),
      )
      .finally(() => setProviderLoading(false));
  }, []);
  const form = useForm({ initialValues: { email: "", password: "" } });
  const login = useMutation({
    mutationFn: (values: typeof form.values) => signIn(values.email, values.password),
    onSuccess: (data) => {
      if ((data as { requiresTwoFactor?: boolean }).requiresTwoFactor) setMfa(true);
      else {
        queryClient.setQueryData(sessionQueryKey, data);
        if (data.mfaEnrollmentRequired) window.location.assign(appUrl("/settings"));
        else void navigate({ to: "/", search: {}, replace: true });
      }
    },
  });
  const passkeyLogin = useMutation({
    mutationFn: () => loginWithPasskey(form.values.email.trim()),
    onSuccess: async (data) => {
      queryClient.setQueryData(sessionQueryKey, data);
      await queryClient.refetchQueries({ queryKey: sessionQueryKey });
      if (data.mfaEnrollmentRequired) window.location.assign(appUrl("/settings"));
      else void navigate({ to: "/", search: {}, replace: true });
    },
  });
  const verify = useMutation({
    mutationFn: () => completeTwoFactor(code),
    onSuccess: (data) => {
      queryClient.setQueryData(sessionQueryKey, data);
      if (data.mfaEnrollmentRequired) window.location.assign(appUrl("/settings"));
      else void navigate({ to: "/", search: {}, replace: true });
    },
  });
  return (
    <Center mih="100vh" bg="gray.0" p="md">
      <Stack maw={440} w="100%" align="center">
        <Card withBorder shadow="sm" p="xl" w="100%">
          <Stack>
            <Title order={2}>{showingMfa ? "Verify your sign-in" : "Welcome back"}</Title>
            <Text c="dimmed">
              {showingMfa
                ? "Enter your authenticator or recovery code."
                : `Sign in to continue to ${appConfig().title}.`}
            </Text>
            {(login.error || verify.error || passkeyLogin.error || federationFailed) && (
              <Alert icon={<IconAlertCircle size={18} />} color="red">
                {federationFailed
                  ? "Your identity provider could not complete sign-in. Try again or use another sign-in method."
                  : (login.error || verify.error || passkeyLogin.error)?.message}
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
                    label="Security code"
                    value={code}
                    onChange={(event) => setCode(event.currentTarget.value)}
                  />
                  <Button type="submit" loading={verify.isPending}>
                    Verify
                  </Button>
                  <Button
                    type="button"
                    variant="subtle"
                    onClick={() => {
                      setMfa(false);
                      setCode("");
                      void navigate({ to: "/sign-in", search: { mfa: undefined, error: undefined }, replace: true });
                    }}
                  >
                    Use another sign-in method
                  </Button>
                </Stack>
              </form>
            ) : (
              <form onSubmit={form.onSubmit((values) => login.mutate(values))}>
                <Stack>
                  <TextInput label="Email" autoComplete="email" {...form.getInputProps("email")} />
                  <PasswordInput label="Password" autoComplete="current-password" {...form.getInputProps("password")} />
                  <Button type="submit" fullWidth loading={login.isPending}>
                    Sign in
                  </Button>
                  <Button
                    type="button"
                    variant="default"
                    fullWidth
                    loading={passkeyLogin.isPending}
                    disabled={!form.values.email.trim()}
                    onClick={() => passkeyLogin.mutate()}
                  >
                    Sign in with a passkey
                  </Button>
                  <Anchor component="a" href={appUrl("/forgot-password")} size="sm">
                    Forgot your password?
                  </Anchor>
                  {providerLoading && (
                    <Text size="sm" c="dimmed" ta="center">
                      Checking available sign-in providers…
                    </Text>
                  )}
                  {providers.length > 0 && <Divider label="or" />}
                  {providers.map((provider) => (
                    <Button
                      key={provider.id}
                      variant="default"
                      component="a"
                      href={federationChallengeUrl(provider.id)}
                    >
                      Continue with {provider.displayName}
                    </Button>
                  ))}
                  {providerError && (
                    <Text size="sm" c="dimmed" ta="center">
                      {providerError}
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
    // Only these server-issued markers may enter the MFA continuation UI.
    mfa: search.mfa === "federation" || search.mfa === "required" ? search.mfa : undefined,
    error: search.error === "federation_sign_in_failed" ? search.error : undefined,
  }),
  beforeLoad: async () => {
    const session = await fetchSession();
    if (session) throw redirect({ to: "/" });
  },
  component: SignInPage,
});
