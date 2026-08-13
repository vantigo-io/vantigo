/* eslint-disable react-refresh/only-export-components -- test-only route tree, fast refresh does not apply */
import { Alert, Button, Card, Center, PasswordInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import type { QueryClient } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, createRoute, Outlet, useNavigate } from "@tanstack/react-router";
import { AppShellLayout, SpotlightSearchBox } from "@vantigo/frontend-shell";
import type { ReactElement } from "react";
import { bootstrapAccount, createInvitation, fetchBootstrapStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { AppSpotlight } from "../components/app-spotlight";
import { showLifecycleFormError } from "../lib/lifecycle-form-errors";
import { CategoriesPage } from "../pages/categories";
import { ProductDetailsPage } from "../pages/products.$productId";
import { ProductsPage } from "../pages/products.index";

const Setup = () => {
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
    onError: (error) => showLifecycleFormError(error, form, "Setup could not be completed"),
  });
  if (status.isPending)
    return (
      <Center mih="100vh">
        <Text>Checking setup availability…</Text>
      </Center>
    );
  return (
    <Center mih="100vh">
      <Card withBorder p="xl" maw={460} w="100%">
        <Stack>
          <Title order={2}>Set up Vantigo</Title>
          <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
            <Stack>
              <TextInput label="Setup secret" {...form.getInputProps("secret")} />
              <TextInput label="Display name" {...form.getInputProps("displayName")} />
              <TextInput label="Email" {...form.getInputProps("email")} />
              <PasswordInput label="Password" {...form.getInputProps("password")} />
              <Button type="submit" loading={mutation.isPending}>
                Create Owner account
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
  const form = useForm({ initialValues: { email: "", displayName: "", role: "User" as "User" | "Owner" } });
  const mutation = useMutation({
    mutationFn: createInvitation,
    onSuccess: () => form.reset(),
    onError: (error) => showLifecycleFormError(error, form, "Invitation could not be sent"),
  });
  return (
    <Stack>
      <Title order={2}>Account settings</Title>
      <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
        <Stack>
          <TextInput label="Email" {...form.getInputProps("email")} />
          <TextInput label="Display name" {...form.getInputProps("displayName")} />
          <Button type="submit" loading={mutation.isPending}>
            Send invite
          </Button>
        </Stack>
      </form>
    </Stack>
  );
};

const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: () => (
    <AppShellLayout
      user={{ displayName: "Test User", email: "test@example.com" }}
      onSignOut={() => {}}
      navbarTop={<SpotlightSearchBox />}
      nav={() => null}
    >
      <Outlet />
      <AppSpotlight />
    </AppShellLayout>
  ),
});

const route = (path: string, component: () => ReactElement) =>
  createRoute({ getParentRoute: () => rootRoute, path, component });

export const routeTree = rootRoute.addChildren([
  route("/", () => <Title order={2}>Dashboard</Title>),
  route("/products", ProductsPage),
  route("/products/$productId", ProductDetailsPage),
  route("/products/categories", CategoriesPage),
  route("/setup", Setup),
  route("/settings", Settings),
]);
