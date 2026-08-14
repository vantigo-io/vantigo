import { Alert, Button, Card, Center, PasswordInput, Stack, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMutation } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { resetPassword } from "../api/account-lifecycle";
import { ApiValidationError } from "../api/request";
import "../i18n";
export const ResetPasswordPage = ({ email, token }: { email: string; token: string }) => {
  const { t } = useI18n("host");
  const form = useForm({ initialValues: { password: "" } });
  const mutation = useMutation({
    mutationFn: () => resetPassword({ email, token, newPassword: form.values.password }),
  });
  const validation = mutation.error instanceof ApiValidationError ? mutation.error : null;
  return (
    <Center mih="100vh" bg="gray.0">
      <Card withBorder p="xl" maw={440} w="100%">
        <Stack>
          <Title order={2}>{t("auth.chooseNewPassword")}</Title>
          {mutation.error &&
            (validation ? (
              <Alert color="red">
                {Object.values(validation.fieldErrors).map((message) => (
                  <div key={message}>{message}</div>
                ))}
              </Alert>
            ) : (
              <Alert color="red">{t("auth.invalidReset")}</Alert>
            ))}
          {mutation.isSuccess ? (
            <Alert color="teal">{t("auth.passwordReset")}</Alert>
          ) : (
            <form onSubmit={form.onSubmit(() => mutation.mutate())}>
              <Stack>
                <PasswordInput
                  label={t("auth.newPassword")}
                  required
                  error={validation?.fieldErrors.newPassword}
                  {...form.getInputProps("password")}
                />
                <Button type="submit" loading={mutation.isPending}>
                  {t("auth.resetPassword")}
                </Button>
              </Stack>
            </form>
          )}
        </Stack>
      </Card>
    </Center>
  );
};
const ResetPasswordRoute = () => {
  const search = Route.useSearch();
  return <ResetPasswordPage email={search.email} token={search.token} />;
};
export const Route = createFileRoute("/reset-password")({
  validateSearch: (search: Record<string, unknown>) => ({
    email: String(search.email ?? ""),
    token: String(search.token ?? ""),
  }),
  component: ResetPasswordRoute,
});
