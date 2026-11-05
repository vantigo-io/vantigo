import {
  ActionIcon,
  Box,
  Card,
  Checkbox,
  Group,
  Image,
  Loader,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconArrowLeft, IconArrowRight, IconStar, IconStarFilled, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageSection, useI18n } from "@vantigo/frontend-shell";
import { type ChangeEvent, type DragEvent, useState } from "react";
import "../i18n";
import {
  deleteProductImage,
  PRODUCT_IMAGE_USES,
  type ProductImage,
  type ProductImageUpdate,
  type ProductImageUse,
  productImagesQueryKey,
  productImagesQueryOptions,
  productImageUrl,
  updateProductImage,
  uploadProductImage,
} from "../api/products";
import type { ApiError } from "../api/request";

/** What upload accepts (messenger design N13); the server judges the bytes and has the last word. */
const IMAGE_ACCEPT = "image/jpeg,image/png";
const MAX_IMAGE_BYTES = 10 * 1024 * 1024;

/** One file the person picked, after it was refused. */
interface Refusal {
  name: string;
  message: string;
}

export interface ProductImagesProps {
  productId: number;
}

/**
 * The product page's Images section (messenger design N14): images dropped on
 * or picked, as thumbnails in order with Move left and Move right, a star for
 * the primary image, each one's alt text and where it may be shown, and delete.
 * Every change is the server's and the list is read again after it.
 */
export const ProductImages = ({ productId }: ProductImagesProps) => {
  const { t } = useI18n("products");
  const queryClient = useQueryClient();
  const { data: images = [], isLoading } = useQuery(productImagesQueryOptions(productId));
  const [uploading, setUploading] = useState<string | null>(null);
  const [refusals, setRefusals] = useState<Refusal[]>([]);
  const [over, setOver] = useState(false);

  const refresh = () => queryClient.invalidateQueries({ queryKey: productImagesQueryKey(productId) });

  const messageFor = (error: Error): string => {
    const { status, code } = error as ApiError;
    if (status === 503) return t("images.storageUnavailable");
    if (code && code in REFUSAL_CODES) return t(`images.refusals.${code as keyof typeof REFUSAL_CODES}`);
    return error.message;
  };

  const send = async (files: File[]) => {
    const failed: Refusal[] = [];
    for (const file of files) {
      if (file.type !== "image/jpeg" && file.type !== "image/png") {
        failed.push({ name: file.name, message: t("images.wrongType", { name: file.name }) });
        continue;
      }
      if (file.size > MAX_IMAGE_BYTES) {
        failed.push({ name: file.name, message: t("images.tooLarge", { name: file.name }) });
        continue;
      }
      setUploading(file.name);
      try {
        await uploadProductImage(productId, file);
        await refresh();
      } catch (error) {
        failed.push({
          name: file.name,
          message: `${t("images.couldNotUpload", { name: file.name })}: ${messageFor(error as Error)}`,
        });
      }
    }
    setUploading(null);
    setRefusals(failed);
  };

  const pick = (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    if (files.length > 0) void send(files);
  };
  const drop = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setOver(false);
    const files = Array.from(event.dataTransfer?.files ?? []);
    if (files.length > 0) void send(files);
  };

  const change = useMutation({
    mutationFn: ({ image, input }: { image: ProductImage; input: ProductImageUpdate }) =>
      updateProductImage(productId, image.id, input),
    onSuccess: () => refresh(),
    onError: (error) => notifications.show({ color: "red", title: t("images.couldNotSave"), message: error.message }),
  });
  const remove = useMutation({
    mutationFn: (image: ProductImage) => deleteProductImage(productId, image.id),
    onSuccess: () => refresh(),
    onError: (error) =>
      notifications.show({ color: "red", title: t("images.couldNotDelete"), message: messageFor(error) }),
  });

  const confirmDelete = (image: ProductImage) =>
    modals.openConfirmModal({
      title: t("images.deleteTitle"),
      centered: true,
      children: <Text size="sm">{t("images.deleteConfirm")}</Text>,
      labels: { confirm: t("common.delete"), cancel: t("common.cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(image),
    });

  return (
    <PageSection title={t("images.heading")}>
      <Stack>
        <Text size="sm" c="dimmed">
          {t("images.description")}
        </Text>
        <Box
          p="sm"
          style={{
            border: "1px dashed var(--mantine-color-gray-4)",
            borderRadius: "var(--mantine-radius-sm)",
            background: over ? "var(--mantine-color-gray-0)" : undefined,
          }}
          onDragOver={(event) => {
            event.preventDefault();
            setOver(true);
          }}
          onDragLeave={() => setOver(false)}
          onDrop={drop}
        >
          <Stack gap="xs">
            <Text size="sm" c="dimmed">
              {t("images.dropHere")}
            </Text>
            <input type="file" multiple accept={IMAGE_ACCEPT} aria-label={t("images.addImages")} onChange={pick} />
          </Stack>
        </Box>
        {uploading && (
          <Group gap="xs">
            <Loader size="xs" />
            <Text size="sm" c="dimmed">
              {t("images.uploading", { name: uploading })}
            </Text>
          </Group>
        )}
        {refusals.map((one, index) => (
          // Two folders can hand over the same file name, so the index is part of the key.
          <Text key={`${one.name}:${index}`} size="sm" c="red">
            {one.message}
          </Text>
        ))}
        {images.length === 0 && !isLoading && (
          <Text size="sm" c="dimmed">
            {t("images.none")}
          </Text>
        )}
        {images.length > 0 && (
          <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }}>
            {images.map((image, index) => (
              <ImageCard
                key={image.id}
                image={image}
                position={index + 1}
                first={index === 0}
                last={index === images.length - 1}
                onChange={(input) => change.mutate({ image, input })}
                onDelete={() => confirmDelete(image)}
              />
            ))}
          </SimpleGrid>
        )}
      </Stack>
    </PageSection>
  );
};

