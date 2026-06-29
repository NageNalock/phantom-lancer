import type { ImageAsset, ImageGenerationJob, ImagePrompt, ImageStorageSettings } from "../app/types";

export type ImageMode = "text_to_image" | "image_to_image" | "multi_image_edit";
export type VideoMode = "text_to_video" | "image_to_video" | "multi_image_video" | "keyframes";
export type MediaMode = ImageMode | VideoMode;
export type MediaType = "image" | "video";
export type ProviderID = "xai" | "agnes";
export type ImagesTab = "generate" | "presets" | "library" | "history" | "settings";
export type ImageLibraryScope = "public" | "private";
export type AssetKind = "legacy" | "media";
export type AssetRef = { kind: AssetKind; id: string };

export interface ModelParameterSchema {
  sizePresets?: string[];
  defaultSize?: string;
  defaultWidth?: number;
  defaultHeight?: number;
  durationPresets?: string[];
  defaultDuration?: string;
  defaultNumFrames?: number;
  defaultFrameRate?: number;
  maxNumFrames?: number;
  numFramesStep?: number;
  minFrameRate?: number;
  maxFrameRate?: number;
  defaultN?: number;
  maxN?: number;
  responseFormats?: string[];
  defaultFormat?: string;
}

export interface ModelCapability {
  provider: ProviderID;
  model: string;
  label: string;
  mediaType: MediaType;
  deprecated: boolean;
  defaultFor?: string[];
  supportedModes: MediaMode[];
  parameters: ModelParameterSchema;
  minReferences: number;
  maxReferences: number;
}

export interface ProviderStatus {
  provider: ProviderID;
  enabled: boolean;
  hasApiKey: boolean;
  maskedApiKey?: string;
  defaultImageModel?: string;
  defaultVideoModel?: string;
  lastTestedAt?: string;
  lastError?: string;
  imageJobCount?: number;
  videoJobCount?: number;
}

export interface ProvidersStatus {
  providers: ProviderStatus[];
  models: ModelCapability[];
  defaultXAI: string;
}

export interface MediaGenerationSource {
  id: string;
  jobId: string;
  assetId?: string;
  slot: number;
  sourceType: string;
  sourceLabel?: string;
  sourceRole?: string;
  mimeType?: string;
  sizeBytes?: number;
  urlRedacted?: string;
  createdAt: string;
}

export interface MediaGenerationOutput {
  id: string;
  jobId: string;
  assetId?: string;
  slot: number;
  mediaType: MediaType;
  remoteUrlRedacted?: string;
  localName?: string;
  mimeType?: string;
  revisedPrompt?: string;
  storage?: string;
  sizeBytes?: number;
  metadata?: Record<string, unknown>;
  createdAt: string;
}

export interface MediaGenerationJob {
  id: string;
  mediaType: MediaType;
  provider: ProviderID;
  status: string;
  mode: MediaMode;
  modeLabel: string;
  model: string;
  endpoint?: string;
  prompt: string;
  parameters: Record<string, unknown>;
  sourceCount: number;
  outputCount: number;
  providerTaskId?: string;
  providerVideoId?: string;
  providerStatus?: string;
  progress?: number;
  usage: Record<string, unknown>;
  errorMessage?: string;
  createdAt: string;
  startedAt?: string;
  completedAt?: string;
  sources?: MediaGenerationSource[];
  outputs?: MediaGenerationOutput[];
}

export interface MediaAsset {
  id: string;
  mediaType: MediaType;
  assetType: string;
  status: string;
  private: boolean;
  provider?: ProviderID;
  model?: string;
  jobId?: string;
  sourceRole?: string;
  slot?: number;
  promptPreview?: string;
  revisedPromptPreview?: string;
  originalFilename?: string;
  originalSourceRedacted?: string;
  mimeType?: string;
  extension?: string;
  sizeBytes?: number;
  width?: number;
  height?: number;
  durationSeconds?: number;
  frameRate?: number;
  frameCount?: number;
  checksumSha256?: string;
  localName?: string;
  url?: string;
  downloadUrl?: string;
  storageBackend: string;
  objectStorageProfileId?: string;
  s3Bucket?: string;
  s3Region?: string;
  s3EndpointLabel?: string;
  s3Key?: string;
  s3Etag?: string;
  privateAt?: string;
  archivedAt?: string;
  deletedAt?: string;
  deletedReason?: string;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
}

export interface AppliedImagePrompt {
  prompt: ImagePrompt;
  nonce: number;
}

export interface MediaJobResponse {
  job?: MediaGenerationJob;
  jobType?: "media_image" | "media_video" | "legacy_image";
  status?: unknown;
}

export interface ImageJobResponse {
  job?: ImageGenerationJob;
  status?: unknown;
}

export interface ImagePrivateStatus {
  unlocked?: boolean;
  expiresAt?: string;
}

export interface ImageUploadResponse {
  asset?: ImageAsset;
  duplicate?: boolean;
}

export interface ImagePromptDraft {
  title: string;
  description: string;
  prompt: string;
  mode: ImageMode;
  model: string;
  aspectRatio: string;
  resolution: string;
  imageCount: number;
  tags: string[];
}

export interface MediaProviderSettingsDraft {
  provider: ProviderID;
  enabled: boolean;
  apiKey: string;
  clearApiKey: boolean;
  updateApiKey: boolean;
  defaultImageModel: string;
  defaultVideoModel: string;
  defaultImageParams: Record<string, unknown>;
  defaultVideoParams: Record<string, unknown>;
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

export const VIDEO_MODES: Array<{ id: VideoMode; label: string; hint: string }> = [
  { id: "text_to_video", label: "文生视频", hint: "不使用参考图" },
  { id: "image_to_video", label: "图生视频", hint: "需要 1 张参考图" },
  { id: "multi_image_video", label: "多图视频", hint: "需要 2-3 张参考图" },
  { id: "keyframes", label: "关键帧动画", hint: "2-3 张关键帧" },
];

export const MEDIA_TYPES: Array<{ id: MediaType; label: string }> = [
  { id: "image", label: "Image" },
  { id: "video", label: "Video" },
];

export const PROVIDERS: Array<{ id: ProviderID; label: string }> = [
  { id: "xai", label: "xAI Grok" },
  { id: "agnes", label: "Agnes" },
];

export const ASPECT_OPTIONS = ["", "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"];
export const RESOLUTION_OPTIONS = ["", "1k", "2k"];
export const GROK_MODEL_OPTIONS = ["grok-imagine-image-quality", "grok-imagine-image"];
export const DURATION_PRESETS: Array<{ id: string; label: string; frames: number; rate: number }> = [
  { id: "3s", label: "约 3 秒", frames: 81, rate: 24 },
  { id: "5s", label: "约 5 秒", frames: 121, rate: 24 },
  { id: "10s", label: "约 10 秒", frames: 241, rate: 24 },
  { id: "18s", label: "约 18 秒", frames: 441, rate: 24 },
];
