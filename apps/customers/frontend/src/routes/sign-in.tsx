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
import { useEffect, useState } from "react";
import { completeTwoFactor, fetchOidcProvider } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey, signIn } from "../api/auth";

const SignInPage = () => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [mfa, setMfa] = useState(false);
  const [code, setCode] = useState("");
  const [oidc, setOidc] = useState<string | null>(null);
  useEffect(() => {
    fetchOidcProvider()
      .then((provider) => setOidc(provider.oidc?.displayName ?? null))
      .catch(() => {});
  }, []);
  const form = useForm({ initialValues: { email: "", password: "" } });
  const login = useMutation({
    mutationFn: (values: typeof form.values) => signIn(values.email, values.password),
    onSuccess: (data) => {
      if ((data as { requiresTwoFactor?: boolean }).requiresTwoFactor) setMfa(true);
      else {
        queryClient.setQueryData(sessionQueryKey, data);
        if (data.mfaEnrollmentRequired) window.location.assign("/settings");
        else void navigate({ to: "/" });
      }
    },
  });
  const verify = useMutation({
    mutationFn: () => completeTwoFactor(code),
    onSuccess: (data) => {
      queryClient.setQueryData(sessionQueryKey, data);
      if (data.mfaEnrollmentRequired) window.location.assign("/settings");
      else void navigate({ to: "/" });
    },
  });
  return (
    <Center mih="100vh" bg="gray.0" p="md">
      <Card withBorder shadow="sm" p="xl" maw={440} w="100%">
        <Stack>
          <Title order={2}>{mfa ? "Verify your sign-in" : "Welcome back"}</Title>
          <Text c="dimmed">
            {mfa ? "Enter your authenticator or recovery code." : "Sign in to continue to Customers."}
          </Text>
          {(login.error || verify.error) && (
            <Alert icon={<IconAlertCircle size={18} />} color="red">
              {(login.error || verify.error)?.message}
            </Alert>
          )}
          {mfa ? (
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
                <Anchor component="a" href="/forgot-password" size="sm">
                  Forgot your password?
                </Anchor>
                {oidc && (
                  <>
                    <Divider label="or" />
                    <Button variant="default" component="a" href="/auth/oidc/challenge">
                      Continue with {oidc}
                    </Button>
                  </>
                )}
              </Stack>
            </form>
          )}
        </Stack>
      </Card>
    </Center>
  );
};
export const Route = createFileRoute("/sign-in")({
  beforeLoad: async () => {
    const session = await fetchSession();
    if (session) throw redirect({ to: "/" });
  },
  component: SignInPage,
});