/** The codes of an image's refusals, each with a sentence in the catalog. */
const REFUSAL_CODES = {
  image_type_unsupported: true,
  image_too_large: true,
  image_too_small: true,
  image_invalid: true,
} as const;

interface ImageCardProps {
  image: ProductImage;
  position: number;
  first: boolean;
  last: boolean;
  onChange: (input: ProductImageUpdate) => void;
  onDelete: () => void;
}

const ImageCard = ({ image, position, first, last, onChange, onDelete }: ImageCardProps) => {
  const { t } = useI18n("products");
  const [altText, setAltText] = useState(image.altText ?? "");
  const toggleUse = (use: ProductImageUse, on: boolean) =>
    onChange({
      uses: PRODUCT_IMAGE_USES.filter((one) => (one === use ? on : image.uses.includes(one))),
    });

  return (
    <Card withBorder padding="xs" component="section" aria-label={t("images.thumbnailAlt", { position })}>
      <Stack gap="xs">
        <Image
          src={productImageUrl(image.productId, image.id, "thumb")}
          alt={image.altText ?? ""}
          h={160}
          fit="contain"
        />
        <Group justify="space-between" wrap="nowrap">
          <Group gap={4} wrap="nowrap">
            <ActionIcon
              variant="subtle"
              aria-label={t("images.moveLeft")}
              disabled={first}
              onClick={() => onChange({ sortOrder: position - 2 })}
            >
              <IconArrowLeft size={16} />
            </ActionIcon>
            <ActionIcon
              variant="subtle"
              aria-label={t("images.moveRight")}
              disabled={last}
              onClick={() => onChange({ sortOrder: position })}
            >
              <IconArrowRight size={16} />
            </ActionIcon>
          </Group>
          <Group gap={4} wrap="nowrap">
            <ActionIcon
              variant="subtle"
              color="yellow"
              aria-label={image.isPrimary ? t("images.primary") : t("images.makePrimary")}
              aria-pressed={image.isPrimary}
              disabled={image.isPrimary}
              onClick={() => onChange({ isPrimary: true })}
            >
              {image.isPrimary ? <IconStarFilled size={16} /> : <IconStar size={16} />}
            </ActionIcon>
            <ActionIcon variant="subtle" color="red" aria-label={t("images.delete")} onClick={onDelete}>
              <IconTrash size={16} />
            </ActionIcon>
          </Group>
        </Group>
        <TextInput
          label={t("images.altText")}
          description={t("images.altTextHelp")}
          value={altText}
          maxLength={250}
          onChange={(event) => setAltText(event.currentTarget.value)}
          onBlur={() => {
            if (altText.trim() !== (image.altText ?? "")) onChange({ altText: altText.trim() });
          }}
        />
        <Checkbox
          label={t("images.showInCommunications")}
          checked={image.uses.includes("communications")}
          onChange={(event) => toggleUse("communications", event.currentTarget.checked)}
        />
        <Checkbox
          label={t("images.showInMarketing")}
          description={t("images.marketingHelp")}
          checked={image.uses.includes("marketing")}
          onChange={(event) => toggleUse("marketing", event.currentTarget.checked)}
        />
      </Stack>
    </Card>
  );
};
