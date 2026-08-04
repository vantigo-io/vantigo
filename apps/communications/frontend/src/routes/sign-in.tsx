import { Alert, Button, Card, Center, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey, signIn } from "../api/auth";
export function SignInPage() {
  const navigate = useNavigate();
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: (v: { email: string; password: string }) => signIn(v.email, v.password),
    onSuccess: (session) => {
      client.setQueryData(sessionQueryKey, session);
      void navigate({ to: "/messages", search: { page: 1 } });
    },
  });
  return (
    <Center mih="100vh" bg="gray.0" p="md">
      <Card withBorder shadow="sm" p="xl" maw={440} w="100%">
        <Stack>
          <Title order={2}>Welcome back</Title>
          <Text c="dimmed">Sign in to manage your communications.</Text>
          {mutation.error && <Alert color="red">{mutation.error.message}</Alert>}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const form = new FormData(e.currentTarget);
              mutation.mutate({ email: String(form.get("email")), password: String(form.get("password")) });
            }}
          >
            <Stack>
              <TextInput name="email" label="Email" type="email" autoComplete="email" required />
              <PasswordInput name="password" label="Password" autoComplete="current-password" required />
              <Button type="submit" loading={mutation.isPending} fullWidth>
                Sign in
              </Button>
            </Stack>
          </form>
        </Stack>
      </Card>
    </Center>
  );
}
export const Route = createFileRoute("/sign-in")({
  beforeLoad: async () => {
    if (await fetchSession()) throw redirect({ to: "/messages", search: { page: 1 } });
  },
  component: SignInPage,
});
