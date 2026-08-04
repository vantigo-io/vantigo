import { Alert, Anchor, Button, Card, Center, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { bootstrapAccount, fetchBootstrapStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { showLifecycleFormError } from "../lib/lifecycle-form-errors";

const SetupPage = () => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const status = useQuery({ queryKey: ["auth", "bootstrap-status"], queryFn: fetchBootstrapStatus, retry: false });
  const form = useForm({
    initialValues: { secret: "", email: "", displayName: "", password: "" },
    validate: {
      secret: (value) => (value ? null : "Enter the setup secret"),
      email: (value) => (/^\S+@\S+$/.test(value) ? null : "Enter a valid email address"),
      displayName: (value) => (value.trim() ? null : "Enter your name"),
      password: (value) => (value ? null : "Enter a password"),
    },
  });
  const mutation = useMutation({
    mutationFn: bootstrapAccount,
    onSuccess: async () => {
      const session = await queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 0 });
      queryClient.setQueryData(sessionQueryKey, session);
      if (session?.mfaEnrollmentRequired) window.location.assign("/settings");
      else void navigate({ to: "/" });
    },
    onError: (error) => showLifecycleFormError(error, form, "Setup could not be completed"),
  });
  if (status.isPending)
    return (
      <Center mih="100vh">
        <Text c="dimmed">Checking setup availability…</Text>
      </Center>
    );
  if (!status.data?.available)
    return (
      <Center mih="100vh" bg="gray.0">
        <Card withBorder p="xl" maw={440} w="100%">
          <Stack>
            <Title order={2}>Setup unavailable</Title>
            <Text c="dimmed">This Vantigo installation has already been set up, or setup is not enabled.</Text>
            <Button component={Link} to="/sign-in">
              Go to sign in
            </Button>
          </Stack>
        </Card>
      </Center>
    );
  return (
    <Center mih="100vh" bg="gray.0" p="md">
      <Card withBorder shadow="sm" p="xl" maw={460} w="100%">
        <Stack>
          <Title order={2}>Set up Vantigo</Title>
          <Text c="dimmed">Create the first Owner account for this installation.</Text>
          {mutation.error && <Alert color="red">{mutation.error.message}</Alert>}
          <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
            <Stack>
              <TextInput label="Setup secret" type="password" autoComplete="off" {...form.getInputProps("secret")} />
              <TextInput label="Display name" autoComplete="name" {...form.getInputProps("displayName")} />
              <TextInput label="Email" autoComplete="email" {...form.getInputProps("email")} />
              <PasswordInput label="Password" autoComplete="new-password" {...form.getInputProps("password")} />
              <Button type="submit" loading={mutation.isPending}>
                Create Owner account
              </Button>
            </Stack>
          </form>
          <Anchor component={Link} to="/sign-in" size="sm">
            Already have an account? Sign in
          </Anchor>
        </Stack>
      </Card>
    </Center>
  );
};
export const Route = createFileRoute("/setup")({ component: SetupPage });
