import { createFileRoute, redirect } from "@tanstack/react-router";

// The communications app prefix is a pure redirect: the inbox is its home.
export const Route = createFileRoute("/communications/")({
  beforeLoad: () => {
    // The inbox validates its search params, so TanStack requires every key
    // on a typed navigation; these are what its validator derives from an
    // empty query string.
    throw redirect({
      to: "/communications/inbox",
      search: {
        conversationId: undefined,
        status: undefined,
        customerId: undefined,
        tagId: undefined,
        unreadOnly: undefined,
      },
    });
  },
});
