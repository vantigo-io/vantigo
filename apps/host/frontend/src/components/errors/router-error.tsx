import { useRouter } from "@tanstack/react-router";
import { ForbiddenPage } from "./forbidden";
import { OfflinePage } from "./offline";
import { routerErrorKind } from "./router-error-utils";
import { UnexpectedErrorPage } from "./unexpected-error";

export const RouterError = ({ error }: { error: unknown }) => {
  const router = useRouter();
  const reset = () => void router.invalidate();

  switch (routerErrorKind(error)) {
    case "offline":
      return <OfflinePage retry={reset} />;
    case "forbidden":
      return <ForbiddenPage />;
    default:
      return <UnexpectedErrorPage error={error} reset={reset} />;
  }
};
