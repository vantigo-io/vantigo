import { createFileRoute, useNavigate, useParams } from "@tanstack/react-router";
import { availabilityQueryOptions } from "@vantigo/assistant-ui/api/assistant";
import { AssistantPage } from "@vantigo/assistant-ui/pages/assistant";
import { useI18n } from "@vantigo/frontend-shell";
import { areaForKey } from "../apps";
import { useOpenRoute } from "../lib/open-route";
import { appLayoutOptions, moduleDisabledNotFound } from "./-app-layout";
import "../i18n";

const layout = appLayoutOptions("assistant");

const AssistantRoute = () => {
  const { t } = useI18n("host");
  const navigate = useNavigate();
  const openRoute = useOpenRoute();
  const { conversationId } = useParams({ strict: false }) as { conversationId?: string };
  // The page is the layout route's own, so choosing a conversation (or the first message creating one)
  // moves the address without remounting the view and losing the stream it is following.
  return (
    <AssistantPage
      conversationId={conversationId ?? null}
      onSelect={(id) => void navigate({ to: id ? `/assistant/${id}` : "/assistant" } as never)}
      onNavigate={openRoute}
      appLabel={(key) => {
        try {
          return t(areaForKey(key as never).label);
        } catch {
          return key;
        }
      }}
    />
  );
};

/**
 * The Assistant app (design S1). Unlike the other apps it is gated on the person, not on a permission:
 * the availability answer says whether they have the assistant (S8), and a person without it meets the
 * not-enabled page, as for a module that is off.
 */
export const Route = createFileRoute("/assistant")({
  ...layout,
  beforeLoad: async ({ context }) => {
    layout.beforeLoad();
    const availability = await context.queryClient.fetchQuery(availabilityQueryOptions()).catch(() => undefined);
    if (!availability?.available) throw moduleDisabledNotFound("assistant");
  },
  component: AssistantRoute,
});
