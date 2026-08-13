import { Center, Loader, Menu, NavLink, Text } from "@mantine/core";
import {
  IconAddressBook,
  IconBolt,
  IconCategory,
  IconInbox,
  IconKey,
  IconLayoutDashboard,
  IconMailbox,
  IconMailOff,
  IconPackage,
  IconSettings,
  IconShieldCheck,
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
import { AppShellLayout, appUrl, type ShellApp, SpotlightSearchBox } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import { fetchBootstrapStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey, signOut } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
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
  capability?: "authorization";
  requiredPermissions?: readonly string[];
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
      { label: "Customers", to: "/customers", icon: IconUsers, requiredPermissions: ["customers:view"] },
      {
        label: "Contacts",
        to: "/contacts",
        icon: IconAddressBook,
        requiredPermissions: ["customers:contacts-view", "customers:associations-view"],
      },
    ],
  },
  {
    label: "Administration",
    items: [
      { label: "Users", to: "/admin/users", icon: IconUsers, ownerOnly: true },
      { label: "Roles & access", to: "/admin/roles", icon: IconShieldCheck, capability: "authorization" },
      { label: "Single sign-on", to: "/admin/sso", icon: IconKey, ownerOnly: true },
    ],
  },
  {
    label: "Communications",
    items: [
      { label: "Messages", to: "/messages", icon: IconInbox, requiredPermissions: ["communications:messages-view"] },
      {
        label: "Mailboxes",
        to: "/communications/mailboxes",
        icon: IconMailbox,
        requiredPermissions: ["communications:mailboxes-view"],
      },
      {
        label: "Suppressions",
        to: "/communications/suppressions",
        icon: IconMailOff,
        requiredPermissions: ["communications:suppressions-view"],
      },
    ],
  },
  {
    label: "Catalog",
    items: [
      {
        label: "Products",
        to: "/products",
        icon: IconPackage,
        requiredPermissions: [
          "products:products-view",
          "products:variants-view",
          "products:pricing-view",
          "products:categories-view",
          "products:tax-categories-view",
        ],
      },
      {
        label: "Categories",
        to: "/products/categories",
        icon: IconCategory,
        requiredPermissions: ["products:categories-view"],
      },
    ],
  },
  {
    label: "Energy",
    items: [
      {
        label: "Metering points",
        to: "/energy/metering-points",
        icon: IconBolt,
        requiredPermissions: ["energy:metering-points-view", "energy:meters-view"],
      },
    ],
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
const hasPermissions = (permissions: string[] | undefined, required?: readonly string[]) =>
  !required?.length ||
  permissions?.includes("*") === true ||
  required.every((permission) => permissions?.includes(permission) === true);

type AppWithPermissions = ShellApp;

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
  const authorization = useQuery({
    queryKey: ["authorization", "me"],
    queryFn: getAuthorizationMe,
    enabled: !isPublic && !!session,
    retry: false,
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
  const permissions = authorization.data?.permissions;
  const canManageAuthorization = authorization.data?.canManageAuthorization === true;
  return (
    <>
      <AppShellLayout
        moduleName="Vantigo"
        apps={(
          [
            {
              id: "customers",
              label: "Customers",
              icon: IconUsers,
              url: "/customers",
              onClick: () => void navigate({ to: "/customers", search: { page: 1, search: "" } }),
              requiredPermissions: ["customers:view"],
            },
            {
              id: "communications",
              label: "Communications",
              icon: IconInbox,
              url: "/messages",
              onClick: () => void navigate({ to: "/messages", search: { page: 1, archived: undefined } }),
              requiredPermissions: ["communications:messages-view"],
            },
            {
              id: "products",
              label: "Products",
              icon: IconPackage,
              url: "/products",
              onClick: () =>
                void navigate({ to: "/products", search: { page: 1, search: "", status: "", categoryId: "" } }),
              requiredPermissions: [
                "products:products-view",
                "products:variants-view",
                "products:pricing-view",
                "products:categories-view",
                "products:tax-categories-view",
              ],
            },
            {
              id: "energy",
              label: "Energy",
              icon: IconBolt,
              url: "/energy/metering-points",
              onClick: () => void navigate({ to: "/energy/metering-points", search: { page: 1, search: "" } }),
              requiredPermissions: ["energy:metering-points-view", "energy:meters-view"],
            },
          ] satisfies readonly AppWithPermissions[]
        ).filter((app) => hasPermissions(permissions, app.requiredPermissions))}
        user={session.user}
        userMenuItems={
          <Menu.Item component={Link} to="/settings" leftSection={<IconSettings size={14} />}>
            Settings
          </Menu.Item>
        }
        onSignOut={() => logout.mutate()}
        signOutDisabled={logout.isPending}
        navbarTop={<SpotlightSearchBox />}
        nav={(close) =>
          navSections.map((section, sectionIndex) => {
            const items = section.items.filter(
              (item) =>
                (!item.ownerOnly || isOwner) &&
                (!item.capability || canManageAuthorization) &&
                hasPermissions(permissions, item.requiredPermissions),
            );
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
