import { Button, Popover, SimpleGrid, UnstyledButton } from "@mantine/core";
import { IconMoodPlus } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";

/** A short, curated set: what routines, policies and how-tos tend to want (K3: one emoji, optional). */
const ICONS: readonly string[] = [
  "📄 📘 📗 📕 📒 📚 📝 📌 📎 📋 📊 📈 🗂️ 🗃️ 🗓️ ⏰",
  "✅ ☑️ ⚠️ ❗ ❓ 💡 🔒 🔑 🛠️ ⚙️ 🧰 🔧 🧪 🖥️ 💻 📱",
  "📞 ✉️ 💬 📣 👋 🤝 👥 🧑‍💼 🏢 🏠 🚚 📦 🛒 💰 🧾 💳",
  "⚡ 🔥 🌱 🌍 🚀 🎯 ⭐ ❤️ 🩺 🦺 ♻️ 🧹 🍽️ ☕ 🎉 🏁",
].flatMap((row) => row.split(" "));

/**
 * The article's icon: the emoji it has, or a button to give it one; a grid
 * to choose from, and a way to take it away. Read-only for a reader.
 */
export const IconPicker = ({
  value,
  onChange,
  disabled,
}: {
  value: string | null;
  onChange: (icon: string) => void;
  disabled?: boolean;
}) => {
  const { t } = useI18n("knowledge");
  const [opened, setOpened] = useState(false);
  if (disabled)
    return value ? (
      <span role="img" aria-label={t("iconLabel")} style={{ fontSize: 40, lineHeight: 1 }}>
        {value}
      </span>
    ) : null;
  const choose = (icon: string) => {
    setOpened(false);
    onChange(icon);
  };
  return (
    <Popover opened={opened} onChange={setOpened} position="bottom-start" withArrow shadow="md">
      <Popover.Target>
        {value ? (
          <UnstyledButton
            aria-label={t("changeIcon")}
            onClick={() => setOpened((o) => !o)}
            style={{ fontSize: 40, lineHeight: 1 }}
          >
            {value}
          </UnstyledButton>
        ) : (
          <Button
            variant="subtle"
            color="gray"
            size="xs"
            leftSection={<IconMoodPlus size={14} />}
            onClick={() => setOpened((o) => !o)}
          >
            {t("addIcon")}
          </Button>
        )}
      </Popover.Target>
      <Popover.Dropdown>
        <SimpleGrid cols={8} spacing={4} role="listbox" aria-label={t("iconLabel")}>
          {ICONS.map((icon) => (
            <UnstyledButton
              key={icon}
              role="option"
              aria-selected={icon === value}
              aria-label={icon}
              onClick={() => choose(icon)}
              style={{ fontSize: 20, lineHeight: 1, padding: 4, textAlign: "center" }}
            >
              {icon}
            </UnstyledButton>
          ))}
        </SimpleGrid>
        {value && (
          <Button variant="subtle" color="gray" size="xs" fullWidth mt="xs" onClick={() => choose("")}>
            {t("removeIcon")}
          </Button>
        )}
      </Popover.Dropdown>
    </Popover>
  );
};
