import { useParams } from "@tanstack/react-router";
import { BankFilePage } from "../pages/bank-file";
import { CURRENT_USER_ID } from "./fixtures";

/** The bank file's page as the host mounts it: the route owns the id and hands it over as a number. */
export const BankFileRoute = () => {
  const { bankFileId } = useParams({ strict: false }) as { bankFileId: string };
  return <BankFilePage bankFileId={Number(bankFileId)} currentUserId={CURRENT_USER_ID} />;
};
