import { useParams } from "@tanstack/react-router";
import { ReminderRunPage } from "../pages/reminder-run";
import { CURRENT_USER_ID } from "./fixtures";

/** A run's page as the host mounts it: the route owns the id and hands it over as a number. */
export const ReminderRunRoute = () => {
  const { runId } = useParams({ strict: false }) as { runId: string };
  return <ReminderRunPage runId={Number(runId)} currentUserId={CURRENT_USER_ID} />;
};
