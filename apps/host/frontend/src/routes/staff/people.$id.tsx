import { useQuery } from "@tanstack/react-query";
import { createFileRoute, notFound } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { PersonTimeline } from "@vantigo/planning-ui";
import { personQueryOptions } from "@vantigo/staff-ui/api/people";
import { validatePersonSearch } from "@vantigo/staff-ui/lib/search";
import { PersonPage } from "@vantigo/staff-ui/pages/person";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasPermissions } from "../../navigation";
import "../../i18n";

/**
 * The person page with the tabs other modules add (planning design §7, plan
 * reading 4): Staff's tabs are its own, and this route — not the package,
 * which never names another module — passes the Planning tab through the
 * page's `extraTabs` slot. It is drawn when the module is on and the caller
 * holds `planning:access` and either `planning:view` or is the person (the
 * person answer's `capabilities.self`, the page's own query): the timeline
 * answers anyone's to `planning:view` and only their own to the person.
 */
const StaffPersonPage = () => {
  const { t } = useI18n("host");
  const { id } = Route.useParams();
  const personId = Number(id);
  const planningOn = enabledModuleKeys().includes("planning");
  const { data: session } = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: planningOn && !!session,
    retry: false,
    staleTime: 300_000,
  });
  const person = useQuery({ ...personQueryOptions(personId), enabled: planningOn, retry: false });
  const permissions = authorization.data?.permissions;
  const planningTab =
    planningOn &&
    hasPermissions(permissions, ["planning:access"]) &&
    (hasPermissions(permissions, ["planning:view"]) || person.data?.capabilities.self === true);
  return (
    <PersonPage
      extraTabs={
        planningTab
          ? [
              {
                value: "planning",
                label: t("navigation.planning"),
                render: () => <PersonTimeline personId={personId} />,
              },
            ]
          : undefined
      }
    />
  );
};

/**
 * One person. The path is the package's own `PERSON_ROUTE_PATH` — People's
 * rows, New person and the org view link to it — so it is not a free choice
 * here.
 *
 * A path that is not a positive whole number never reaches the API as
 * `GET /people/NaN`: it is a not-found, which the `/staff` layout's own
 * boundary renders. The module gate and the permission gate are the
 * layout's and the guard's, as for every other staff route.
 */
export const Route = createFileRoute("/staff/people/$id")({
  validateSearch: validatePersonSearch,
  beforeLoad: ({ params }) => {
    if (!/^\d+$/.test(params.id) || !Number.isSafeInteger(Number(params.id)) || Number(params.id) <= 0) {
      throw notFound();
    }
  },
  component: StaffPersonPage,
});
