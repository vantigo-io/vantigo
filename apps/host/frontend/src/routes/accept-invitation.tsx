import { Alert, Button, Card, Center, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { appConfig, appUrl, useI18n } from "@vantigo/frontend-shell";
import { acceptInvitation, validateInvitation } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey } from "../api/auth";
import "../i18n";

export const AcceptInvitationPage = ({ token }: { token: string }) => {
  const { t } = useI18n("host");
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
      if (session?.mfaEnrollmentRequired) window.location.assign(appUrl("/settings"));
      else void navigate({ to: "/" });
    },
  });
  return (
    <Center mih="100vh" bg="gray.0">
      <Card withBorder p="xl" maw={440} w="100%">
        <Stack>
          <Title order={2}>{t("auth.joinApp", { app: appConfig().title })}</Title>
          {!invitation.isPending && !invitation.data?.valid && <Alert color="red">{t("auth.invalidInvitation")}</Alert>}
          {invitation.data?.valid && (
            <>
              <Text>{t("auth.invitationFor", { email: invitation.data.email })}</Text>
              <form onSubmit={form.onSubmit(() => mutation.mutate())}>
                <Stack>
                  <TextInput label={t("common.displayName")} {...form.getInputProps("displayName")} />
                  <PasswordInput label={t("common.password")} required {...form.getInputProps("password")} />
                  <Button type="submit" loading={mutation.isPending}>
                    {t("auth.createAccount")}
                  </Button>
                </Stack>
              </form>
            </>
          )}
          {mutation.error && <Alert color="red">{t("auth.invitationUnavailable")}</Alert>}
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
