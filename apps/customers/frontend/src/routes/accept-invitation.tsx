import { Alert, Button, Card, Center, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { acceptInvitation, validateInvitation } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey } from "../api/auth";

export const AcceptInvitationPage = ({ token }: { token: string }) => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const invitation = useQuery({
    queryKey: ["invitation", token],
    queryFn: () => validateInvitation(token),
    enabled: Boolean(token),
  });
  const form = useForm({ initialValues: { displayName: "", password: "" } });
  const mutation = useMutation({
    mutationFn: () => acceptInvitation({ token, ...form.values }),
    onSuccess: async () => {
      const session = await queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 0 });
      queryClient.setQueryData(sessionQueryKey, session);
      if (session?.mfaEnrollmentRequired) window.location.assign("/settings");
      else void navigate({ to: "/" });
    },
  });
  return (
    <Center mih="100vh" bg="gray.0">
      <Card withBorder p="xl" maw={440} w="100%">
        <Stack>
          <Title order={2}>Join Vantigo</Title>
          {!invitation.isPending && !invitation.data?.valid && (
            <Alert color="red">This invitation is invalid or expired.</Alert>
          )}
          {invitation.data?.valid && (
            <>
              <Text>Invitation for {invitation.data.email}</Text>
              <form onSubmit={form.onSubmit(() => mutation.mutate())}>
                <Stack>
                  <TextInput label="Display name" {...form.getInputProps("displayName")} />
                  <PasswordInput label="Password" required {...form.getInputProps("password")} />
                  <Button type="submit" loading={mutation.isPending}>
                    Create account
                  </Button>
                </Stack>
              </form>
            </>
          )}
          {mutation.error && <Alert color="red">This invitation is invalid or no longer available.</Alert>}
        </Stack>
      </Card>
    </Center>
  );
};
const AcceptInvitationRoute = () => <AcceptInvitationPage token={Route.useSearch().token} />;
export const Route = createFileRoute("/accept-invitation")({
  validateSearch: (search: Record<string, unknown>) => ({ token: String(search.token ?? "") }),
  component: AcceptInvitationRoute,
});
