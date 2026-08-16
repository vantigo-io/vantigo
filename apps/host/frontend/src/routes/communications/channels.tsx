import { createFileRoute } from "@tanstack/react-router";
import { ChannelsPage } from "@vantigo/communications-ui/pages/admin.channels";
export const Route = createFileRoute("/communications/channels")({ component: ChannelsPage });
