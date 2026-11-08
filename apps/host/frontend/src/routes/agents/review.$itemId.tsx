import { createFileRoute, notFound } from "@tanstack/react-router";
import { ReviewItemRoutePage } from "./-agents-pages";
import { isUuid } from "./-ids";

/** One review item, where the internal note on the conversation links to. A path that is not a uuid is the layout's not-found. */
export const Route = createFileRoute("/agents/review/$itemId")({
  beforeLoad: ({ params }) => {
    if (!isUuid(params.itemId)) throw notFound();
  },
  component: ReviewItemRoutePage,
});
