import { ActionIcon, Button, Card, Group, Select, SimpleGrid, Stack, Text, TextInput } from "@mantine/core";
import type { UseFormReturnType } from "@mantine/form";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";
import { emptyNextOfKin, type NextOfKinFormValues } from "../lib/person-form";
import { MAX_NEXT_OF_KIN, nextOfKinRelationships } from "../lib/terms";

/**
 * The next of kin as a list editor, in the order they are to be listed —
 * HR's replace and My page's both send the whole list. At most three.
 */
export const NextOfKinFields = <V extends { nextOfKin: NextOfKinFormValues[] }>({
  form,
}: {
  form: UseFormReturnType<V>;
}) => {
  const { t } = useI18n("staff");
  const rows = (form.values as { nextOfKin: NextOfKinFormValues[] }).nextOfKin;
  const path = (index: number, field: keyof NextOfKinFormValues) => `nextOfKin.${index}.${field}` as never;
  return (
    <Stack gap="sm">
      {rows.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noNextOfKin")}
        </Text>
      )}
      {rows.map((_, index) => (
        // The list has no ids of its own; its order is its identity.
        <Card key={index} withBorder padding="sm" radius="sm">
          <Group align="flex-start" wrap="nowrap" gap="xs">
            <SimpleGrid cols={{ base: 1, sm: 2 }} style={{ flex: 1 }}>
              <TextInput label={t("name")} withAsterisk {...form.getInputProps(path(index, "name"))} />
              <Select
                label={t("relationship")}
                withAsterisk
                allowDeselect={false}
                data={nextOfKinRelationships.map((value) => ({ value, label: t(`relationship_${value}`) }))}
                {...form.getInputProps(path(index, "relationship"))}
              />
              <TextInput label={t("phone")} type="tel" {...form.getInputProps(path(index, "phone"))} />
              <TextInput label={t("note")} {...form.getInputProps(path(index, "note"))} />
            </SimpleGrid>
            <ActionIcon
              variant="subtle"
              color="red"
              mt={28}
              aria-label={t("removeNextOfKin", { n: index + 1 })}
              onClick={() => form.removeListItem("nextOfKin" as never, index)}
            >
              <IconTrash size={16} />
            </ActionIcon>
          </Group>
        </Card>
      ))}
      {rows.length < MAX_NEXT_OF_KIN ? (
        <Button
          variant="light"
          leftSection={<IconPlus size={16} />}
          onClick={() => form.insertListItem("nextOfKin" as never, emptyNextOfKin() as never)}
          style={{ alignSelf: "flex-start" }}
        >
          {t("addNextOfKin")}
        </Button>
      ) : (
        <Text size="sm" c="dimmed">
          {t("nextOfKinMax", { max: MAX_NEXT_OF_KIN })}
        </Text>
      )}
    </Stack>
  );
};
