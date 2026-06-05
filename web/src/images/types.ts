import type { ImageGenerationJob, ImageProviderSettings, ImageStorageSettings } from "../app/types";

export type ImageMode = "text_to_image" | "image_to_image" | "multi_image_edit";
export type ImagesTab = "generate" | "library" | "history" | "settings";
export type ImageLibraryScope = "public" | "private";

export interface ImageJobResponse {
  job?: ImageGenerationJob;
  status?: unknown;
}

export interface ImagePrivateStatus {
  unlocked?: boolean;
  expiresAt?: string;
}

export interface ImageSettingsDraft extends Required<ImageProviderSettings> {
  xaiApiKey: string;
  clearApiKey: boolean;
}

export interface ImageStorageSettingsDraft extends Required<ImageStorageSettings> {
  s3AccessKeyId: string;
  s3SecretAccessKey: string;
  s3SessionToken: string;
  clearSecret: boolean;
}

export const IMAGE_MODES: Array<{ id: ImageMode; label: string; hint: string }> = [
  { id: "text_to_image", label: "文生图", hint: "不使用参考图" },
  { id: "image_to_image", label: "图生图", hint: "需要 1 张参考图" },
  { id: "multi_image_edit", label: "多图编辑", hint: "需要 2-3 张参考图" },
];

export const ASPECT_OPTIONS = ["", "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"];
export const RESOLUTION_OPTIONS = ["", "1k", "2k"];
export const MODEL_OPTIONS = ["grok-imagine-image-quality", "grok-imagine-image"];
