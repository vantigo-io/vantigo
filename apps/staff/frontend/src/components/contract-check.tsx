import { Badge, Group, List, Text } from "@mantine/core";
import { IconCircleCheck, IconCircleDashed, IconCircleX } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { contractCheckQueryOptions, type StaffContractCheckItem } from "../api/employments";
import "../i18n";
import { useStaffFormat } from "../lib/format";
import { FieldGroup } from "./field-group";

const looks: Record<StaffContractCheckItem["state"], { color: string; icon: typeof IconCircleCheck }> = {
  present: { color: "teal", icon: IconCircleCheck },
  missing: { color: "red", icon: IconCircleX },
  not_applicable: { color: "gray", icon: IconCircleDashed },
};

/**
 * The § 14-6 check of one employment (`GET /employments/{id}/contract-check`):
 * the seventeen items a written contract must hold, each present, missing or
 * not applicable as the server judged them on the terms in force today, and
 * whether a signed contract is recorded. Each state is said in words beside
 * its colour. It informs; nothing here stops a save.
 */
export const ContractCheck = ({ employmentId }: { employmentId: number }) => {
  const format = useStaffFormat();
  const t = format.t;
  const { data } = useQuery(contractCheckQueryOptions(employmentId));
  if (!data) return null;
  return (
    <FieldGroup title={t("contractCheck")} description={t("contractCheckExplained")}>
      <Text size="sm" fw={500}>
        {data.contractSignedAt
          ? t("contractSignedOn", { date: format.date(data.contractSignedAt) })
          : t("contractNotSigned")}
      </Text>
      <List listStyleType="none" spacing={4} size="sm">
        {data.items.map((item) => {
          const look = looks[item.state];
          const Icon = look.icon;
          return (
            <List.Item key={item.item}>
              <Group gap="xs" wrap="nowrap" justify="space-between">
                <Group gap={6} wrap="nowrap">
                  <Icon size={16} color={`var(--mantine-color-${look.color}-6)`} aria-hidden />
                  <Text size="sm" component="span" ff="monospace">
                    {item.item})
                  </Text>
                  <Text size="sm" component="span">
                    {t(`contractItem_${item.item}`)}
                  </Text>
                </Group>
                <Badge size="sm" variant="light" color={look.color}>
                  {t(`contractState_${item.state}`)}
                </Badge>
              </Group>
            </List.Item>
          );
        })}
      </List>
    </FieldGroup>
  );
};
