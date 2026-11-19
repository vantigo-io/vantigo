import { Alert, Button, Card, Center, Stack, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMutation } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { AuthBrandMark, useDomainBrand, useI18n, vantigoTokens } from "@vantigo/frontend-shell";
import { requestPasswordRecovery } from "../api/account-lifecycle";
import "../i18n";

const ForgotPasswordPage = () => {
  const { t } = useI18n("host");
  const form = useForm({ initialValues: { email: "" } });
  const domainBrand = useDomainBrand(form.values.email);
  const mutation = useMutation({ mutationFn: () => requestPasswordRecovery(form.values.email) });
  return (
    <Center mih="100vh" bg={vantigoTokens.pageBg}>
      <Card withBorder p="xl" maw={440} w="100%">
        <Stack>
          <AuthBrandMark />
          <Title order={2}>{t("auth.resetTitle")}</Title>
          <Text c="dimmed">{t("auth.resetDescription")}</Text>
          {mutation.isSuccess && <Alert color="teal">{t("auth.checkInbox")}</Alert>}
          <form onSubmit={form.onSubmit(() => mutation.mutate())}>
            <Stack>
              <TextInput
                label={t("common.email")}
                required
                {...form.getInputProps("email")}
                onBlur={(event) => {
                  form.getInputProps("email").onBlur(event);
                  domainBrand.onBlur();
                }}
              />
              <Button type="submit" loading={mutation.isPending}>
                {t("auth.sendReset")}
              </Button>
            </Stack>
          </form>
        </Stack>
      </Card>
    </Center>
  );
};
export const Route = createFileRoute("/forgot-password")({ component: ForgotPasswordPage });
