import { createFileRoute } from "@tanstack/react-router";
import { validateMyPageSearch } from "@vantigo/staff-ui/lib/search";
import { MyPage } from "@vantigo/staff-ui/pages/my-page";

export const Route = createFileRoute("/staff/me")({
  validateSearch: validateMyPageSearch,
  component: MyPage,
});
