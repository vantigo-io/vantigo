import { Alert, Card, Code, Divider, List, Stack, Text, Title } from "@mantine/core";
import { IconLock } from "@tabler/icons-react";
import { createFileRoute } from "@tanstack/react-router";
export const Route = createFileRoute("/settings")({ component: SettingsPage });
function SettingsPage() {
  return (
    <Stack gap="xl">
      <div>
        <Text className="eyebrow">Developer reference</Text>
        <Title order={2}>Service API</Title>
        <Text c="dimmed">Understand the connection without handling credentials in the browser.</Text>
      </div>
      <Alert icon={<IconLock size={18} />} color="indigo" title="Credentials stay server-side">
        This screen never asks for, stores, or exposes API keys. Authentication is handled by the signed-in workspace
        session.
      </Alert>
      <Card withBorder radius="lg">
        <Title order={4}>Read-only request shape</Title>
        <Text c="dimmed" size="sm">
          The UI uses the authenticated session cookie and sends no secret headers.
        </Text>
        <Divider my="lg" />
        <Code block>
          GET /api/v1/messages?page=1&amp;pageSize=20{"\n"}Cookie: authenticated workspace session{"\n\n"}GET
          /api/v1/messages/&#123;id&#125;{"\n"}GET /api/v1/messages/&#123;id&#125;/events
        </Code>
        <List mt="lg">
          <List.Item>Responses use the standard Customer-style data and pagination envelope.</List.Item>
          <List.Item>Delivery status is recorded by the communications service.</List.Item>
          <List.Item>Contact your workspace administrator to configure service credentials.</List.Item>
        </List>
      </Card>
    </Stack>
  );
}
