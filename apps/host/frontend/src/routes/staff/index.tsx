import { createFileRoute, redirect } from "@tanstack/react-router";
import { visibleStaffNavItems } from "@vantigo/staff-ui/lib/navigation";
import { MY_PAGE_ROUTE_PATH, PEOPLE_ROUTE_PATH } from "@vantigo/staff-ui/lib/routes";
import { validatePeopleSearch } from "@vantigo/staff-ui/lib/search";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

// The staff app prefix is a pure redirect: People for whoever the sidebar
// offers it (staff:view or staff:manage) beside the app itself, My page for
// everyone else. The permissions are the guard's own query, usually cached;
// when they cannot be read, My page — the one destination every Staff user
// has — is the safe landing.
export const Route = createFileRoute("/staff/")({
  beforeLoad: async ({ context }) => {
    const permissions = await context.queryClient
      .fetchQuery({ queryKey: ["authorization", "me", "none"], queryFn: getAuthorizationMe, staleTime: 300_000 })
      .then((authorization) => authorization.permissions)
      .catch(() => undefined);
    if (
      hasPermissions(permissions, ["staff:access"]) &&
      visibleStaffNavItems(permissions).some((item) => item.key === "people")
    ) {
      throw redirect({ to: PEOPLE_ROUTE_PATH, search: validatePeopleSearch({}) });
    }
    throw redirect({ to: MY_PAGE_ROUTE_PATH, search: { tab: undefined } });
  },
});
