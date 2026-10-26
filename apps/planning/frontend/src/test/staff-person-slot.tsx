import { useParams } from "@tanstack/react-router";
import { PersonTimeline } from "../components/person-timeline";

/**
 * What the host puts on Staff's person page: a route of another app, whose
 * parameter is not this package's — the component is given the person and
 * reads nothing from the route.
 */
export const StaffPersonSlot = () => {
  const { id } = useParams({ strict: false }) as { id: string };
  return <PersonTimeline personId={Number(id)} />;
};
