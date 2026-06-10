import { Container, MantineProvider, Text } from "@mantine/core";
import {
  createRootRoute,
  createRoute,
  createRouter,
  Navigate,
  Outlet,
} from "@tanstack/react-router";
import { commonTheme } from "@vantigo/ui";
import { authClient } from "./auth";
import { AccountPage } from "./pages/account/page";
import { LoginPage } from "./pages/login/page";

// Root Route: Setup global Providers (Mantine, routing outlets)
const rootRoute = createRootRoute({
  component: () => (
    <MantineProvider theme={commonTheme}>
      <Outlet />
    </MantineProvider>
  ),
});

// Root Index Redirect Route (/) -> (/idp)
const rootIndexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: () => <Navigate to="/idp" />,
});

// App Index Redirect Route (/idp) -> (/idp/account) or (/idp/login)
const idpIndexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/idp",
  component: () => {
    const { data: session, isPending } = authClient.useSession();
    if (isPending) {
      return (
        <Container size="xs" mt="xl" style={{ textAlign: "center" }}>
          <Text size="sm" color="dimmed">
            Loading session...
          </Text>
        </Container>
      );
    }
    if (session?.user) {
      return <Navigate to="/idp/account" />;
    }
    return <Navigate to="/idp/login" search={{ redirectUrl: undefined }} />;
  },
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/idp/login",
  validateSearch: (search: Record<string, unknown>) => {
    return {
      redirectUrl: (search.redirectUrl as string) || undefined,
    };
  },
  component: LoginPage,
});

const accountRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/idp/account",
  component: AccountPage,
});

// Configure Route Tree
const routeTree = rootRoute.addChildren([
  rootIndexRoute,
  idpIndexRoute,
  loginRoute,
  accountRoute,
]);

export const router = createRouter({
  routeTree,
  defaultPreload: "intent",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
