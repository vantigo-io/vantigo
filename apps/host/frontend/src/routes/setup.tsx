import { Alert, Anchor, Button, Card, Center, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { appConfig, appUrl, SupportContactLine, useI18n } from "@vantigo/frontend-shell";
import { bootstrapAccount, fetchBootstrapStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { showLifecycleFormError } from "../lib/lifecycle-form-errors";
import "../i18n";

const SetupPage = () => {
  const { t } = useI18n("host");
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const status = useQuery({ queryKey: ["auth", "bootstrap-status"], queryFn: fetchBootstrapStatus, retry: false });
  const form = useForm({
    initialValues: { email: "", displayName: "", password: "" },
    validate: {
      email: (value) => (/^\S+@\S+$/.test(value) ? null : t("auth.validEmail")),
      displayName: (value) => (value.trim() ? null : t("auth.enterName")),
      password: (value) => (value ? null : t("auth.enterPassword")),
    },
  });
  const mutation = useMutation({
    mutationFn: bootstrapAccount,
    onSuccess: async () => {
      const session = await queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 0 });
      queryClient.setQueryData(sessionQueryKey, session);
      if (session?.mfaEnrollmentRequired) window.location.assign(appUrl("/settings"));
      else void navigate({ to: "/" });
    },
    onError: (error) => showLifecycleFormError(error, form, t("auth.setupFailed")),
  });
  if (status.isPending)
    return (
      <Center mih="100vh">
        <Text c="dimmed">{t("auth.checkingSetup")}</Text>
      </Center>
    );
  if (!status.data?.available)
    return (
      <Center mih="100vh" bg="gray.0">
        <Card withBorder p="xl" maw={440} w="100%">
          <Stack>
            <Title order={2}>{t("auth.setupUnavailable")}</Title>
            <Text c="dimmed">{t("auth.setupUnavailableDescription", { app: appConfig().title })}</Text>
            <Button component={Link} to="/sign-in">
              {t("auth.goToSignIn")}
            </Button>
          </Stack>
        </Card>
      </Center>
    );
  return (
    <Center mih="100vh" bg="gray.0" p="md">
      <Stack maw={460} w="100%" align="center">
        <Card withBorder shadow="sm" p="xl" w="100%">
          <Stack>
            <Title order={2}>{t("auth.setupTitle", { app: appConfig().title })}</Title>
            <Text c="dimmed">{t("auth.setupDescription")}</Text>
            {mutation.error && <Alert color="red">{mutation.error.message}</Alert>}
            <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
              <Stack>
                <TextInput label={t("common.displayName")} autoComplete="name" {...form.getInputProps("displayName")} />
                <TextInput label={t("common.email")} autoComplete="email" {...form.getInputProps("email")} />
                <PasswordInput
                  label={t("common.password")}
                  autoComplete="new-password"
                  {...form.getInputProps("password")}
                />
                <Button type="submit" loading={mutation.isPending}>
                  {t("auth.createOwner")}
                </Button>
              </Stack>
            </form>
            <Anchor component={Link} to="/sign-in" size="sm">
              {t("auth.alreadyAccount")}
            </Anchor>
          </Stack>
        </Card>
        <SupportContactLine />
      </Stack>
    </Center>
  );
};
export const Route = createFileRoute("/setup")({ component: SetupPage });
