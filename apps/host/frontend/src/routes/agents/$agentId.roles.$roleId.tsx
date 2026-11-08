import { createFileRoute, notFound } from "@tanstack/react-router";
import { RoleRoutePage } from "./-agents-pages";
import { isUuid } from "./-ids";

/** One role of an agent. A path that is not two uuids is the layout's not-found. */
export const Route = createFileRoute("/agents/$agentId/roles/$roleId")({
  beforeLoad: ({ params }) => {
    if (!isUuid(params.agentId) || !isUuid(params.roleId)) throw notFound();
  },
  component: RoleRoutePage,
});
