import { useParams } from "@tanstack/react-router";
import { SendBatchPage } from "../pages/send-batch";

/** A send batch's page as the host mounts it: the route owns the id and hands it over as a number. */
export const SendBatchRoute = () => {
  const { batchId } = useParams({ strict: false }) as { batchId: string };
  return <SendBatchPage batchId={Number(batchId)} />;
};
