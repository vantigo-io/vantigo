import { useParams } from "@tanstack/react-router";

/** Stands in for the person page (Task 5b): it says which person the link reached, and nothing else. */
export const PersonStandIn = () => {
  const { id } = useParams({ strict: false }) as { id: string };
  return <p data-testid="person-page">{id}</p>;
};
