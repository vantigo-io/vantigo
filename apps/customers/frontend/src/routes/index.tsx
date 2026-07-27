import { AreaChart, DonutChart } from "@mantine/charts";
import { Badge, Card, Group, SimpleGrid, Stack, Text, ThemeIcon, Title } from "@mantine/core";
import {
  IconBuildingBank,
  IconTrendingDown,
  IconTrendingUp,
  IconUserPlus,
  IconUsers,
  IconUsersGroup,
} from "@tabler/icons-react";
import { createFileRoute } from "@tanstack/react-router";

const stats = [
  {
    label: "Total customers",
    value: "1,284",
    icon: IconUsers,
    color: "blue",
    trend: "+4.3%",
    trendUp: true,
  },
  {
    label: "New this month",
    value: "37",
    icon: IconUserPlus,
    color: "teal",
    trend: "+12.1%",
    trendUp: true,
  },
  {
    label: "Active customers",
    value: "1,102",
    icon: IconUsersGroup,
    color: "grape",
    trend: "+1.8%",
    trendUp: true,
  },
  {
    label: "Churn rate",
    value: "2.4%",
    icon: IconBuildingBank,
    color: "orange",
    trend: "-0.6%",
    trendUp: false,
  },
];

const growthData = [
  { month: "Feb", customers: 980 },
  { month: "Mar", customers: 1021 },
  { month: "Apr", customers: 1054 },
  { month: "May", customers: 1119 },
  { month: "Jun", customers: 1187 },
  { month: "Jul", customers: 1284 },
];

const legalTypeData = [
  { name: "Limited company", value: 742, color: "blue.6" },
  { name: "Sole proprietor", value: 318, color: "teal.6" },
  { name: "Partnership", value: 141, color: "grape.6" },
  { name: "Other", value: 83, color: "gray.5" },
];

const DashboardPage = () => (
  <Stack gap="lg">
    <Title order={2}>Dashboard</Title>

    <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }}>
      {stats.map((stat) => (
        <Card key={stat.label} withBorder padding="lg" radius="md">
          <Group justify="space-between" align="flex-start">
            <div>
              <Text size="xs" c="dimmed" tt="uppercase" fw={700}>
                {stat.label}
              </Text>
              <Text size="xl" fw={700} mt={4}>
                {stat.value}
              </Text>
            </div>
            <ThemeIcon color={stat.color} variant="light" size="lg" radius="md">
              <stat.icon size={20} stroke={1.5} />
            </ThemeIcon>
          </Group>
          <Badge
            mt="sm"
            variant="light"
            color={stat.trendUp ? "teal" : "red"}
            leftSection={stat.trendUp ? <IconTrendingUp size={12} /> : <IconTrendingDown size={12} />}
          >
            {stat.trend} vs last month
          </Badge>
        </Card>
      ))}
    </SimpleGrid>

    <SimpleGrid cols={{ base: 1, lg: 2 }}>
      <Card withBorder padding="lg" radius="md">
        <Text fw={600} mb="md">
          Customer growth
        </Text>
        <AreaChart
          h={260}
          data={growthData}
          dataKey="month"
          series={[{ name: "customers", color: "blue.6", label: "Customers" }]}
          curveType="monotone"
          withGradient
        />
      </Card>

      <Card withBorder padding="lg" radius="md">
        <Text fw={600} mb="md">
          Customers by legal type
        </Text>
        <Group justify="center">
          <DonutChart h={260} data={legalTypeData} withLabelsLine withLabels tooltipDataSource="segment" />
        </Group>
      </Card>
    </SimpleGrid>
  </Stack>
);

export const Route = createFileRoute("/")({ component: DashboardPage });
