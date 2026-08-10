import { Center, Loader, Menu, NavLink, Text } from "@mantine/core";
import {
  IconAddressBook,
  IconBolt,
  IconCategory,
  IconInbox,
  IconLayoutDashboard,
  IconMailbox,
  IconMailOff,
  IconPackage,
  IconSettings,
  IconUsers,
} from "@tabler/icons-react";
import type { QueryClient } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import {
  createRootRouteWithContext,
  Link,
  Outlet,
  redirect,
  useNavigate,
  useRouterState,
} from "@tanstack/react-router";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";
import { AppShellLayout, appUrl, SpotlightSearchBox } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import { fetchBootstrapStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey, signOut } from "../api/auth";
import { AppSpotlight } from "../components/app-spotlight";

const publicPaths = new Set([
  "/sign-in",
  "/setup",
  "/forgot-password",
  "/reset-password",
  "/password-reset",
  "/accept-invitation",
  "/invitations/accept",
]);

interface NavItem {
  label: string;
  to: string;
  icon: typeof IconUsers;
  ownerOnly?: boolean;
}

interface NavSection {
  label?: string;
  items: readonly NavItem[];
}

const navSections: readonly NavSection[] = [
  {
    items: [{ label: "Dashboard", to: "/", icon: IconLayoutDashboard }],
  },
  {
    label: "CRM",
    items: [
      { label: "Customers", to: "/customers", icon: IconUsers },
      { label: "Contacts", to: "/contacts", icon: IconAddressBook },
    ],
  },
  {
    label: "Communications",
    items: [
      { label: "Messages", to: "/messages", icon: IconInbox },
      { label: "Mailboxes", to: "/communications/mailboxes", icon: IconMailbox, ownerOnly: true },
      { label: "Suppressions", to: "/communications/suppressions", icon: IconMailOff, ownerOnly: true },
    ],
  },
  {
    label: "Catalog",
    items: [
      { label: "Products", to: "/products", icon: IconPackage },
      { label: "Categories", to: "/products/categories", icon: IconCategory },
    ],
  },
  {
    label: "Energy",
    items: [{ label: "Metering points", to: "/energy/metering-points", icon: IconBolt }],
  },
];

/**
 * The nav entry whose path is the longest prefix of the current location wins,
 * so "/contacts" highlights Contacts without also lighting Customers.
 */
const activeNavPath = (pathname: string, items: readonly NavItem[]) => {
  let best: string | undefined;
  for (const item of items) {
    const matches = item.to === "/" ? pathname === "/" : pathname === item.to || pathname.startsWith(`${item.to}/`);
    if (matches && (best === undefined || item.to.length > best.length)) best = item.to;
  }
  return best;
};

const RootLayout = () => {
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const isPublic = publicPaths.has(pathname);
  const { data: session, isPending } = useQuery({
    queryKey: sessionQueryKey,
    queryFn: fetchSession,
    enabled: !isPublic,
    staleTime: 300_000,
  });
  const logout = useMutation({
    mutationFn: signOut,
    onSuccess: () => {
      queryClient.setQueryData(sessionQueryKey, null);
      window.location.assign(appUrl("/sign-in"));
    },
  });
  useEffect(() => {
    if (!isPublic && !isPending && !session) window.location.assign(appUrl("/sign-in"));
  }, [isPending, isPublic, session]);
  if (isPublic) return <Outlet />;
  if (isPending || !session)
    return (
      <Center mih="100vh">
        <Loader size="sm" />
      </Center>
    );
  const isOwner = session.user.roles.includes("Owner");
  return (
    <>
      <AppShellLayout
        moduleName="Vantigo"
        apps={[
          {
            id: "customers",
            label: "Customers",
            icon: IconUsers,
            url: "/customers",
            onClick: () => void navigate({ to: "/customers", search: { page: 1, search: "" } }),
          },
          {
            id: "communications",
            label: "Communications",
            icon: IconInbox,
            url: "/messages",
            onClick: () => void navigate({ to: "/messages", search: { page: 1, archived: undefined } }),
          },
          {
            id: "products",
            label: "Products",
            icon: IconPackage,
            url: "/products",
            onClick: () =>
              void navigate({ to: "/products", search: { page: 1, search: "", status: "", categoryId: "" } }),
          },
          {
            id: "energy",
            label: "Energy",
            icon: IconBolt,
            url: "/energy/metering-points",
            onClick: () => void navigate({ to: "/energy/metering-points", search: { page: 1, search: "" } }),
          },
        ]}
        user={session.user}
        userMenuItems={
          isOwner && (
            <Menu.Item component={Link} to="/settings" leftSection={<IconSettings size={14} />}>
              Settings
            </Menu.Item>
          )
        }
        onSignOut={() => logout.mutate()}
        signOutDisabled={logout.isPending}
        navbarTop={<SpotlightSearchBox />}
        nav={(close) =>
          navSections.map((section, sectionIndex) => {
            const items = section.items.filter((item) => !item.ownerOnly || isOwner);
            if (items.length === 0) return null;
            const active = activeNavPath(pathname, items);
            return (
              <div key={section.label ?? sectionIndex}>
                {section.label && (
                  <Text size="xs" fw={700} tt="uppercase" c="dimmed" mt="md" mb={4} px="xs">
                    {section.label}
                  </Text>
                )}
                {items.map((item) => (
                  <NavLink
                    key={item.to}
                    component={Link}
                    to={item.to}
                    // Mantine styles [aria-current="page"] as active; keep TanStack's
                    // own marker exact so only activeNavPath decides the highlight.
                    activeOptions={{ exact: true }}
                    label={item.label}
                    leftSection={<item.icon size={18} stroke={1.5} />}
                    active={item.to === active}
                    onClick={close}
                  />
                ))}
              </div>
            );
          })
        }
      >
        <Outlet />
      </AppShellLayout>
      <AppSpotlight />
      <TanStackRouterDevtools />
      <ReactQueryDevtools />
    </>
  );
};

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  beforeLoad: async ({ location, context }) => {
    if (publicPaths.has(location.pathname)) return;
    const session = await context.queryClient.fetchQuery({
      queryKey: sessionQueryKey,
      queryFn: fetchSession,
      staleTime: 300_000,
    });
    if (!session) {
      let available = false;
      try {
        available = (await fetchBootstrapStatus()).available;
      } catch {
        /* optional */
      }
      throw redirect({ to: available ? "/setup" : "/sign-in" });
    }
  },
  component: RootLayout,
});
