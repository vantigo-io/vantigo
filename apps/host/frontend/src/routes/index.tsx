import { Button, Card, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import { IconMessage, IconPackage, IconUsers } from "@tabler/icons-react";
import { createFileRoute, Link } from "@tanstack/react-router";

const modules = [
  { title: "Customers", description: "Manage customers and contacts.", to: "/customers", icon: IconUsers },
  {
    title: "Communications",
    description: "Review and send messages.",
    to: "/messages",
    icon: IconMessage,
  },
  { title: "Products", description: "Manage products, prices and categories.", to: "/products", icon: IconPackage },
] as const;

const DashboardPage = () => (
  <Stack gap="lg">
    <Title order={2}>Dashboard</Title>
    <Text c="dimmed">Choose a module to get started.</Text>
    <SimpleGrid cols={{ base: 1, sm: 3 }}>
      {modules.map((module) => (
        <Card key={module.to} withBorder>
          <Stack>
            <module.icon size={28} />
            <Title order={4}>{module.title}</Title>
            <Text c="dimmed" size="sm">
              {module.description}
            </Text>
            <Button component={Link} to={module.to}>
              Open
            </Button>
          </Stack>
        </Card>
      ))}
    </SimpleGrid>
  </Stack>
);
export const Route = createFileRoute("/")({ component: DashboardPage });
