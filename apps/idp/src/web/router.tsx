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
import { getVantigoConfig } from "./config";
import { AccountPage } from "./pages/account/page";
import { LoginPage } from "./pages/login/page";

const config = getVantigoConfig();

// Root Route: Setup global Providers (Mantine, routing outlets)
const rootRoute = createRootRoute({
  component: () => (
    <MantineProvider theme={commonTheme}>
      <Outlet />
    </MantineProvider>
  ),
});

// App Index Redirect Route (/) -> (/account) or (/login) relative to basepath
const idpIndexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
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
      return <Navigate to="/account" />;
    }
    return <Navigate to="/login" search={{ redirectUrl: undefined }} />;
  },
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  validateSearch: (search: Record<string, unknown>) => {
    return {
      redirectUrl: (search.redirectUrl as string) || undefined,
    };
  },
  component: LoginPage,
});

const accountRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/account",
  component: AccountPage,
});

// Configure Route Tree (routes are relative to basepath)
const routeTree = rootRoute.addChildren([
  idpIndexRoute,
  loginRoute,
  accountRoute,
]);

export const router = createRouter({
  routeTree,
  defaultPreload: "intent",
  basepath: config.sitePath,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
