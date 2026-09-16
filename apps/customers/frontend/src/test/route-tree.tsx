/* eslint-disable react-refresh/only-export-components -- test-only route tree, fast refresh does not apply */
import { Alert, Button, Card, Center, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import type { QueryClient } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, createRoute, Link, Outlet, useNavigate, useParams } from "@tanstack/react-router";
import { AppShellLayout, SpotlightSearchBox, useI18n } from "@vantigo/frontend-shell";
import type { ReactElement } from "react";
import { bootstrapAccount, createInvitation, fetchBootstrapStatus, mfaStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { AppSpotlight } from "../components/app-spotlight";
import { showLifecycleFormError } from "../lib/lifecycle-form-errors";
import { ContactDetailsPage } from "../pages/contacts.$contactId";
import { ContactsPage } from "../pages/contacts.index";
import { CustomerDetailHeader, CustomerOverview } from "../pages/customers.$customerId";
import { CustomersPage } from "../pages/customers.index";
import "../i18n";

const Dashboard = () => {
  const { t } = useI18n("customers");
  return <Title order={2}>{t("dashboard")}</Title>;
};

const CustomerDetailsTestPage = () => {
  const { customerId } = useParams({ strict: false }) as { customerId: number };
  return (
    <>
      <CustomerDetailHeader customerId={customerId} />
      <CustomerOverview customerId={customerId} />
    </>
  );
};

const Setup = () => {
  const { t } = useI18n("customers");
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const status = useQuery({ queryKey: ["auth", "bootstrap-status"], queryFn: fetchBootstrapStatus, retry: false });
  const form = useForm({ initialValues: { secret: "", email: "", displayName: "", password: "" } });
  const mutation = useMutation({
    mutationFn: bootstrapAccount,
    onSuccess: async () => {
      const session = await queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 0 });
      queryClient.setQueryData(sessionQueryKey, session);
      void navigate({ to: "/" });
    },
    onError: (error) =>
      showLifecycleFormError(error, form, t("setupCouldNotBeCompleted"), {
        accountExists: t("accountAlreadyExists"),
        request: t("requestCouldNotComplete"),
      }),
  });
  if (status.isPending)
    return (
      <Center mih="100vh">
        <Text>{t("checkingSetupAvailability")}</Text>
      </Center>
    );
  return (
    <Center mih="100vh">
      <Card withBorder p="xl" maw={460} w="100%">
        <Stack>
          <Title order={2}>{t("setupVantigo")}</Title>
          <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
            <Stack>
              <TextInput label={t("setupSecret")} {...form.getInputProps("secret")} />
              <TextInput label={t("displayName")} {...form.getInputProps("displayName")} />
              <TextInput label={t("email")} {...form.getInputProps("email")} />
              <PasswordInput label={t("password")} {...form.getInputProps("password")} />
              <Button type="submit" loading={mutation.isPending}>
                {t("createOwnerAccount")}
              </Button>
            </Stack>
          </form>
          {mutation.error && <Alert color="red">{mutation.error.message}</Alert>}
        </Stack>
      </Card>
    </Center>
  );
};

const Settings = () => {
  const { t } = useI18n("customers");
  const mfa = useQuery({ queryKey: ["mfa"], queryFn: mfaStatus });
  const form = useForm({ initialValues: { email: "", displayName: "", role: "User" as "User" | "Owner" } });
  const mutation = useMutation({
    mutationFn: createInvitation,
    onSuccess: () => form.reset(),
    onError: (error) =>
      showLifecycleFormError(error, form, t("invitationCouldNotBeSent"), {
        accountExists: t("accountAlreadyExists"),
        request: t("requestCouldNotComplete"),
      }),
  });
  return (
    <Stack>
      <Title order={2}>{t("accountSettings")}</Title>
      <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
        <Stack>
          <TextInput label={t("email")} {...form.getInputProps("email")} />
          <TextInput label={t("displayName")} {...form.getInputProps("displayName")} />
          <Button type="submit" loading={mutation.isPending}>
            {t("sendInvite")}
          </Button>
        </Stack>
      </form>
      <Text>{mfa.data?.twoFactorEnabled ? t("automatic") : t("notEnabled")}</Text>
    </Stack>
  );
};

const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: () => (
    <AppShellLayout headerCenter={<SpotlightSearchBox />} nav={() => null}>
      <Outlet />
      <AppSpotlight />
    </AppShellLayout>
  ),
});

const route = (path: string, component: () => ReactElement, errorComponent?: () => ReactElement) =>
  createRoute({ getParentRoute: () => rootRoute, path, component, errorComponent });

export const routeTree = rootRoute.addChildren([
  route("/", Dashboard),
  route("/customers", CustomersPage),
  route("/customers/$customerId", CustomerDetailsTestPage, () => (
    <Stack align="center">
      <Title order={3}>Customer not found</Title>
      <Button component={Link} to="/customers">
        Back to customers
      </Button>
    </Stack>
  )),
  route("/customers/contacts", ContactsPage),
  route("/customers/contacts/$contactId", ContactDetailsPage, () => (
    <Stack align="center">
      <Title order={3}>Contact not found</Title>
      <Button component={Link} to="/customers/contacts">
        Back to contacts
      </Button>
    </Stack>
  )),
  route("/setup", Setup),
  route("/settings", Settings),
]);
