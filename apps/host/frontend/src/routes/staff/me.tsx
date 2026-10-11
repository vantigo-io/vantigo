import { createFileRoute } from "@tanstack/react-router";
import { validatePersonSearch } from "@vantigo/staff-ui/lib/search";
import { MyPage } from "@vantigo/staff-ui/pages/my-page";

export const Route = createFileRoute("/staff/me")({
  validateSearch: validatePersonSearch,
  component: MyPage,
});
