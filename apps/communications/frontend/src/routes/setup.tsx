import { Alert, Button, Card, Center, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { bootstrapAccount, fetchBootstrapStatus, sessionQueryKey } from "../api/auth";
export function SetupPage() {
  const navigate = useNavigate();
  const client = useQueryClient();
  const status = useQuery({ queryKey: ["auth", "bootstrap-status"], queryFn: fetchBootstrapStatus, retry: false });
  const mutation = useMutation({
    mutationFn: (v: { secret: string; email: string; displayName: string; password: string }) => bootstrapAccount(v),
    onSuccess: (s) => {
      client.setQueryData(sessionQueryKey, s);
      void navigate({ to: "/messages", search: { page: 1 } });
    },
  });
  if (status.isPending)
    return (
      <Center mih="100vh">
        <Text c="dimmed">Checking setup availability…</Text>
      </Center>
    );
  if (!status.data?.available)
    return (
      <Center mih="100vh">
        <Card withBorder p="xl">
          <Stack>
            <Title order={2}>Setup unavailable</Title>
            <Text c="dimmed">This workspace has already been set up.</Text>
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
          <Title order={2}>Set up Communications</Title>
          <Text c="dimmed">Create the first Owner account.</Text>
          {mutation.error && <Alert color="red">{mutation.error.message}</Alert>}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const f = new FormData(e.currentTarget);
              mutation.mutate({
                secret: String(f.get("secret")),
                email: String(f.get("email")),
                displayName: String(f.get("displayName")),
                password: String(f.get("password")),
              });
            }}
          >
            <Stack>
              <PasswordInput name="secret" label="Setup secret" autoComplete="off" required />
              <TextInput name="displayName" label="Display name" autoComplete="name" required />
              <TextInput name="email" label="Email" type="email" autoComplete="email" required />
              <PasswordInput name="password" label="Password" autoComplete="new-password" required />
              <Button type="submit" loading={mutation.isPending}>
                Create Owner account
              </Button>
            </Stack>
          </form>
          <Button component={Link} to="/sign-in" variant="subtle">
            Already have an account? Sign in
          </Button>
        </Stack>
      </Card>
    </Center>
  );
}
export const Route = createFileRoute("/setup")({ component: SetupPage });
