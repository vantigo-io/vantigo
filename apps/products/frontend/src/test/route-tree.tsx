/* eslint-disable react-refresh/only-export-components -- test-only route tree, fast refresh does not apply */
import { Alert, Button, Card, Center, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import type { QueryClient } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, createRoute, Outlet, useNavigate } from "@tanstack/react-router";
import { AppShellLayout, SpotlightSearchBox, useI18n } from "@vantigo/frontend-shell";
import type { ReactElement } from "react";
import { bootstrapAccount, createInvitation, fetchBootstrapStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { AppSpotlight } from "../components/app-spotlight";
import { showLifecycleFormError } from "../lib/lifecycle-form-errors";
import { CategoriesPage } from "../pages/categories";
import { ProductDetailsPage } from "../pages/products.$productId";
import { ProductsPage } from "../pages/products.index";

const Setup = () => {
  const { t } = useI18n("products");
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
    onError: (error) => showLifecycleFormError(error, form, t("lifecycle.setupError")),
  });
  if (status.isPending)
    return (
      <Center mih="100vh">
        <Text>{t("lifecycle.checkingSetup")}</Text>
      </Center>
    );
  return (
    <Center mih="100vh">
      <Card withBorder p="xl" maw={460} w="100%">
        <Stack>
          <Title order={2}>{t("lifecycle.setupTitle")}</Title>
          <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
            <Stack>
              <TextInput label={t("lifecycle.setupSecret")} {...form.getInputProps("secret")} />
              <TextInput label={t("lifecycle.displayName")} {...form.getInputProps("displayName")} />
              <TextInput label={t("lifecycle.email")} {...form.getInputProps("email")} />
              <PasswordInput label={t("lifecycle.password")} {...form.getInputProps("password")} />
              <Button type="submit" loading={mutation.isPending}>
                {t("lifecycle.createOwner")}
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
  const { t } = useI18n("products");
  const form = useForm({ initialValues: { email: "", displayName: "", role: "User" as "User" | "Owner" } });
  const mutation = useMutation({
    mutationFn: createInvitation,
    onSuccess: () => form.reset(),
    onError: (error) => showLifecycleFormError(error, form, t("lifecycle.invitationError")),
  });
  return (
    <Stack>
      <Title order={2}>{t("lifecycle.accountSettings")}</Title>
      <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
        <Stack>
          <TextInput label={t("lifecycle.email")} {...form.getInputProps("email")} />
          <TextInput label={t("lifecycle.displayName")} {...form.getInputProps("displayName")} />
          <Button type="submit" loading={mutation.isPending}>
            {t("lifecycle.sendInvite")}
          </Button>
        </Stack>
      </form>
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

const route = (path: string, component: () => ReactElement) =>
  createRoute({ getParentRoute: () => rootRoute, path, component });

export const routeTree = rootRoute.addChildren([
  route("/", () => {
    const { t } = useI18n("products");
    return <Title order={2}>{t("lifecycle.dashboard")}</Title>;
  }),
  route("/products", ProductsPage),
  route("/products/$productId", ProductDetailsPage),
  route("/products/categories", CategoriesPage),
  route("/setup", Setup),
  route("/settings", Settings),
]);
