import type { MediaGenerationOutput, ProviderStatus } from "../types";
import type { FormEvent, ReactNode } from "react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import type { ImageAsset, ImageGenerationJob, ImagePrompt, ImageProviderSettings, ImageStatus, ImageStorageSettings, ObjectStorageProfile, Tone } from "../../app/types";
import { Button, CheckLabel, ContextList, EmptyState, Field, ImageDropInput, Notice, Panel, Pill, SubTabs } from "../../components/ui";
import { formatBytes } from "../../utils/format";
import { defaultImageSettings, defaultImageStorageSettings, formatDate, imageAssetTypeLabel, imageJobStatusLabel, imageModeLabel, imageStatusLabel, imageStorageBackendLabel } from "../../domain/labels";
import type { AppliedImagePrompt, AssetKind, AssetRef, ImageLibraryScope, ImageMode, ImagePromptDraft, ImageSettingsDraft, ImagesTab, ImageStorageSettingsDraft, MediaAsset, MediaGenerationJob, MediaMode, MediaProviderSettingsDraft, MediaType, ModelCapability, ProviderID, VideoMode } from "../types";
import { ASPECT_OPTIONS, DURATION_PRESETS, GROK_MODEL_OPTIONS, IMAGE_MODES, MEDIA_TYPES, PROVIDERS, RESOLUTION_OPTIONS, VIDEO_MODES } from "../types";

type PaginationState = {
  page: number;
  pageSize: number;
  total: number;
  pageCount: number;
  onPageChange: (page: number) => void;
  onPageSizeChange?: (pageSize: number) => void;
  pageSizeOptions?: number[];
};

function PageJumpInput({ page, pageCount, onJump }: { page: number; pageCount: number; onJump: (page: number) => void }) {
  const id = useId();
  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const form = e.currentTarget as HTMLFormElement;
    const input = form.elements.namedItem("page") as HTMLInputElement;
    const val = parseInt(input.value, 10);
    if (!isNaN(val) && val >= 1 && val <= pageCount && val !== page) {
      onJump(val);
    }
  }
  return (
    <form onSubmit={handleSubmit} className="flex items-center gap-1">
      <label htmlFor={id} className="text-xs text-[var(--muted-strong)]">
        跳至
      </label>
      <input
        id={id}
        name="page"
        type="number"
        min={1}
        max={pageCount}
        defaultValue={page}
        className="w-12 min-h-7 rounded border border-[var(--line)] bg-transparent px-1.5 py-0.5 text-center text-xs text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
      />
      <span className="text-xs text-[var(--muted-strong)]">页</span>
    </form>
  );
}

function assetRefKey(ref: AssetRef): string {
  return `${ref.kind}:${ref.id}`;
}

function sameAssetRef(a: AssetRef, b: AssetRef): boolean {
  return a.kind === b.kind && a.id === b.id;
}

function imageFilesFromClipboard(data: DataTransfer | null): File[] {
  if (!data) return [];
  const directFiles = Array.from(data.files || []).filter((file) => file.type.startsWith("image/"));
  if (directFiles.length) return directFiles.map((file, index) => normalizeClipboardImageFile(file, index));
  const itemFiles = Array.from(data.items || [])
    .filter((item) => item.kind === "file" && item.type.startsWith("image/"))
    .map((item) => item.getAsFile())
    .filter(Boolean) as File[];
  return itemFiles.map((file, index) => normalizeClipboardImageFile(file, index));
}

function normalizeClipboardImageFile(file: File, index: number): File {
  const hasUsableName = Boolean(file.name && file.name !== "image.png" && file.name !== "image");
  if (hasUsableName) return file;
  const extension = imageExtensionFromMime(file.type);
  return new File([file], `clipboard-reference-${Date.now()}-${index + 1}.${extension}`, {
    type: file.type || "image/png",
    lastModified: Date.now(),
  });
}

function imageExtensionFromMime(type: string): string {
  if (type.includes("jpeg") || type.includes("jpg")) return "jpg";
  if (type.includes("webp")) return "webp";
  if (type.includes("gif")) return "gif";
  return "png";
}

export function ImagesTabs({ active, hrefFor, onChange }: { active: ImagesTab; hrefFor?: (tab: ImagesTab) => string; onChange: (tab: ImagesTab) => void }) {
  const tabs: Array<{ id: ImagesTab; label: string }> = [
    { id: "generate", label: "生成" },
    { id: "presets", label: "生成预设" },
    { id: "library", label: "资源库" },
    { id: "history", label: "历史" },
    { id: "settings", label: "设置" },
  ];
  return <SubTabs activeId={active} onChange={(id) => onChange(id as ImagesTab)} tabs={tabs.map((tab) => ({ ...tab, href: hrefFor?.(tab.id) }))} />;
}

export function GeneratePanel({
  appliedPrompt,
  busy,
  capabilities,
  currentMediaJob,
  currentProvider,
  hasApiKey,
  keyframeRefs = [],
  latestJob,
  libraryAssets = [],
  libraryImageAssetRef,
  libraryImage,
  libraryMediaAssets = [],
  mediaJobs,
  mediaType,
  multiEditRefs = [],
  onApplyPrompt,
  onClearKeyframeRefs,
  onClearLibraryImage,
  onClearMultiEditRefs,
  onClearVideoReferenceRef,
  onRemoveKeyframeRefAtIndex,
  onRemoveMultiEditRefAtIndex,
  onMediaTypeChange,
  onOpenCurrentJobInHistory,
  onOpenPromptLibrary,
  onOpenResourceLibrary,
  onApplyReferenceRefs,
  onProviderChange,
  onResubmit,
  onSaveAsPreset,
  onUseCurrentAsReference,
  providers,
  onSubmit,
  prompts,
  providerDefaults,
  settings,
  storageSettings,
  videoReferenceRef,
}: {
  appliedPrompt?: AppliedImagePrompt;
  busy: boolean;
  capabilities: ModelCapability[];
  currentMediaJob?: MediaGenerationJob;
  currentProvider: ProviderID;
  hasApiKey: boolean;
  keyframeRefs?: AssetRef[];
  latestJob?: ImageGenerationJob;
  libraryAssets?: ImageAsset[];
  libraryImageAssetRef?: AssetRef;
  libraryImage?: ImageAsset;
  libraryMediaAssets?: MediaAsset[];
  mediaJobs: MediaGenerationJob[];
  mediaType: MediaType;
  multiEditRefs?: AssetRef[];
  onApplyPrompt?: (prompt: ImagePrompt) => void;
   onClearKeyframeRefs?: () => void;
   onClearLibraryImage?: () => void;
   onClearMultiEditRefs?: () => void;
   onClearVideoReferenceRef?: () => void;
   onRemoveKeyframeRefAtIndex?: (index: number) => void;
   onRemoveMultiEditRefAtIndex?: (index: number) => void;
  onMediaTypeChange?: (t: MediaType) => void;
  onOpenCurrentJobInHistory?: () => void;
  onOpenPromptLibrary?: () => void;
  onOpenResourceLibrary?: () => void;
  onApplyReferenceRefs?: (refs: AssetRef[], context: { mediaType: MediaType; mode: MediaMode }) => void;
  onProviderChange?: (p: ProviderID) => void;
   onResubmit?: () => void;
   onSaveAsPreset?: (form: ImagePromptFormState) => void;
   onUseCurrentAsReference?: () => void;
  providers: ProviderStatus[];
  onSubmit: (data: FormData) => Promise<void>;
  prompts?: ImagePrompt[];
  providerDefaults?: ProviderStatus;
  settings: ImageProviderSettings;
  storageSettings: ImageStorageSettings;
  videoReferenceRef?: AssetRef;
}) {
  const providerInfo = PROVIDERS.find((p) => p.id === currentProvider);
  const providerStatus = providers.find((p) => p.provider === currentProvider);
  const isAgnes = currentProvider === "agnes";
  const isImage = mediaType === "image";
  const isVideo = mediaType === "video";

  const [imageMode, setImageMode] = useState<ImageMode>("text_to_image");
  const [videoMode, setVideoMode] = useState<VideoMode>("text_to_video");
  const defaults = useMemo(() => {
    return {
      defaultModel: isVideo ? providerDefaults?.defaultVideoModel || "" : providerDefaults?.defaultImageModel || settings.defaultModel || "",
      defaultAspectRatio: settings.defaultAspectRatio,
      defaultResolution: settings.defaultResolution,
    };
  }, [isVideo, providerDefaults, settings]);
  const [promptValue, setPromptValue] = useState("");
  const [model, setModel] = useState(defaults.defaultModel);
  const [aspectRatio, setAspectRatio] = useState(defaults.defaultAspectRatio || "1:1");
  const [resolution, setResolution] = useState(defaults.defaultResolution || "1024x1024");
  const [imageCount, setImageCount] = useState(1);
  const [videoDuration, setVideoDuration] = useState(5);
  const [fps, setFps] = useState(24);
  const [seed, setSeed] = useState<number | "">("");
  const [lastAppliedPromptTitle, setLastAppliedPromptTitle] = useState("");
  const [referencePickerOpen, setReferencePickerOpen] = useState(false);
  const [referenceUploadFiles, setReferenceUploadFiles] = useState<Record<number, File>>({});
  const [referencePasteMessage, setReferencePasteMessage] = useState("");

  function resolveAssetRef(ref: AssetRef): ImageAsset | MediaAsset | undefined {
    if (ref.kind === "legacy") return libraryAssets.find((a) => a.id === ref.id);
    return libraryMediaAssets.find((a) => a.id === ref.id);
  }
  function assetURL(asset: ImageAsset | MediaAsset | undefined): string {
    if (!asset) return "";
    if ("mediaType" in asset) return mediaContentURL(asset as MediaAsset);
    return (asset as ImageAsset).downloadUrl || (asset as ImageAsset).url || "";
  }
  function assetTitleFromRef(ref: AssetRef): string {
    const a = resolveAssetRef(ref);
    return a ? ("promptPreview" in a ? (a as MediaAsset).promptPreview || (a as MediaAsset).originalFilename || a.id : assetTitle(a as ImageAsset)) : ref.id;
  }

  const slotRefs: AssetRef[] = useMemo(() => {
    if (isImage) {
      if (imageMode === "image_to_image") {
        if (multiEditRefs.length > 0) return [multiEditRefs[0]];
        if (libraryImageAssetRef) return [libraryImageAssetRef];
        return [];
      }
      if (imageMode === "multi_image_edit") return multiEditRefs;
      return [];
    }
    if (videoMode === "image_to_video" && videoReferenceRef) return [videoReferenceRef];
    if (videoMode === "keyframes") return keyframeRefs;
    if (videoMode === "multi_image_video") return keyframeRefs.length > 0 ? keyframeRefs : multiEditRefs;
    return [];
  }, [isImage, imageMode, videoMode, multiEditRefs, libraryImageAssetRef, keyframeRefs, videoReferenceRef]);

  const selectedCapability = capabilities.find((c) => c.model === model && c.mediaType === mediaType);
  const maxImageCount = selectedCapability?.parameters.maxN || (isAgnes ? 1 : 10);
  const baseReferenceSlots = isImage
    ? imageMode === "text_to_image" ? 0 : imageMode === "image_to_image" ? 1 : 3
    : videoMode === "text_to_video" ? 0 : videoMode === "image_to_video" ? 1 : videoMode === "keyframes" ? 6 : 3;
  const capabilityMaxReferences = selectedCapability?.maxReferences && selectedCapability.maxReferences > 0
    ? selectedCapability.maxReferences
    : baseReferenceSlots;
  const referenceSlots = baseReferenceSlots > 0 ? Math.min(baseReferenceSlots, capabilityMaxReferences) : 0;
  const slotMinCount = useMemo(() => {
    if (referenceSlots <= 1) return referenceSlots;
    const modelMin = selectedCapability?.minReferences && selectedCapability.minReferences > 0
      ? Math.min(selectedCapability.minReferences, referenceSlots)
      : 0;
    if (isImage && imageMode === "multi_image_edit") return Math.max(2, modelMin);
    if (isVideo && (videoMode === "keyframes" || videoMode === "multi_image_video")) return Math.max(2, modelMin);
    return Math.max(1, modelMin);
  }, [referenceSlots, selectedCapability, isImage, imageMode, isVideo, videoMode]);
  const effectiveSlotRefs = useMemo(() => slotRefs.slice(0, referenceSlots), [slotRefs, referenceSlots]);
  const referenceMode = isImage ? imageMode : videoMode;
  const referenceUploadEntries = useMemo(() => {
    return Object.entries(referenceUploadFiles)
      .map(([slot, file]) => ({ slot: Number(slot), file }))
      .filter(({ slot, file }) => Boolean(file) && slot > effectiveSlotRefs.length && slot <= referenceSlots)
      .sort((a, b) => a.slot - b.slot);
  }, [referenceUploadFiles, effectiveSlotRefs.length, referenceSlots]);
  const referenceFilledCount = effectiveSlotRefs.length + referenceUploadEntries.length;
  const selectedDurationPreset = DURATION_PRESETS.find((d) => Number(d.id.replace(/\D/g, "")) === videoDuration) || DURATION_PRESETS.find((d) => d.id === "5s");
  const videoFrameCount = selectedDurationPreset?.frames || 121;
  const videoSize = videoSizeForAspectRatio(aspectRatio);
  const providerOptions = isVideo ? providers.filter((p) => p.provider === "agnes") : providers;

  function handleSaveAsPreset() {
    onSaveAsPreset?.({
      title: "",
      description: "",
      prompt: promptValue,
      mediaType,
      mode: isImage ? imageMode : videoMode,
      model,
      aspectRatio,
      resolution,
      imageCount,
      tagsText: "",
      videoDuration: isVideo ? videoDuration : undefined,
      videoFps: isVideo ? fps : undefined,
    });
  }

  const availableModels = useMemo(() => {
    const caps = capabilities.filter((c) => c.provider === currentProvider && c.mediaType === mediaType);
    return caps.length > 0 ? caps.map((c) => ({ id: c.model, label: c.label || c.model, hint: c.supportedModes?.join(", ") || "" }))
      : currentProvider === "xai" && mediaType === "image" ? GROK_MODEL_OPTIONS.map((id) => ({ id, label: id, hint: "" })) : [];
  }, [capabilities, currentProvider, mediaType]);

  useEffect(() => {
    if ((libraryImage || libraryImageAssetRef) && isImage) setImageMode("image_to_image");
  }, [libraryImage, libraryImageAssetRef, isImage]);

  useEffect(() => {
    if (isImage) {
      if (multiEditRefs.length >= 2 && imageMode !== "multi_image_edit") setImageMode("multi_image_edit");
      else if (multiEditRefs.length === 1 && imageMode === "text_to_image") setImageMode("image_to_image");
    } else {
      if (keyframeRefs.length >= 2 && videoMode !== "keyframes") setVideoMode("keyframes");
      else if (videoReferenceRef && videoMode === "text_to_video") setVideoMode("image_to_video");
      else if (multiEditRefs.length >= 2 && videoMode === "text_to_video") setVideoMode("multi_image_video");
    }
  }, [isImage, multiEditRefs.length, keyframeRefs.length, Boolean(videoReferenceRef), imageMode, videoMode]);

  useEffect(() => {
    if (availableModels.length > 0 && !availableModels.some((m) => m.id === model)) {
      setModel(availableModels[0].id);
    }
  }, [availableModels, model]);

  useEffect(() => {
    if (imageCount > maxImageCount) setImageCount(maxImageCount);
  }, [imageCount, maxImageCount]);

  useEffect(() => {
    setReferenceUploadFiles((prev) => {
      let changed = false;
      const next: Record<number, File> = {};
      for (const [slotRaw, file] of Object.entries(prev)) {
        const slot = Number(slotRaw);
        if (slot > effectiveSlotRefs.length && slot <= referenceSlots) {
          next[slot] = file;
        } else {
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }, [effectiveSlotRefs.length, referenceSlots]);

  useEffect(() => {
    if (!referencePasteMessage) return;
    const timeout = window.setTimeout(() => setReferencePasteMessage(""), 3200);
    return () => window.clearTimeout(timeout);
  }, [referencePasteMessage]);

  useEffect(() => {
    if (referenceSlots <= 0 || referencePickerOpen) return;
    const handlePaste = (event: ClipboardEvent) => {
      if (applyReferenceClipboardData(event.clipboardData)) event.preventDefault();
    };
    document.addEventListener("paste", handlePaste);
    return () => document.removeEventListener("paste", handlePaste);
  }, [referenceSlots, referencePickerOpen, effectiveSlotRefs.length, referenceUploadFiles]);

  useEffect(() => {
    if (!appliedPrompt) return;
    const prompt = appliedPrompt.prompt;
    setPromptValue(prompt.prompt || "");
    if (prompt.mode) {
      const modeStr = String(prompt.mode);
      if (VIDEO_MODES.some((m) => m.id === modeStr)) {
        setVideoMode(modeStr as VideoMode);
      } else {
        setImageMode(normalizeImageMode(modeStr));
      }
    }
    setModel(prompt.model || defaults.defaultModel);
    setAspectRatio(prompt.aspectRatio || defaults.defaultAspectRatio || "1:1");
    setResolution(prompt.resolution || defaults.defaultResolution || "1024x1024");
    setImageCount(clampImageCount(prompt.imageCount || 1));
    setLastAppliedPromptTitle(prompt.title || "");
    const extra = (prompt as unknown as { _videoParams?: { duration?: number; fps?: number; numFrames?: number } })._videoParams;
    if (extra) {
      if (Number.isFinite(extra.duration) && extra.duration! > 0) setVideoDuration(extra.duration!);
      if (Number.isFinite(extra.fps) && extra.fps! > 0) setFps(extra.fps!);
    }
  }, [appliedPrompt, defaults]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    await onSubmit(new FormData(event.currentTarget));
  }

  function clearReferenceRefs() {
    onClearMultiEditRefs?.();
    onClearKeyframeRefs?.();
    onClearVideoReferenceRef?.();
    onClearLibraryImage?.();
    setReferenceUploadFiles({});
  }

  function removeReferenceRefAtIndex(index: number, ref: AssetRef) {
    if (isImage) {
      if (imageMode === "multi_image_edit") {
        onRemoveMultiEditRefAtIndex?.(index);
        return;
      }
      const multiIndex = multiEditRefs.findIndex((item) => sameAssetRef(item, ref));
      if (multiIndex >= 0) {
        onRemoveMultiEditRefAtIndex?.(multiIndex);
        return;
      }
      onClearLibraryImage?.();
      return;
    }

    if (videoMode === "image_to_video") {
      onClearVideoReferenceRef?.();
      return;
    }
    const keyframeIndex = keyframeRefs.findIndex((item) => sameAssetRef(item, ref));
    if (keyframeIndex >= 0) {
      onRemoveKeyframeRefAtIndex?.(keyframeIndex);
      return;
    }
    const multiIndex = multiEditRefs.findIndex((item) => sameAssetRef(item, ref));
    if (multiIndex >= 0) onRemoveMultiEditRefAtIndex?.(multiIndex);
  }

  function setReferenceUploadFile(slot: number, file?: File) {
    setReferenceUploadFiles((prev) => {
      const next = { ...prev };
      if (file) next[slot] = file;
      else delete next[slot];
      return next;
    });
  }

  function availableReferenceUploadSlots(files = referenceUploadFiles): number[] {
    const occupied = new Set(
      Object.keys(files)
        .map((slot) => Number(slot))
        .filter((slot) => slot > effectiveSlotRefs.length && slot <= referenceSlots),
    );
    const slots: number[] = [];
    for (let slot = effectiveSlotRefs.length + 1; slot <= referenceSlots; slot++) {
      if (!occupied.has(slot)) slots.push(slot);
    }
    return slots;
  }

  function applyReferenceClipboardData(data: DataTransfer | null): boolean {
    const files = imageFilesFromClipboard(data);
    if (!files.length) return false;
    const slots = availableReferenceUploadSlots();
    if (!slots.length) {
      setReferencePasteMessage("参考图槽位已满");
      return true;
    }
    const accepted = files.slice(0, slots.length);
    setReferenceUploadFiles((prev) => {
      const next = { ...prev };
      accepted.forEach((file, index) => {
        next[slots[index]] = file;
      });
      return next;
    });
    setReferencePasteMessage(
      accepted.length === files.length
        ? `已加入 ${accepted.length} 张剪贴板图片`
        : `已加入 ${accepted.length} 张剪贴板图片，剩余图片超过槽位上限`,
    );
    return true;
  }

  return (
    <>
    <div className="grid grid-cols-[minmax(0,1fr)_minmax(300px,0.85fr)] gap-4 max-xl:grid-cols-1">
      <Panel
        actions={
          <Button disabled={busy || !hasApiKey} tone="primary" type="submit" form="imagesGenerateForm">
            {busy ? "调用中" : "生成"}
          </Button>
        }
        subtitle={isAgnes ? "Agnes 多模态：图片 + 视频生成，密钥和调用记录均在本模块管理。" : "所有调用都经过后端校验、密钥边界和历史记录。"}
        title="生成任务"
      >
        <form className="grid gap-4" id="imagesGenerateForm" onSubmit={(event) => void submit(event)}>
          <input name="provider" readOnly type="hidden" value={currentProvider} />
          <input name="media_type" readOnly type="hidden" value={mediaType} />

          <div className="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
            <Field label="供应商">
              <select className="select mono" onChange={(event) => onProviderChange?.(event.target.value as ProviderID)} value={currentProvider}>
                {providerOptions.map((p) => (
                  <option key={p.provider} value={p.provider}>
                    {PROVIDERS.find((pr) => pr.id === p.provider)?.label || p.provider}
                    {p.hasApiKey ? "" : " (未配置)"}
                  </option>
                ))}
                {providerOptions.length === 0 ? (
                  <option key={isVideo ? "agnes" : "xai"} value={isVideo ? "agnes" : "xai"}>
                    {isVideo ? "Agnes" : "xAI (legacy)"}
                  </option>
                ) : null}
              </select>
            </Field>
            <Field label="生成类型">
              <fieldset className="m-0 flex gap-1 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-1">
                <legend className="sr-only">生成类型</legend>
                {MEDIA_TYPES.map((t) => {
                  const active = mediaType === t.id;
                  return (
                    <label className={`flex-1 grid gap-0.5 rounded-md px-3 py-1.5 text-center text-sm transition ${active ? "bg-[var(--surface)] shadow-sm" : "hover:bg-[var(--surface)]"}`} key={t.id}>
                      <span className="font-medium">{t.label}</span>
                      <input checked={active} className="sr-only" onChange={() => onMediaTypeChange?.(t.id as MediaType)} type="radio" value={t.id} />
                    </label>
                  );
                })}
               </fieldset>
         </Field>
       </div>

          {isImage ? (
            <fieldset className="m-0 grid grid-cols-3 gap-1 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-1 max-md:grid-cols-1">
              <legend className="sr-only">图片调用类型</legend>
              {IMAGE_MODES.map((item) => (
                <label className={`grid gap-1 rounded-md px-3 py-2 text-sm transition ${imageMode === item.id ? "bg-[var(--surface)] shadow-sm" : "hover:bg-[var(--surface)]"}`} key={item.id}>
                  <span className="font-medium">{item.label}</span>
                  <small className="muted text-xs">{item.hint}</small>
                  <input checked={imageMode === item.id} className="sr-only" name="mode" onChange={() => setImageMode(item.id)} type="radio" value={item.id} />
                </label>
              ))}
            </fieldset>
          ) : (
            <fieldset className="m-0 grid grid-cols-4 gap-1 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-1 max-md:grid-cols-2">
              <legend className="sr-only">视频调用类型</legend>
              {VIDEO_MODES.map((item) => (
                <label className={`grid gap-1 rounded-md px-3 py-2 text-sm transition ${videoMode === item.id ? "bg-[var(--surface)] shadow-sm" : "hover:bg-[var(--surface)]"}`} key={item.id}>
                  <span className="font-medium">{item.label}</span>
                  <small className="muted text-xs">{item.hint}</small>
                  <input checked={videoMode === item.id} className="sr-only" name="mode" onChange={() => setVideoMode(item.id)} type="radio" value={item.id} />
                </label>
              ))}
            </fieldset>
          )}

          <PromptInlinePicker
            busy={busy}
            currentMediaType={mediaType}
            lastAppliedTitle={lastAppliedPromptTitle}
            onApply={onApplyPrompt}
            onOpenLibrary={onOpenPromptLibrary}
            prompts={prompts || []}
          />

          <Field label="提示词" help="最多 8000 字符；详细描述主体、风格、镜头和约束。">
            <textarea className="textarea min-h-44" maxLength={8000} name="prompt" onChange={(event) => setPromptValue(event.target.value)} required value={promptValue} />
          </Field>

          <div className="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
            <div className="col-span-2 max-sm:col-span-1">
              <Field label="模型">
                <select className="select mono" name="model" onChange={(event) => setModel(event.target.value)} value={model || ""}>
                  {availableModels.map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.label}
                    </option>
                  ))}
                </select>
              </Field>
            </div>
            {isImage ? (
              <>
                <Field label="比例">
                  <select className="select mono" name="aspect_ratio" onChange={(event) => setAspectRatio(event.target.value)} value={aspectRatio}>
                    {ASPECT_OPTIONS.map((value) => (
                      <option key={value || "default"} value={value}>
                        {value || "默认"}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field label="分辨率">
                  <select className="select mono" name="resolution" onChange={(event) => setResolution(event.target.value)} value={resolution}>
                    {RESOLUTION_OPTIONS.map((value) => (
                      <option key={value || "default"} value={value}>
                        {value || "默认"}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field label="数量">
                  <input className="input mono" max={maxImageCount} min={1} name="n" onChange={(event) => setImageCount(clampImageCount(Number(event.target.value || 1), maxImageCount))} type="number" value={imageCount} />
                </Field>
                <Field label="Seed (可选)">
                  <input className="input mono" name="seed" onChange={(event) => setSeed(event.target.value === "" ? "" : Number(event.target.value))} placeholder="随机" type="number" value={seed} />
                </Field>
              </>
            ) : (
              <>
                <input name="num_frames" readOnly type="hidden" value={videoFrameCount} />
                <input name="width" readOnly type="hidden" value={videoSize.width} />
                <input name="height" readOnly type="hidden" value={videoSize.height} />
                <Field label="时长 (秒)">
                  <select className="select mono" name="duration" onChange={(event) => {
                    const preset = DURATION_PRESETS.find((d) => d.id === event.target.value);
                    setVideoDuration(Number(event.target.value.replace(/\D/g, "")) || 5);
                    if (preset) setFps(preset.rate);
                  }} value={selectedDurationPreset?.id || "5s"}>
                    {DURATION_PRESETS.map((d) => (
                      <option key={d.id} value={d.id}>
                        {d.label}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field label="帧率">
                  <select className="select mono" name="frame_rate" onChange={(event) => setFps(Number(event.target.value))} value={String(fps)}>
                    {[16, 24, 30].map((f) => (
                      <option key={f} value={f}>
                        {f} fps
                      </option>
                    ))}
                  </select>
                </Field>
                <Field label="比例">
                  <select className="select mono" name="aspect_ratio" onChange={(event) => setAspectRatio(event.target.value)} value={aspectRatio}>
                    {ASPECT_OPTIONS.map((value) => (
                      <option key={value || "default"} value={value}>
                        {value || "默认"}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field label="Seed (可选)">
                  <input className="input mono" name="seed" onChange={(event) => setSeed(event.target.value === "" ? "" : Number(event.target.value))} placeholder="随机" type="number" value={seed} />
                </Field>
              </>
            )}
          </div>

          {isImage && libraryImage && imageMode === "text_to_image" ? (
            <Notice>图片库参考图已保留，但当前文生图模式不会提交参考图；切换到图生图后会继续使用这张图片。</Notice>
          ) : null}

          {referenceSlots > 0 ? (
            <section className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
              <div className="flex items-start justify-between gap-3">
                <div>
                  <strong className="text-sm">参考图</strong>
                  <p className="muted mt-1 mb-0 text-xs">
                    {referenceSlots === 1 ? "需要 1 张" : `需要 ${slotMinCount}-${referenceSlots} 张`} · 已选 {referenceFilledCount}
                  </p>
                </div>
                <div className="flex flex-wrap items-center justify-end gap-2">
                  <Button className="min-h-7 px-2 text-xs" onClick={() => setReferencePickerOpen(true)} type="button">
                    选择图片
                  </Button>
                  {referenceFilledCount ? (
                    <Button className="min-h-7 px-2 text-xs" onClick={clearReferenceRefs} type="button">
                      清除
                    </Button>
                  ) : null}
                </div>
              </div>

              {referencePasteMessage ? (
                <div className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs text-[var(--muted-strong)]">
                  {referencePasteMessage}
                </div>
              ) : null}

              {referenceFilledCount > 0 ? (
                <div className="flex flex-wrap gap-2 rounded-md border border-[var(--line)] bg-[var(--surface)] p-2">
                  {effectiveSlotRefs.map((ref, index) => {
                    const resolvedAsset = resolveAssetRef(ref);
                    const thumb = assetURL(resolvedAsset);
                    const title = assetTitleFromRef(ref);
                    return (
                      <div className="group flex min-w-0 items-center gap-2 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-1.5" key={`${ref.kind}-${ref.id}-${index}`}>
                        <input name={`source_asset_${index + 1}`} type="hidden" value={assetRefKey(ref)} />
                        {thumb ? (
                          <img alt={title || ""} className="h-10 w-10 shrink-0 rounded border border-[var(--line)] object-cover" decoding="async" src={thumb} />
                        ) : (
                          <div className="grid h-10 w-10 shrink-0 place-items-center rounded border border-[var(--line)] text-[10px] text-[var(--muted)]">asset</div>
                        )}
                        <div className="grid min-w-0 max-w-[160px]">
                          <span className="mono text-[11px] text-[var(--muted-strong)]">src {String(index + 1).padStart(2, "0")}</span>
                          <span className="truncate text-xs">{title || ref.id.slice(0, 8)}</span>
                        </div>
                        <button className="rounded p-1 text-[var(--muted)] hover:bg-[var(--surface)] hover:text-[var(--danger)]" onClick={() => removeReferenceRefAtIndex(index, ref)} title="移除" type="button">
                          ×
                        </button>
                      </div>
                    );
                  })}
                  {referenceUploadEntries.map(({ slot, file }) => (
                    <ReferenceUploadChip
                      file={file}
                      key={`upload-${slot}-${file.name}-${file.lastModified}`}
                      onClear={() => setReferenceUploadFile(slot)}
                      slot={slot}
                    />
                  ))}
                </div>
              ) : (
                <div className="grid min-h-16 place-items-center rounded-md border border-dashed border-[var(--line)] bg-[var(--surface)] px-3 py-4 text-center">
                  <div>
                    <strong className="block text-sm">还没有参考图</strong>
                    <p className="muted mt-1 mb-0 text-xs">从图片库勾选，或展开手动上传 / URL。</p>
                  </div>
                </div>
              )}

              <details className="rounded-md border border-[var(--line)] bg-[var(--surface)]">
                <summary className="flex cursor-pointer items-center justify-between gap-2 px-3 py-2 text-xs hover:bg-[var(--surface-soft)]">
                  <span className="muted">
                    {effectiveSlotRefs.length < referenceSlots ? `手动上传或 URL · 剩余 ${Math.max(0, referenceSlots - referenceFilledCount)} 个槽位` : "手动上传或 URL · 当前槽位已满"}
                  </span>
                  <span className="muted">▾</span>
                </summary>
                <div className="grid grid-cols-3 gap-1.5 border-t border-[var(--line)] p-2 max-lg:grid-cols-2 max-sm:grid-cols-1">
                  {effectiveSlotRefs.length < referenceSlots ? (
                    Array.from({ length: referenceSlots - effectiveSlotRefs.length }, (_, offset) => {
                      const slotIndex = effectiveSlotRefs.length + offset + 1;
                      return (
                        <ReferenceSlot
                          compact
                          index={slotIndex}
                          key={`manual-slot-${slotIndex}`}
                          onClearFile={referenceUploadFiles[slotIndex] ? () => setReferenceUploadFile(slotIndex) : undefined}
                          onFiles={(files) => setReferenceUploadFile(slotIndex, files[0])}
                          uploadFile={referenceUploadFiles[slotIndex]}
                        />
                      );
                    })
                  ) : (
                    <p className="muted col-span-full m-0 px-1 py-2 text-xs">资源库参考图已占满当前模型槽位。移除一张后可手动补充 URL 或上传文件。</p>
                  )}
                </div>
              </details>
            </section>
          ) : null}

          {!hasApiKey ? (
            <Notice>
              需要先在「设置」中配置 {providerInfo?.label || currentProvider} 的密钥，才能发起模型调用。
            </Notice>
          ) : null}

          {providerStatus && providerStatus.lastError ? (
            <Notice tone="warn">
              上次连接测试失败：{providerStatus.lastError}
            </Notice>
          ) : null}
        </form>
      </Panel>

      <Panel title="本次结果" subtitle={isVideo ? "视频生成可能需要 30-120 秒，状态自动刷新。" : "最近一次生成结果会保留在这里；完整记录见历史。"}>
        {currentMediaJob ? (
          <div className="grid gap-3">
            <MediaJobCard job={currentMediaJob} />
            {currentMediaJob.status === "success" && (onOpenCurrentJobInHistory || onUseCurrentAsReference || onResubmit) ? (
              <div className="flex flex-wrap items-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2">
                <span className="muted mr-1 text-xs">下一步：</span>
                {onResubmit ? (
                  <Button className="min-h-7 px-2 text-xs" onClick={onResubmit} type="button">
                    恢复参数
                  </Button>
                ) : null}
                {currentMediaJob.mediaType === "image" && onUseCurrentAsReference ? (
                  <Button className="min-h-7 px-2 text-xs" onClick={onUseCurrentAsReference} type="button">
                    用作图生图参考
                  </Button>
                ) : null}
                {onOpenCurrentJobInHistory ? (
                  <Button className="min-h-7 px-2 text-xs" onClick={onOpenCurrentJobInHistory} type="button">
                    打开历史详情
                  </Button>
                ) : null}
                <Button className="min-h-7 px-2 text-xs" onClick={handleSaveAsPreset} type="button">
                  保存为生成预设
                </Button>
              </div>
            ) : null}
          </div>
        ) : latestJob ? (
          <div className="grid gap-3">
            <JobCard job={latestJob} />
            {latestJob.status === "success" && (onOpenCurrentJobInHistory || onUseCurrentAsReference || onResubmit) ? (
              <div className="flex flex-wrap items-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2">
                <span className="muted mr-1 text-xs">下一步：</span>
                {onResubmit ? (
                  <Button className="min-h-7 px-2 text-xs" onClick={onResubmit} type="button">
                    恢复参数
                  </Button>
                ) : null}
                {onUseCurrentAsReference ? (
                  <Button className="min-h-7 px-2 text-xs" onClick={onUseCurrentAsReference} type="button">
                    用作图生图参考
                  </Button>
                ) : null}
                {onOpenCurrentJobInHistory ? (
                  <Button className="min-h-7 px-2 text-xs" onClick={onOpenCurrentJobInHistory} type="button">
                    打开历史详情
                  </Button>
                ) : null}
                <Button className="min-h-7 px-2 text-xs" onClick={handleSaveAsPreset} type="button">
                  保存为生成预设
                </Button>
              </div>
            ) : null}
          </div>
        ) : (
          <EmptyState title="等待生成" body={`选择 provider 和 ${isImage ? "图片模式" : "视频模式"}，填写 prompt 后创建任务。`} />
        )}
        {mediaJobs.length > 0 ? (
          <div className="mt-3 border-t border-[var(--line)] pt-3">
            <p className="muted mb-2 text-xs">本次会话其他任务</p>
            <ul className="grid gap-2 max-h-60 overflow-y-auto">
              {mediaJobs.slice(0, 8).filter((j) => j.id !== currentMediaJob?.id).map((j) => (
                <MediaJobMini key={j.id} job={j} />
              ))}
            </ul>
          </div>
        ) : null}
      </Panel>
    </div>
    <ReferenceLibraryDrawer
      assets={libraryAssets}
      maxCount={referenceSlots}
      mediaAssets={libraryMediaAssets}
      mediaType={mediaType}
      minCount={slotMinCount}
      mode={referenceMode}
      model={model}
      onApply={(refs) => {
        onApplyReferenceRefs?.(refs, { mediaType, mode: referenceMode });
        setReferencePickerOpen(false);
      }}
      onClose={() => setReferencePickerOpen(false)}
      onOpenLibrary={onOpenResourceLibrary}
      open={referencePickerOpen}
      selectedRefs={effectiveSlotRefs}
    />
    </>
  );
}

function PromptInlinePicker({
  busy,
  currentMediaType,
  lastAppliedTitle,
  onApply,
  onOpenLibrary,
  prompts,
}: {
  busy: boolean;
  currentMediaType?: MediaType;
  lastAppliedTitle?: string;
  onApply?: (prompt: ImagePrompt) => void;
  onOpenLibrary?: () => void;
  prompts: ImagePrompt[];
}) {
  const [selectedId, setSelectedId] = useState("");
  const allActive = prompts.filter((prompt) => prompt.status !== "deleted");
  const activePrompts = currentMediaType
    ? allActive.filter((prompt) => VIDEO_MODES.some((m) => m.id === String(prompt.mode || ""))
        ? currentMediaType === "video"
        : currentMediaType === "image")
    : allActive;
  const filtered = activePrompts.length ? activePrompts : allActive;
  const selected = activePrompts.find((prompt) => prompt.id === selectedId) || filtered[0];

  useEffect(() => {
    if (!selectedId && activePrompts[0]?.id) setSelectedId(activePrompts[0].id);
    if (selectedId && !activePrompts.some((prompt) => prompt.id === selectedId)) setSelectedId(activePrompts[0]?.id || "");
  }, [activePrompts, selectedId]);

  if (!activePrompts.length) {
    return (
      <div className="flex items-center justify-between gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 max-sm:grid">
        <div>
          <strong className="text-sm">生成预设</strong>
          <p className="muted mt-1 mb-0 text-xs">保存常用的生成参数：图片提示词、视频提示词、模型预设等。</p>
        </div>
        <Button onClick={onOpenLibrary} type="button">
          新建生成预设
        </Button>
      </div>
    );
  }

  return (
    <div className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <div className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-end gap-2 max-md:grid-cols-1">
         <Field label="生成预设">
           <select className="select" onChange={(event) => setSelectedId(event.target.value)} value={selected?.id || ""}>
            {activePrompts.map((prompt) => (
              <option key={prompt.id} value={prompt.id}>
                {prompt.title}
              </option>
            ))}
          </select>
        </Field>
        <Button disabled={busy || !selected} onClick={() => selected && onApply?.(selected)} type="button">
          带入
        </Button>
        <Button onClick={onOpenLibrary} type="button">
          管理
        </Button>
      </div>
      <div className="muted flex flex-wrap gap-2 text-xs">
        {selected?.mode ? (() => {
          const mt: MediaType = VIDEO_MODES.some((m) => m.id === String(selected.mode)) ? "video" : "image";
          return <Pill>{mediaModeLabel(selected.mode, mt)}</Pill>;
        })() : null}
        {selected?.model ? <span className="mono">{selected.model}</span> : null}
        {lastAppliedTitle ? <span>已带入：{lastAppliedTitle}</span> : null}
      </div>
    </div>
  );
}

function ReferenceLibraryDrawer({
  assets,
  maxCount,
  mediaAssets,
  mediaType,
  minCount,
  mode,
  model,
  onApply,
  onClose,
  onOpenLibrary,
  open,
  selectedRefs,
}: {
  assets: ImageAsset[];
  maxCount: number;
  mediaAssets: MediaAsset[];
  mediaType: MediaType;
  minCount: number;
  mode: MediaMode;
  model: string;
  onApply: (refs: AssetRef[]) => void;
  onClose: () => void;
  onOpenLibrary?: () => void;
  open: boolean;
  selectedRefs: AssetRef[];
}) {
  const firstFieldRef = useRef<HTMLInputElement | null>(null);
  const previousActiveRef = useRef<HTMLElement | null>(null);
  const [query, setQuery] = useState("");
  const [providerFilter, setProviderFilter] = useState<"all" | ProviderID>("all");
  const [storageFilter, setStorageFilter] = useState<"all" | "local" | "s3" | "remote">("all");
  const [sourceFilter, setSourceFilter] = useState<"all" | "generated" | "upload" | "source">("all");
  const [privacyFilter, setPrivacyFilter] = useState<"all" | "private" | "public">("all");
  const [sortOrder, setSortOrder] = useState<"newest" | "oldest" | "size">("newest");
  const [draftRefs, setDraftRefs] = useState<AssetRef[]>([]);

  useEffect(() => {
    if (!open) return;
    setDraftRefs(uniqueAssetRefs(selectedRefs).slice(0, maxCount));
  }, [open, selectedRefs, maxCount]);

  useEffect(() => {
    if (!open) return;
    previousActiveRef.current = document.activeElement as HTMLElement | null;
    const raf = requestAnimationFrame(() => firstFieldRef.current?.focus());
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key !== "Tab") return;
      const drawer = document.querySelector<HTMLElement>("[data-drawer='reference-library']");
      if (!drawer) return;
      const focusables = drawer.querySelectorAll<HTMLElement>("a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex='-1'])");
      if (!focusables.length) return;
      const first = focusables[0];
      const last = focusables[focusables.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      cancelAnimationFrame(raf);
      document.removeEventListener("keydown", handleKeyDown);
      previousActiveRef.current?.focus?.();
    };
  }, [open, onClose]);

  const imageItems = useMemo<AnyAsset[]>(() => {
    const legacy: AnyAsset[] = (assets || [])
      .filter((asset) => !asset.deletedAt)
      .map((asset) => ({ kind: "legacy", data: asset }));
    const media: AnyAsset[] = (mediaAssets || [])
      .filter((asset) => asset.mediaType === "image" && !asset.deletedAt && asset.status !== "failed")
      .map((asset) => ({ kind: "media", data: asset }));
    return [...legacy, ...media];
  }, [assets, mediaAssets]);

  const filteredItems = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const list = imageItems.filter((item) => {
      if (needle && !anyAssetSearchText(item).includes(needle)) return false;
      if (providerFilter !== "all" && anyAssetProvider(item) !== providerFilter) return false;
      if (storageFilter !== "all") {
        if (storageFilter === "local" && !anyAssetIsLocal(item)) return false;
        if (storageFilter === "s3" && !anyAssetIsS3(item)) return false;
        if (storageFilter === "remote" && !anyAssetIsRemote(item)) return false;
      }
      if (sourceFilter === "generated" && !anyAssetIsGenerated(item)) return false;
      if (sourceFilter === "upload" && !anyAssetIsUpload(item)) return false;
      if (sourceFilter === "source" && !anyAssetIsSource(item)) return false;
      if (privacyFilter === "private" && !anyAssetIsPrivate(item)) return false;
      if (privacyFilter === "public" && anyAssetIsPrivate(item)) return false;
      return true;
    });
    return [...list].sort((a, b) => {
      if (sortOrder === "oldest") return anyAssetCreatedAt(a).localeCompare(anyAssetCreatedAt(b));
      if (sortOrder === "size") return anyAssetSizeBytes(b) - anyAssetSizeBytes(a);
      return anyAssetCreatedAt(b).localeCompare(anyAssetCreatedAt(a));
    });
  }, [imageItems, query, providerFilter, storageFilter, sourceFilter, privacyFilter, sortOrder]);

  const selectedKeys = useMemo(() => new Set(draftRefs.map(assetRefKey)), [draftRefs]);
  const canApply = draftRefs.length >= minCount && draftRefs.length <= maxCount;
  const selectionHint = draftRefs.length < minCount
    ? `还需要 ${minCount - draftRefs.length} 张`
    : draftRefs.length > maxCount
      ? `超出 ${draftRefs.length - maxCount} 张`
      : "可应用";

  function toggleRef(ref: AssetRef) {
    setDraftRefs((prev) => {
      const exists = prev.some((item) => sameAssetRef(item, ref));
      if (exists) return prev.filter((item) => !sameAssetRef(item, ref));
      if (maxCount <= 1) return [ref];
      if (prev.length >= maxCount) return prev;
      return [...prev, ref];
    });
  }

  if (!open) return null;

  return (
    <div aria-hidden={false} className="fixed inset-0 z-50">
      <div aria-label="关闭参考图选择面板" className="absolute inset-0 bg-black/30" onClick={onClose} role="presentation" />
      <aside
        aria-describedby="reference-library-subtitle"
        aria-label="选择参考图"
        aria-modal="true"
        className="absolute right-0 top-0 flex h-full w-full max-w-3xl flex-col border-l border-[var(--line)] bg-[var(--bg)] shadow-2xl"
        data-drawer="reference-library"
        role="dialog"
      >
        <div className="flex items-start justify-between gap-3 border-b border-[var(--line)] p-4">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="m-0 text-sm font-semibold">选择参考图</h3>
              <Pill>{mediaModeLabel(mode, mediaType)}</Pill>
              <Pill>{draftRefs.length}/{maxCount}</Pill>
            </div>
            <p className="muted mt-1 mb-0 min-w-0 text-xs" id="reference-library-subtitle">
              {model || "默认模型"} · {maxCount === 1 ? "单张参考图" : `模型允许 ${minCount}-${maxCount} 张参考图`}
            </p>
          </div>
          <Button onClick={onClose}>关闭</Button>
        </div>

        <div className="flex-1 overflow-y-auto p-4">
          <div className="grid gap-3">
            <div className="grid grid-cols-[minmax(0,1fr)_160px_140px] gap-2 max-lg:grid-cols-1">
              <Field label="过滤">
                <input
                  autoComplete="off"
                  className="input"
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="搜索 prompt、文件名、模型或 job id"
                  ref={firstFieldRef}
                  value={query}
                />
              </Field>
              <Field label="供应商">
                <select className="select mono" onChange={(event) => setProviderFilter(event.target.value as "all" | ProviderID)} value={providerFilter}>
                  <option value="all">全部</option>
                  {PROVIDERS.map((provider) => (
                    <option key={provider.id} value={provider.id}>{provider.label}</option>
                  ))}
                </select>
              </Field>
              <Field label="排序">
                <select className="select mono" onChange={(event) => setSortOrder(event.target.value as "newest" | "oldest" | "size")} value={sortOrder}>
                  <option value="newest">最新</option>
                  <option value="oldest">最早</option>
                  <option value="size">大小</option>
                </select>
              </Field>
            </div>

            <div className="grid grid-cols-3 gap-2 max-lg:grid-cols-1">
              <Field label="存储">
                <select className="select mono" onChange={(event) => setStorageFilter(event.target.value as "all" | "local" | "s3" | "remote")} value={storageFilter}>
                  <option value="all">全部</option>
                  <option value="local">本地</option>
                  <option value="s3">对象存储</option>
                  <option value="remote">远程 URL</option>
                </select>
              </Field>
              <Field label="来源">
                <select className="select mono" onChange={(event) => setSourceFilter(event.target.value as "all" | "generated" | "upload" | "source")} value={sourceFilter}>
                  <option value="all">全部</option>
                  <option value="generated">生成结果</option>
                  <option value="upload">上传</option>
                  <option value="source">参考源图</option>
                </select>
              </Field>
              <Field label="可见性">
                <select className="select mono" onChange={(event) => setPrivacyFilter(event.target.value as "all" | "private" | "public")} value={privacyFilter}>
                  <option value="all">全部</option>
                  <option value="public">公开库</option>
                  <option value="private">私密库</option>
                </select>
              </Field>
            </div>

            <div className="flex items-center justify-between gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-xs">
              <span className="muted">匹配 {filteredItems.length} 张图片</span>
              <span className={canApply ? "text-[var(--good)]" : "text-[var(--warn)]"}>{selectionHint}</span>
            </div>

            {filteredItems.length ? (
              <div className="grid gap-2">
                {filteredItems.map((item) => {
                  const ref: AssetRef = { kind: item.kind, id: anyAssetId(item) };
                  const key = assetRefKey(ref);
                  const checked = selectedKeys.has(key);
                  const disabled = !checked && draftRefs.length >= maxCount;
                  const title = referenceAnyAssetTitle(item);
                  const thumb = referenceAnyAssetURL(item);
                  const meta = [
                    anyAssetProvider(item) || "unknown",
                    imageStorageBackendLabel(anyAssetStorage(item)),
                    anyAssetSizeBytes(item) ? formatBytes(anyAssetSizeBytes(item)) : "",
                    anyAssetCreatedAt(item) ? formatDate(anyAssetCreatedAt(item)) : "",
                  ].filter(Boolean).join(" · ");
                  return (
                    <label
                      className={`grid cursor-pointer grid-cols-[auto_72px_minmax(0,1fr)] items-center gap-3 rounded-lg border p-2 text-left transition ${checked ? "border-[var(--accent)] bg-[var(--accent-soft)]" : "border-[var(--line)] bg-[var(--surface)] hover:bg-[var(--surface-soft)]"} ${disabled ? "opacity-55" : ""}`}
                      key={key}
                    >
                      <input
                        checked={checked}
                        className="h-4 w-4 accent-[var(--accent)]"
                        disabled={disabled}
                        onChange={() => toggleRef(ref)}
                        type="checkbox"
                      />
                      {thumb ? (
                        <img alt={title || ""} className="aspect-square h-[72px] w-[72px] rounded-md border border-[var(--line)] object-cover" decoding="async" loading="lazy" src={thumb} />
                      ) : (
                        <div className="grid aspect-square h-[72px] w-[72px] place-items-center rounded-md border border-[var(--line)] text-xs text-[var(--muted)]">image</div>
                      )}
                      <div className="grid min-w-0 gap-1">
                        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                          <span className="truncate text-sm font-medium">{title || anyAssetId(item)}</span>
                          {anyAssetIsPrivate(item) ? <Pill tone="warn">私密</Pill> : null}
                          <Pill>{referenceAnyAssetSourceLabel(item)}</Pill>
                        </div>
                        <p className="muted m-0 truncate text-xs">{meta || anyAssetId(item)}</p>
                        <p className="mono muted m-0 truncate text-[11px]">{item.kind}:{anyAssetId(item)}</p>
                      </div>
                    </label>
                  );
                })}
              </div>
            ) : (
              <EmptyState title="没有匹配图片" body="调整过滤条件，或进入资源库上传/管理图片资产。" />
            )}
          </div>
        </div>

        <div className="flex items-center justify-between gap-3 border-t border-[var(--line)] p-4">
          <div className="grid gap-1 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-xs">
            <div>已选 {draftRefs.length} / {maxCount}</div>
            <div className="muted">{selectionHint}</div>
          </div>
          <div className="flex flex-wrap justify-end gap-2">
            {onOpenLibrary ? (
              <Button onClick={() => {
                onClose();
                onOpenLibrary();
              }} type="button">
                打开资源库
              </Button>
            ) : null}
            <Button onClick={onClose} type="button">取消</Button>
            <Button disabled={!canApply} onClick={() => onApply(draftRefs)} tone="primary" type="button">
              应用参考图
            </Button>
          </div>
        </div>
      </aside>
    </div>
  );
}

function ReferenceUploadChip({ file, onClear, slot }: { file: File; onClear: () => void; slot: number }) {
  const [previewURL, setPreviewURL] = useState("");

  useEffect(() => {
    const url = URL.createObjectURL(file);
    setPreviewURL(url);
    return () => URL.revokeObjectURL(url);
  }, [file]);

  return (
    <div className="group flex min-w-0 items-center gap-2 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-1.5">
      {previewURL ? (
        <img alt={file.name || "upload"} className="h-10 w-10 shrink-0 rounded border border-[var(--line)] object-cover" decoding="async" src={previewURL} />
      ) : (
        <div className="grid h-10 w-10 shrink-0 place-items-center rounded border border-[var(--line)] text-[10px] text-[var(--muted)]">file</div>
      )}
      <div className="grid min-w-0 max-w-[160px]">
        <span className="mono text-[11px] text-[var(--muted-strong)]">src {String(slot).padStart(2, "0")} · upload</span>
        <span className="truncate text-xs">{file.name || "clipboard image"}</span>
        <span className="muted text-[11px]">{formatBytes(file.size)}</span>
      </div>
      <button className="rounded p-1 text-[var(--muted)] hover:bg-[var(--surface)] hover:text-[var(--danger)]" onClick={onClear} title="移除" type="button">
        ×
      </button>
    </div>
  );
}

function ReferenceSlot({
  compact,
  index,
  displayAsset,
  assetId,
  onClear,
  onClearFile,
  onFiles,
  uploadFile,
}: {
  compact?: boolean;
  index: number;
  displayAsset?: ImageAsset | MediaAsset;
  assetId?: string;
  onClear?: () => void;
  onClearFile?: () => void;
  onFiles?: (files: File[]) => void;
  uploadFile?: File;
}) {
  const hasAsset = Boolean(displayAsset || assetId);
  const hasUploadFile = Boolean(uploadFile);
  const asset = displayAsset;
  const assetUrl = asset ? ("downloadUrl" in asset ? (asset.downloadUrl || asset.url || "") : (asset as ImageAsset).url || "") : "";
  const assetH = asset ? ("height" in asset ? (asset as MediaAsset).height || (asset as ImageAsset).height || 512 : 512) : 512;
  const assetW = asset ? ("width" in asset ? (asset as MediaAsset).width || (asset as ImageAsset).width || 512 : 512) : 512;
  const titleText = asset
    ? ("originalFilename" in asset && (asset as MediaAsset).originalFilename) || (asset as MediaAsset).promptPreview || ("revisedPromptPreview" in asset && (asset as ImageAsset).revisedPromptPreview) || ("promptPreview" in asset && (asset as ImageAsset).promptPreview) || asset.id
    : "";
  return (
    <div className={compact ? "grid min-h-28 content-start gap-1.5 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-2" : "grid min-h-44 content-start gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3"}>
      <strong className={compact ? "mono text-[10px] text-[var(--muted-strong)]" : "mono text-xs text-[var(--muted-strong)]"}>source {String(index).padStart(2, "0")}</strong>
      {hasAsset ? (
        <div className="grid gap-2 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-2">
          {assetUrl ? <img alt={titleText || "reference"} className="aspect-video w-full rounded border border-[var(--line)] object-cover" decoding="async" height={assetH} loading="lazy" src={assetUrl} width={assetW} /> : <div className="grid aspect-video place-items-center rounded border border-[var(--line)] text-xs text-[var(--muted)]">asset loaded</div>}
          {assetId ? <input name={`source_asset_${index}`} type="hidden" value={assetId} /> : null}
          <div className="flex items-center justify-between gap-2">
            <span className="min-w-0 truncate text-xs font-medium">{titleText || assetId || "参考图"}</span>
            {onClear ? (
              <Button className={compact ? "min-h-6 px-1.5 text-[11px]" : "min-h-7 px-2 text-xs"} onClick={onClear} type="button">
                清除
              </Button>
            ) : null}
          </div>
        </div>
      ) : null}
      {hasUploadFile ? (
        <div className="flex items-center justify-between gap-2 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] px-2 py-1.5">
          <span className="min-w-0 truncate text-xs">{uploadFile?.name || "clipboard image"}</span>
          {onClearFile ? (
            <Button className="min-h-6 px-1.5 text-[11px]" onClick={onClearFile} type="button">
              移除
            </Button>
          ) : null}
        </div>
      ) : null}
      <Field label="URL">
        <input autoComplete="off" className="input mono" disabled={hasAsset || hasUploadFile} name={`image_url_${index}`} placeholder="https://example.com/image.png" spellCheck={false} type="url" />
      </Field>
      <Field label="上传">
        <ImageDropInput
          disabled={hasAsset}
          file={uploadFile}
          hint={compact ? "点击或拖拽图片" : "点击选择，或拖拽图片到这里"}
          label="上传参考图"
          name={`image_file_${index}`}
          onFiles={onFiles}
        />
      </Field>
    </div>
  );
}

function PaginationFooter({ pagination, visibleCount }: { pagination?: PaginationState; visibleCount: number }) {
  if (!pagination || pagination.total <= pagination.pageSize && pagination.page <= 1) return null;
  const pageCount = Math.max(1, pagination.pageCount || Math.ceil(pagination.total / Math.max(1, pagination.pageSize)));
  const page = Math.min(Math.max(1, pagination.page), pageCount);
  const { pageSizeOptions, onPageSizeChange, pageSize } = pagination;
  return (
    <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-[var(--line)] pt-3 text-xs text-[var(--muted-strong)]">
      <span>
        第 <span className="mono">{page}</span> / <span className="mono">{pageCount}</span> 页
        <span className="mx-2">·</span>
        本页 <span className="mono">{visibleCount}</span> 项
        <span className="mx-2">·</span>
        总量 <span className="mono">{pagination.total}</span>
      </span>
      <div className="flex flex-wrap items-center gap-2">
        <Button className="min-h-7 px-2 text-xs" disabled={page <= 1} onClick={() => pagination.onPageChange(1)} type="button">
          « 首页
        </Button>
        <Button className="min-h-7 px-2 text-xs" disabled={page <= 1} onClick={() => pagination.onPageChange(page - 1)} type="button">
          上一页
        </Button>
        <PageJumpInput page={page} pageCount={pageCount} onJump={(p) => pagination.onPageChange(p)} />
        <Button className="min-h-7 px-2 text-xs" disabled={page >= pageCount} onClick={() => pagination.onPageChange(page + 1)} type="button">
          下一页
        </Button>
        <Button className="min-h-7 px-2 text-xs" disabled={page >= pageCount} onClick={() => pagination.onPageChange(pageCount)} type="button">
          末页 »
        </Button>
        {pageSizeOptions && onPageSizeChange && (
          <select
            value={pageSize}
            onChange={(e) => onPageSizeChange(parseInt(e.target.value, 10))}
            className="min-h-7 rounded border border-[var(--line)] bg-transparent px-1.5 text-xs text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
          >
            {pageSizeOptions.map((n) => (
              <option key={n} value={n}>
                {n} 条/页
              </option>
            ))}
          </select>
        )}
      </div>
    </div>
  );
}

export function HistoryPanel({
  jobs,
  libraryMediaAssets,
  mediaJobs,
  mediaType,
  onMediaTypeChange,
  onCopyJobParams,
  onOpenAsset,
  onProviderChange,
  pagination,
  onRefresh,
  onRetryJob,
  onRestoreJob,
  onSaveJobAsPreset,
  onUseAssetAsReference,
  onUseLegacyOutputAsReference,
  targetJobId,
  targetJobKind,
  onJobScrolled,
  onOpenJobLogs,
}: {
  jobs: ImageGenerationJob[];
  libraryMediaAssets?: MediaAsset[];
  mediaJobs?: MediaGenerationJob[];
  mediaType?: MediaType;
  onMediaTypeChange?: (t: MediaType) => void;
  onCopyJobParams?: (kind: "legacy" | "media", job: unknown) => void;
  onOpenAsset?: (assetId: string) => void;
  onProviderChange?: (p: ProviderID) => void;
  pagination?: PaginationState;
  onRefresh: () => Promise<void>;
  onRetryJob?: (kind: "legacy" | "media", job: unknown) => void;
  onRestoreJob?: (kind: "legacy" | "media", job: unknown) => void;
  onSaveJobAsPreset?: (kind: "legacy" | "media", job: unknown) => void;
  onUseAssetAsReference?: (asset: MediaAsset) => void;
  onUseLegacyOutputAsReference?: (assetId: string, url?: string) => void;
  targetJobId?: string;
  targetJobKind?: "legacy" | "media";
  onJobScrolled?: () => void;
  onOpenJobLogs?: (kind: "legacy" | "media", job: unknown) => void;
}) {
  const scrolledRef = useRef(false);
  const [showAllTypes, setShowAllTypes] = useState(true);
  const [providerFilter, setProviderFilter] = useState<ProviderID | "all">("all");
  const [modeFilter, setModeFilter] = useState<string>("all");
  const [statusFilter, setStatusFilter] = useState<string>("all");
  const allJobs = useMemo(() => {
    const legacy = (jobs || []).map((j) => ({ kind: "legacy" as const, id: j.id, createdAt: j.createdAt, job: j as ImageGenerationJob | MediaGenerationJob }));
    const media = (mediaJobs || []).map((j) => ({ kind: "media" as const, id: j.id, createdAt: j.createdAt, job: j as ImageGenerationJob | MediaGenerationJob }));
    return [...legacy, ...media].sort((a, b) => (b.createdAt || "").localeCompare(a.createdAt || ""));
  }, [jobs, mediaJobs]);

  const filteredJobs = useMemo(() => {
    return allJobs.filter((row) => {
      let status: string;
      let mode: string;
      if (row.kind === "legacy") {
        const lj = row.job as ImageGenerationJob;
        if (!showAllTypes && mediaType && mediaType !== "image") return false;
        if (providerFilter !== "all" && providerFilter !== "xai") return false;
        status = lj.status || "";
        mode = String(lj.mode || "text_to_image");
      } else {
        const mj = row.job as MediaGenerationJob;
        if (!showAllTypes && mediaType && mj.mediaType !== mediaType) return false;
        if (providerFilter !== "all" && mj.provider !== providerFilter) return false;
        status = mj.status || "";
        mode = mj.mode || (mj.mediaType === "video" ? "text_to_video" : "text_to_image");
      }
      if (modeFilter !== "all" && mode !== modeFilter) return false;
      if (statusFilter !== "all" && status !== statusFilter) return false;
      return true;
    });
  }, [allJobs, mediaType, providerFilter, modeFilter, statusFilter, showAllTypes]);

  useEffect(() => {
    if (!targetJobId || !targetJobKind || scrolledRef.current) return;
    const el = document.querySelector(`[data-job-id="${targetJobKind}-${targetJobId}"]`) as HTMLElement | null;
    if (el) {
      scrolledRef.current = true;
      el.scrollIntoView({ behavior: "smooth", block: "center" });
      window.setTimeout(() => onJobScrolled?.(), 700);
    } else {
      scrolledRef.current = true;
      onJobScrolled?.();
    }
    return () => {
      scrolledRef.current = false;
    };
  }, [targetJobId, targetJobKind, onJobScrolled, filteredJobs]);

  return (
    <Panel actions={<Button onClick={() => void onRefresh()}>刷新</Button>} subtitle="成功和失败的调用都会保留，便于追踪模型参数与上游错误。" title="历史记录">
      <div className="mb-3 grid grid-cols-2 gap-3 max-sm:grid-cols-1">
        <Field label="生成类型">
          <fieldset className="m-0 flex gap-1 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-1">
            <legend className="sr-only">生成类型</legend>
            {[{ id: "all", label: "全部" }, ...MEDIA_TYPES].map((t) => {
              const active = t.id === "all" ? showAllTypes : (!showAllTypes && mediaType === t.id);
              return (
                <label key={t.id} className={`flex-1 grid gap-0.5 rounded-md px-3 py-1.5 text-center text-sm transition ${active ? "bg-[var(--surface)] shadow-sm" : "hover:bg-[var(--surface)]"}`}>
                  <span className="font-medium">{t.label}</span>
                  <input checked={active} className="sr-only" onChange={() => {
                    if (t.id === "all") {
                      setShowAllTypes(true);
                    } else {
                      setShowAllTypes(false);
                      onMediaTypeChange?.(t.id as MediaType);
                    }
                  }} type="radio" value={t.id} />
                </label>
              );
            })}
          </fieldset>
        </Field>
         <Field label="供应商">
           <select className="select mono" onChange={(event) => {
            const next = event.target.value as ProviderID | "all";
            setProviderFilter(next);
            if (next !== "all") onProviderChange?.(next);
          }} value={providerFilter}>
            <option value="all">全部</option>
            {PROVIDERS.map((p) => (
              <option key={p.id} value={p.id}>{p.label}</option>
            ))}
          </select>
        </Field>
      </div>
      {filteredJobs.length ? (
        <div className="grid gap-3">
          {filteredJobs.map((row) => (
              row.kind === "legacy" ? (
                <JobCard
                  job={row.job as ImageGenerationJob}
                  key={row.id}
                  onCopyParams={onCopyJobParams ? (j) => onCopyJobParams("legacy", j) : undefined}
                  onRetry={onRetryJob ? (j) => onRetryJob("legacy", j) : undefined}
                  onRestore={onRestoreJob ? (j) => onRestoreJob("legacy", j) : undefined}
                  onSaveAsPreset={onSaveJobAsPreset ? (j) => onSaveJobAsPreset("legacy", j) : undefined}
                  onUseOutputAsReference={onUseLegacyOutputAsReference}
                  targetJobId={targetJobId}
                  targetJobKind={targetJobKind}
                  onOpenLogs={onOpenJobLogs ? (j) => onOpenJobLogs("legacy", j) : undefined}
                />
              ) : (
                <MediaJobCard
                  job={row.job as MediaGenerationJob}
                  key={row.id}
                  libraryMediaAssets={libraryMediaAssets}
                  onCopyParams={onCopyJobParams ? (j) => onCopyJobParams("media", j) : undefined}
                  onOpenAsset={onOpenAsset}
                  onRetry={onRetryJob ? (j) => onRetryJob("media", j) : undefined}
                  onRestore={onRestoreJob ? (j) => onRestoreJob("media", j) : undefined}
                  onSaveAsPreset={onSaveJobAsPreset ? (j) => onSaveJobAsPreset("media", j) : undefined}
                  onUseAssetAsReference={onUseAssetAsReference}
                  targetJobId={targetJobId}
                  targetJobKind={targetJobKind}
                  onOpenLogs={onOpenJobLogs ? (j) => onOpenJobLogs("media", j) : undefined}
               />
            )
          ))}
        </div>
      ) : (
        <EmptyState title="暂无历史" body="生成或编辑图片/视频后，这里会展示调用记录。" />
      )}
      <PaginationFooter pagination={pagination} visibleCount={filteredJobs.length} />
    </Panel>
  );
}

export type ImagePromptFormState = Omit<ImagePromptDraft, "tags" | "mode"> & { tagsText: string; mediaType: MediaType; mode: string; videoDuration?: number; videoFps?: number; _source?: string };

export function PromptLibraryPanel({
  busy,
  onCreate,
  onDelete,
  onExternalDraftConsumed,
  externalDraft,
  onRefresh,
  onSelect,
  onUpdate,
  onUse,
  prompts,
  selectedId,
  settings,
}: {
  busy: string;
  onCreate: (draft: ImagePromptDraft) => Promise<ImagePrompt | undefined>;
  onDelete: (prompt: ImagePrompt) => void;
  onExternalDraftConsumed?: () => void;
  externalDraft?: ImagePromptFormState;
  onRefresh: () => Promise<void>;
  onSelect: (prompt: ImagePrompt) => void;
  onUpdate: (id: string, draft: ImagePromptDraft) => Promise<ImagePrompt | undefined>;
  onUse: (prompt: ImagePrompt) => void;
  prompts: ImagePrompt[];
  selectedId?: string;
  settings: ImageProviderSettings;
}) {
  const [query, setQuery] = useState("");
  const [mediaTypeFilter, setMediaTypeFilter] = useState<"all" | MediaType>("all");
  const [modeFilter, setModeFilter] = useState("all");
  const [editing, setEditing] = useState(false);
  const [creating, setCreating] = useState(false);
  const selected = prompts.find((prompt) => prompt.id === selectedId) || prompts[0];
  const [draft, setDraft] = useState<ImagePromptFormState>(() => promptFormFromPrompt(selected, settings));
  const filtered = useMemo(() => filterPrompts(prompts, query, mediaTypeFilter, modeFilter), [prompts, query, mediaTypeFilter, modeFilter]);
  const saving = busy === "prompt-save" || Boolean(selected && busy === `prompt:${selected.id}`);

  useEffect(() => {
    if (!externalDraft) return;
    setDraft(externalDraft);
    setCreating(true);
    setEditing(true);
    onExternalDraftConsumed?.();
  }, [externalDraft, onExternalDraftConsumed]);

  useEffect(() => {
    if (editing || creating) return;
    setDraft(promptFormFromPrompt(selected, settings));
  }, [creating, editing, selected, settings]);

  function startCreate() {
    setCreating(true);
    setEditing(true);
    setDraft(promptFormFromPrompt(undefined, settings));
  }

  function startEdit(prompt: ImagePrompt) {
    onSelect(prompt);
    setCreating(false);
    setEditing(true);
    setDraft(promptFormFromPrompt(prompt, settings));
  }

  function cancelEdit() {
    setCreating(false);
    setEditing(false);
    setDraft(promptFormFromPrompt(selected, settings));
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const payload = promptDraftFromForm(draft);
    const result = creating ? await onCreate(payload) : selected ? await onUpdate(selected.id, payload) : undefined;
    if (!result) return;
    setCreating(false);
    setEditing(false);
    onSelect(result);
  }

  return (
    <Panel
      actions={
        <>
          <Button onClick={() => void onRefresh()}>刷新</Button>
          <Button onClick={startCreate} tone="primary">
            新建生成预设
          </Button>
        </>
      }
      subtitle="保存常用的生成参数组合，包括图片提示词、视频提示词、模型和模式。一键带入生成任务。"
      title="生成预设"
    >
      <div className="grid gap-4">
        <div className="grid grid-cols-[minmax(0,1fr)_200px_200px] gap-3 max-lg:grid-cols-1">
          <Field label="搜索">
            <input className="input" onChange={(event) => setQuery(event.target.value)} value={query} />
          </Field>
          <Field label="类型">
            <select className="select" onChange={(event) => { setMediaTypeFilter(event.target.value as "all" | MediaType); setModeFilter("all"); }} value={mediaTypeFilter}>
              <option value="all">全部</option>
              {MEDIA_TYPES.map((t) => (
                <option key={t.id} value={t.id}>{t.label}</option>
              ))}
            </select>
          </Field>
          <Field label="适用模式">
            <select className="select" onChange={(event) => setModeFilter(event.target.value)} value={modeFilter}>
              <option value="all">全部</option>
              {(mediaTypeFilter === "all" ? (
                [...IMAGE_MODES, ...VIDEO_MODES].map((m) => (
                  <option key={m.id} value={m.id}>{m.label}</option>
                ))
              ) : mediaTypeFilter === "image" ? (
                IMAGE_MODES.map((m) => (
                  <option key={m.id} value={m.id}>{m.label}</option>
                ))
              ) : (
                VIDEO_MODES.map((m) => (
                  <option key={m.id} value={m.id}>{m.label}</option>
                ))
              ))}
            </select>
          </Field>
        </div>

        <div className="grid grid-cols-[minmax(280px,0.86fr)_minmax(0,1.35fr)] gap-4 max-xl:grid-cols-1">
          <section className="min-w-0 overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface-soft)]">
            <div className="flex items-center justify-between gap-3 border-b border-[var(--line)] px-3 py-2">
              <strong className="text-sm">条目</strong>
              <span className="muted mono text-xs">{filtered.length} / {prompts.length}</span>
            </div>
            {filtered.length ? (
              <div className="max-h-[620px] overflow-y-auto">
                {filtered.map((prompt) => {
                  const selectedRow = prompt.id === selected?.id && !creating;
                  return (
                    <article className={`grid gap-2 border-b border-[var(--line)] p-3 last:border-b-0 ${selectedRow ? "bg-[var(--surface)] shadow-[inset_2px_0_0_var(--accent)]" : "hover:bg-[var(--surface)]"}`} key={prompt.id}>
                       <button className="grid min-w-0 gap-2 text-left" onClick={() => {
                         setCreating(false);
                         setEditing(false);
                         onSelect(prompt);
                       }} type="button">
                         <div className="flex min-w-0 items-center gap-2 flex-wrap">
                           <strong className="min-w-0 truncate text-sm">{prompt.title}</strong>
                             <span className={`pill pill-xs ${VIDEO_MODES.some((m) => m.id === String(prompt.mode || "")) ? "pill-muted" : ""}`}>
                               {VIDEO_MODES.some((m) => m.id === String(prompt.mode || "")) ? "视频" : "图片"}
                             </span>
                             <Pill>{mediaModeLabel(prompt.mode as ImageMode, VIDEO_MODES.some((m) => m.id === String(prompt.mode || "")) ? "video" : "image")}</Pill>
                         </div>
                        <p className="muted m-0 line-clamp-2 text-xs leading-relaxed">{prompt.description || prompt.prompt}</p>
                        <div className="muted mono flex flex-wrap gap-x-3 gap-y-1 text-xs">
                          <span>used {prompt.useCount || 0}</span>
                          {prompt.lastUsedAt ? <span>{formatDate(prompt.lastUsedAt)}</span> : null}
                          {prompt.tags?.slice(0, 3).map((tag) => <span key={tag}>#{tag}</span>)}
                        </div>
                      </button>
                      <div className="flex flex-wrap gap-2">
                        <Button className="min-h-8 px-2 text-xs" disabled={busy === `prompt-use:${prompt.id}`} onClick={() => onUse(prompt)}>
                          使用
                        </Button>
                        <Button className="min-h-8 px-2 text-xs" onClick={() => startEdit(prompt)}>
                          编辑
                        </Button>
                      </div>
                    </article>
                  );
                })}
              </div>
            ) : (
              <div className="p-3">
                <EmptyState title="没有匹配的生成预设" body="调整搜索条件，或新建一个生成参数预设。" />
              </div>
            )}
          </section>

          <section className="min-w-0 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-4">
            {editing ? (
              <form className="grid gap-4" onSubmit={(event) => void save(event)}>
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <div className="flex flex-wrap items-center gap-2">
                      <h3 className="m-0 text-sm font-semibold">{creating ? "新建生成预设" : "编辑生成预设"}</h3>
                      {draft._source ? <Pill tone="neutral">{draft._source}</Pill> : null}
                    </div>
                    <p className="muted mt-1 mb-0 text-xs">生成预设保存参数组合，包括提示词、媒体类型、模型和模式。<strong className="text-[var(--muted-strong)]">不保存参考图引用</strong>，参考图仍需从生成表单或资源库手动选择。</p>
                  </div>
                  <div className="flex gap-2">
                    <Button onClick={cancelEdit} type="button">
                      取消
                    </Button>
                    <Button disabled={saving} tone="primary" type="submit">
                      {saving ? "保存中" : "保存"}
                    </Button>
                  </div>
                 </div>

                 <div className="flex flex-wrap items-start gap-x-4 gap-y-1.5 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-xs">
                   {draft._source ? (
                     <div className="flex items-center gap-1.5">
                       <span className="muted">来源</span>
                       <span className="font-medium">{draft._source}</span>
                     </div>
                   ) : editing ? (
                     <div className="flex items-center gap-1.5">
                       <span className="muted">来源</span>
                       <span>预设库</span>
                     </div>
                   ) : (
                     <div className="flex items-center gap-1.5">
                       <span className="muted">来源</span>
                       <span>手动创建</span>
                     </div>
                   )}
                   <span className="muted text-[var(--line-strong)]">·</span>
                   <div className="flex items-center gap-1.5">
                     <span className="muted">参考图</span>
                     <span>不保存，仅保存参数（<strong className="text-[var(--muted-strong)]">提示词 / 模型 / 模式 / 比例 / 时长</strong>）</span>
                   </div>
                 </div>

                 <div className="grid grid-cols-[minmax(0,1fr)_140px_200px] gap-3 max-lg:grid-cols-1">
                  <Field label="标题">
                    <input className="input" maxLength={120} onChange={(event) => updateDraft("title", event.target.value)} required value={draft.title} />
                  </Field>
                  <Field label="生成类型">
                    <select
                      className="select"
                      onChange={(event) => {
                        const next = event.target.value as MediaType;
                        updateDraft("mediaType", next);
                        const defaultMode = next === "image" ? "text_to_image" : "text_to_video";
                        const nextMode = (next === "image"
                          ? IMAGE_MODES.some((m) => m.id === draft.mode)
                          : VIDEO_MODES.some((m) => m.id === draft.mode))
                          ? draft.mode
                          : defaultMode;
                        updateDraft("mode", nextMode);
                      }}
                      value={draft.mediaType}
                    >
                      {MEDIA_TYPES.map((t) => (
                        <option key={t.id} value={t.id}>{t.label}</option>
                      ))}
                    </select>
                  </Field>
                  <Field label="适用模式">
                    <select className="select" onChange={(event) => updateDraft("mode", event.target.value)} value={draft.mode}>
                      {(draft.mediaType === "image" ? IMAGE_MODES : VIDEO_MODES).map((mode) => (
                        <option key={mode.id} value={mode.id}>
                          {mode.label}
                        </option>
                      ))}
                    </select>
                  </Field>
                </div>

                 <Field label="提示词" help="最多 8000 字符。可以包含文字描述、风格提示、构图、镜头、约束条件等。">
                  <textarea className="textarea min-h-48" maxLength={8000} onChange={(event) => updateDraft("prompt", event.target.value)} required value={draft.prompt} />
                </Field>

                <Field label="说明">
                  <textarea className="textarea min-h-20" maxLength={1000} onChange={(event) => updateDraft("description", event.target.value)} value={draft.description} />
                </Field>

                <div className="grid grid-cols-4 gap-3 max-lg:grid-cols-2 max-sm:grid-cols-1">
                  <Field label="模型">
                    <select className="select mono" onChange={(event) => updateDraft("model", event.target.value)} value={draft.model}>
                      {promptModelOptions(draft.model, settings, draft.mediaType).map((model) => (
                        <option key={model || "default"} value={model}>
                          {model || "模块默认"}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Field label="比例">
                    <select className="select mono" onChange={(event) => updateDraft("aspectRatio", event.target.value)} value={draft.aspectRatio}>
                      {ASPECT_OPTIONS.map((value) => (
                        <option key={value || "default"} value={value}>
                          {value || "默认"}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Field label="分辨率">
                    <select className="select mono" onChange={(event) => updateDraft("resolution", event.target.value)} value={draft.resolution}>
                      {RESOLUTION_OPTIONS.map((value) => (
                        <option key={value || "default"} value={value}>
                          {value || "默认"}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Field label="数量">
                    <input className="input mono" max={10} min={1} onChange={(event) => updateDraft("imageCount", clampImageCount(Number(event.target.value || 1)))} type="number" value={draft.imageCount} />
                  </Field>
                </div>

                {draft.mediaType === "video" ? (
                  <div className="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
                    <Field label="视频时长 (秒)" help="目前支持 5 秒 / 10 秒">
                      <select className="select mono" onChange={(event) => updateDraft("videoDuration", Number(event.target.value))} value={draft.videoDuration ?? 5}>
                        {DURATION_PRESETS.map((d) => (
                          <option key={d.id} value={Number(d.id.replace(/\D/g, ""))}>{d.label}</option>
                        ))}
                      </select>
                    </Field>
                    <Field label="帧率 (fps)">
                      <select className="select mono" onChange={(event) => updateDraft("videoFps", Number(event.target.value))} value={draft.videoFps ?? 24}>
                        <option value={24}>24 fps</option>
                        <option value={30}>30 fps</option>
                      </select>
                    </Field>
                  </div>
                ) : null}

                <Field label="标签" help="用逗号分隔，最多 12 个。">
                  <input className="input" onChange={(event) => updateDraft("tagsText", event.target.value)} value={draft.tagsText} />
                </Field>
              </form>
            ) : selected ? (
              <PromptDetail onDelete={onDelete} onEdit={() => startEdit(selected)} onUse={onUse} prompt={selected} />
             ) : (
                <EmptyState title="暂无生成预设" body="新建一个生成预设保存常用的生成参数组合，可以一键带入生成任务。" />
             )}
          </section>
        </div>
      </div>
    </Panel>
  );

  function updateDraft<Key extends keyof ImagePromptFormState>(key: Key, value: ImagePromptFormState[Key]) {
    setDraft((current) => ({ ...current, [key]: value }));
  }
}

function PromptDetail({ onDelete, onEdit, onUse, prompt }: { onDelete: (prompt: ImagePrompt) => void; onEdit: () => void; onUse: (prompt: ImagePrompt) => void; prompt: ImagePrompt }) {
  const isVideo = VIDEO_MODES.some((m) => m.id === String(prompt.mode || ""));
  return (
    <div className="grid gap-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="m-0 break-words text-sm font-semibold">{prompt.title}</h3>
          <div className="mt-2 flex flex-wrap gap-2">
            <Pill>{isVideo ? "视频" : "图片"}</Pill>
            <Pill>{mediaModeLabel(prompt.mode as ImageMode, isVideo ? "video" : "image")}</Pill>
            {prompt.model ? <Pill>{prompt.model}</Pill> : null}
            {prompt.aspectRatio ? <Pill>{prompt.aspectRatio}</Pill> : null}
            {prompt.resolution ? <Pill>{prompt.resolution}</Pill> : null}
          </div>
        </div>
        <div className="flex flex-wrap justify-end gap-2">
          <Button onClick={() => onUse(prompt)} tone="primary">
            使用
          </Button>
          <Button onClick={onEdit}>编辑</Button>
          <Button onClick={() => onDelete(prompt)} tone="danger">
            删除
          </Button>
        </div>
      </div>
      {prompt.description ? <p className="muted m-0 text-sm leading-relaxed">{prompt.description}</p> : null}
      <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
        <pre className="m-0 max-h-[360px] whitespace-pre-wrap break-words font-sans text-sm leading-6">{prompt.prompt}</pre>
      </div>
      <ContextList
        items={[
          ["数量", prompt.imageCount || 1],
          ["使用次数", prompt.useCount || 0],
          ["最近使用", formatDate(prompt.lastUsedAt) || "-"],
          ["标签", prompt.tags?.length ? prompt.tags.map((tag) => `#${tag}`).join(" ") : "-"],
          ["创建", formatDate(prompt.createdAt) || "-"],
          ["更新", formatDate(prompt.updatedAt) || "-"],
        ]}
      />
    </div>
  );
}

function promptFormFromPrompt(
  prompt: ImagePrompt | undefined,
  settings: ImageProviderSettings,
  overrides?: Partial<ImagePromptFormState>,
): ImagePromptFormState {
  const defaults = { ...defaultImageSettings(), ...settings };
  const modeStr = String(overrides?.mode || prompt?.mode || "text_to_image");
  const mediaType: MediaType = overrides?.mediaType || (VIDEO_MODES.some((m) => m.id === modeStr) ? "video" : "image");
  const mode = mediaType === "image" ? normalizeImageMode(modeStr) : modeStr;
  return {
    title: overrides?.title || prompt?.title || "",
    description: overrides?.description || prompt?.description || "",
    prompt: overrides?.prompt || prompt?.prompt || "",
    mediaType,
    mode,
    model: overrides?.model || prompt?.model || defaults.defaultModel || "",
    aspectRatio: overrides?.aspectRatio || prompt?.aspectRatio || defaults.defaultAspectRatio || "",
    resolution: overrides?.resolution || prompt?.resolution || defaults.defaultResolution || "",
    imageCount: clampImageCount(overrides?.imageCount ?? prompt?.imageCount ?? 1),
    tagsText: overrides?.tagsText || (prompt?.tags || []).join(", "),
    videoDuration: overrides?.videoDuration,
    videoFps: overrides?.videoFps,
  };
}

function promptDraftFromForm(form: ImagePromptFormState): ImagePromptDraft {
  return {
    title: form.title,
    description: form.description,
    prompt: form.prompt,
    mode: form.mode as ImageMode,
    model: form.model,
    aspectRatio: form.aspectRatio,
    resolution: form.resolution,
    imageCount: clampImageCount(form.imageCount),
    tags: form.tagsText.split(",").map((tag) => tag.trim()).filter(Boolean),
  };
}

function filterPrompts(prompts: ImagePrompt[], query: string, mediaType: "all" | MediaType, mode: string): ImagePrompt[] {
  const needle = query.trim().toLowerCase();
  return prompts.filter((prompt) => {
    if (prompt.status === "deleted") return false;
    if (mediaType !== "all") {
      const isVideo = VIDEO_MODES.some((m) => m.id === String(prompt.mode || ""));
      if (mediaType === "video" && !isVideo) return false;
      if (mediaType === "image" && isVideo) return false;
    }
    if (mode !== "all" && prompt.mode !== mode) return false;
    if (!needle) return true;
    const haystack = [prompt.title, prompt.description, prompt.prompt, ...(prompt.tags || [])].join(" ").toLowerCase();
    return haystack.includes(needle);
  });
}

function promptModelOptions(current: string, settings: ImageProviderSettings, mediaType: MediaType = "image"): string[] {
  const values = mediaType === "image" ? ["", ...GROK_MODEL_OPTIONS] : [""];
  const defaultModel = settings.defaultModel || "";
  if (defaultModel && !values.includes(defaultModel)) values.push(defaultModel);
  if (current && !values.includes(current)) values.push(current);
  return values;
}

type AnyAsset = { kind: "legacy"; data: ImageAsset } | { kind: "media"; data: MediaAsset };

function uniqueAssetRefs(refs: AssetRef[]): AssetRef[] {
  const seen = new Set<string>();
  const result: AssetRef[] = [];
  for (const ref of refs) {
    const key = assetRefKey(ref);
    if (seen.has(key)) continue;
    seen.add(key);
    result.push(ref);
  }
  return result;
}

function anyAssetId(a: AnyAsset): string {
  return a.kind === "legacy" ? (a.data.id ?? "") : (a.data.id ?? "");
}
function anyAssetCreatedAt(a: AnyAsset): string {
  return (a.kind === "legacy" ? a.data.createdAt : a.data.createdAt) || "";
}
function anyAssetSizeBytes(a: AnyAsset): number {
  return (a.kind === "legacy" ? a.data.sizeBytes : a.data.sizeBytes) || 0;
}
function anyAssetMediaType(a: AnyAsset): "image" | "video" {
  return a.kind === "legacy" ? "image" : (a.data.mediaType || "image") as "image" | "video";
}
function anyAssetStorage(a: AnyAsset): string {
  return (a.kind === "legacy" ? a.data.storageBackend : a.data.storageBackend) || "";
}
function anyAssetSearchText(a: AnyAsset): string {
  if (a.kind === "legacy") {
    const d = a.data;
    return [d.promptPreview || "", d.revisedPromptPreview || "", d.model || "", d.jobId || "", d.id, d.originalFilename || ""].join(" ").toLowerCase();
  }
  const d = a.data;
  return [d.promptPreview || "", d.revisedPromptPreview || "", d.model || "", d.jobId || "", d.id, d.originalFilename || ""].join(" ").toLowerCase();
}
function anyAssetIsGenerated(a: AnyAsset): boolean {
  const t = (a.kind === "legacy" ? a.data.assetType : a.data.assetType) || "";
  return t === "generated";
}
function anyAssetIsUpload(a: AnyAsset): boolean {
  const t = (a.kind === "legacy" ? a.data.assetType : a.data.assetType) || "";
  return t.includes("upload");
}
function anyAssetIsSource(a: AnyAsset): boolean {
  if (a.kind === "legacy") return a.data.assetType === "source_upload";
  return Boolean(a.data.sourceRole !== undefined && a.data.sourceRole !== null && a.data.sourceRole !== "");
}
function anyAssetIsVideo(a: AnyAsset): boolean {
  return anyAssetMediaType(a) === "video";
}
function anyAssetIsLocal(a: AnyAsset): boolean {
  return anyAssetStorage(a) === "local";
}
function anyAssetIsS3(a: AnyAsset): boolean {
  const s = anyAssetStorage(a);
  return s === "s3" || s === "object_storage";
}
function anyAssetIsRemote(a: AnyAsset): boolean {
  if (a.kind === "legacy") return a.data.storageBackend === "remote";
  const d = a.data;
  const hasUrl = Boolean(d.url || d.downloadUrl);
  const isLocalOrS3 = anyAssetIsLocal(a) || anyAssetIsS3(a);
  return hasUrl && !isLocalOrS3;
}
function anyAssetProvider(a: AnyAsset): string {
  if (a.kind === "legacy") return "xai";
  return (a.data.provider || "") as string;
}
function anyAssetIsPrivate(a: AnyAsset): boolean {
  return Boolean(a.kind === "legacy" ? a.data.private : a.data.private);
}
function anyAssetMode(a: AnyAsset, mediaJobs?: MediaGenerationJob[], legacyJobs?: ImageGenerationJob[]): string {
  const jobId = a.kind === "legacy" ? a.data.jobId : a.data.jobId;
  if (!jobId) return "";
  if (a.kind === "legacy" && legacyJobs) {
    const j = legacyJobs.find((x) => x.id === jobId);
    if (j) return j.mode || "text_to_image";
  }
  if (a.kind === "media" && mediaJobs) {
    const j = mediaJobs.find((x) => x.id === jobId);
    if (j) return j.mode || (a.data.mediaType === "video" ? "text_to_video" : "text_to_image");
  }
  return "";
}

function referenceAnyAssetTitle(a: AnyAsset): string {
  if (a.kind === "legacy") return assetTitle(a.data);
  return a.data.originalFilename || a.data.promptPreview || a.data.revisedPromptPreview || a.data.id;
}

function referenceAnyAssetURL(a: AnyAsset): string {
  if (a.kind === "legacy") return a.data.downloadUrl || a.data.url || "";
  return mediaContentURL(a.data);
}

function referenceAnyAssetSourceLabel(a: AnyAsset): string {
  if (anyAssetIsGenerated(a)) return "生成";
  if (anyAssetIsUpload(a)) return "上传";
  if (anyAssetIsSource(a)) return "参考源";
  const type = a.kind === "legacy" ? a.data.assetType : a.data.assetType;
  return type || "图片";
}

export function LibraryPanel({
  assets,
  busy,
  libraryScope,
  mediaAssets,
  mediaJobs,
  legacyJobs,
  mediaType,
  onArchive,
  onArchiveMedia,
  onBulkDeleteResources,
  onBulkDownloadComplete,
  onDelete,
  onDeleteMedia,
  onGoToGenerate,
  onGoToSettings,
  onLockPrivate,
  onMarkPrivate,
  onMarkPrivateMedia,
  onMediaTypeChange,
  onOpenJob,
  pagination,
  onRefresh,
  onRefreshMedia,
  onSelect,
  onSelectMedia,
  onScopeChange,
  onSetKeyframes,
  onSetMultiEditImages,
  onSetVideoReference,
  onUpload,
  onUseForImage,
  onUseMediaForImage,
  onUnlockPrivate,
  privateExpiresAt,
   privateUnlocked,
   selectedLegacyId,
   selectedMediaId,
   storageSettings,
}: {
   assets: ImageAsset[];
   busy: string;
   libraryScope: ImageLibraryScope;
   mediaAssets?: MediaAsset[];
   mediaJobs?: MediaGenerationJob[];
   legacyJobs?: ImageGenerationJob[];
   mediaType?: MediaType;
   onArchive: (asset: ImageAsset) => void;
   onArchiveMedia?: (asset: MediaAsset) => void;
   onBulkDeleteResources?: (resources: AssetRef[]) => Promise<boolean>;
   onBulkDownloadComplete?: (count: number) => void;
   onDelete: (asset: ImageAsset) => void;
   onDeleteMedia?: (asset: MediaAsset) => void;
   onGoToGenerate?: () => void;
   onGoToSettings?: () => void;
   onLockPrivate: () => void;
   onMarkPrivate: (asset: ImageAsset, nextPrivate: boolean) => void;
   onMarkPrivateMedia?: (asset: MediaAsset, nextPrivate: boolean) => void;
   onMediaTypeChange?: (t: MediaType) => void;
   onOpenJob?: (jobId: string, kind: AssetKind) => void;
   pagination?: PaginationState;
   onRefresh: () => void | Promise<void>;
   onRefreshMedia?: () => void | Promise<void>;
   onSelect: (asset: ImageAsset) => void;
   onSelectMedia?: (asset: MediaAsset) => void;
   onScopeChange: (scope: ImageLibraryScope) => void;
   onSetKeyframes?: (refs: AssetRef[]) => void;
   onSetMultiEditImages?: (refs: AssetRef[]) => void;
   onSetVideoReference?: (ref: AssetRef) => void;
   onUpload: (data: FormData) => Promise<boolean>;
   onUseForImage: (asset: ImageAsset) => void;
   onUseMediaForImage?: (asset: MediaAsset) => void;
   onUnlockPrivate: (password: string) => Promise<void>;
   privateExpiresAt?: string;
   privateUnlocked: boolean;
   selectedLegacyId?: string;
   selectedMediaId?: string;
   storageSettings: ImageStorageSettings;
}) {
  const [viewer, setViewer] = useState<ImageAsset | null>(null);
  const [mediaViewer, setMediaViewer] = useState<MediaAsset | null>(null);
  const [uploadFile, setUploadFile] = useState<File | null>(null);
  const privateScope = libraryScope === "private";

  const [searchQuery, setSearchQuery] = useState("");
  const [filterMediaType, setFilterMediaType] = useState<"all" | "image" | "video">("all");
  const [sortOrder, setSortOrder] = useState<"newest" | "oldest" | "size">("newest");
  const [filterProvider, setFilterProvider] = useState<"all" | "agnes" | "xai">("all");
  const [filterStorage, setFilterStorage] = useState<"all" | "local" | "s3" | "remote">("all");
  const [filterPrivate, setFilterPrivate] = useState<"all" | "private" | "public">("all");
  const [filterMode, setFilterMode] = useState<string>("all");
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [selectionMode, setSelectionMode] = useState(false);
  const [i2iMenuOpen, setI2iMenuOpen] = useState(false);
  const [filterPopoverOpen, setFilterPopoverOpen] = useState(false);
  const [uploadOpen, setUploadOpen] = useState(false);
  const [bulkError, setBulkError] = useState<{ failedCount: number; summary: string } | null>(null);

  const filteredMediaAssets = useMemo(() => {
    return mediaAssets || [];
  }, [mediaAssets]);

  const allAssets = useMemo<AnyAsset[]>(() => {
    const legacy: AnyAsset[] = (assets || []).map((a) => ({ kind: "legacy", data: a }));
    const media: AnyAsset[] = (filteredMediaAssets || []).map((a) => ({ kind: "media", data: a }));
    return [...legacy, ...media];
  }, [assets, filteredMediaAssets]);

  const filteredSorted = useMemo<AnyAsset[]>(() => {
    const needle = searchQuery.trim().toLowerCase();
    let list = allAssets.filter((a) => {
      if (needle && !anyAssetSearchText(a).includes(needle)) return false;
      switch (filterMediaType) {
        case "image": if (anyAssetIsVideo(a)) return false; break;
        case "video": if (!anyAssetIsVideo(a)) return false; break;
      }
      if (filterProvider !== "all") {
        const prov = anyAssetProvider(a).toLowerCase();
        if (prov !== filterProvider) return false;
      }
      if (filterStorage !== "all") {
        switch (filterStorage) {
          case "local": if (!anyAssetIsLocal(a)) return false; break;
          case "s3": if (!anyAssetIsS3(a)) return false; break;
          case "remote": if (!anyAssetIsRemote(a)) return false; break;
        }
      }
      if (filterPrivate === "private" && !anyAssetIsPrivate(a)) return false;
      if (filterPrivate === "public" && anyAssetIsPrivate(a)) return false;
      if (filterMode !== "all" && anyAssetMode(a, mediaJobs, legacyJobs) !== filterMode) return false;
      return true;
    });
    list = [...list];
    switch (sortOrder) {
      case "newest":
        list.sort((a, b) => anyAssetCreatedAt(b).localeCompare(anyAssetCreatedAt(a))); break;
      case "oldest":
        list.sort((a, b) => anyAssetCreatedAt(a).localeCompare(anyAssetCreatedAt(b))); break;
      case "size":
        list.sort((a, b) => anyAssetSizeBytes(b) - anyAssetSizeBytes(a)); break;
    }
    return list;
  }, [allAssets, searchQuery, filterMediaType, filterProvider, filterStorage, filterPrivate, filterMode, mediaJobs, legacyJobs, sortOrder]);

  const filteredLegacyIds = useMemo(() => new Set(filteredSorted.filter((a) => a.kind === "legacy").map((a) => a.data.id)), [filteredSorted]);
  const filteredMediaIds = useMemo(() => new Set(filteredSorted.filter((a) => a.kind === "media").map((a) => a.data.id)), [filteredSorted]);
  const displayLegacyAssets = useMemo(() => assets.filter((a) => filteredLegacyIds.has(a.id)), [assets, filteredLegacyIds]);
  const displayMediaAssets = useMemo(() => filteredMediaAssets.filter((a) => filteredMediaIds.has(a.id)), [filteredMediaAssets, filteredMediaIds]);
  const visibleLegacyAssets = useMemo(() => {
    const order = new Map<string, number>();
    let i = 0;
    for (const a of filteredSorted) { if (a.kind === "legacy") order.set(a.data.id, i++); }
    return [...displayLegacyAssets].sort((a, b) => (order.get(a.id) ?? 0) - (order.get(b.id) ?? 0));
  }, [filteredSorted, displayLegacyAssets]);
  const visibleMediaAssets = useMemo(() => {
    const order = new Map<string, number>();
    let i = 0;
    for (const a of filteredSorted) { if (a.kind === "media") order.set(a.data.id, i++); }
    return [...displayMediaAssets].sort((a, b) => (order.get(a.id) ?? 0) - (order.get(b.id) ?? 0));
  }, [filteredSorted, displayMediaAssets]);

  const filteredLegacyList = visibleLegacyAssets;
  const filteredMediaList = visibleMediaAssets;

  const visibleIds = useMemo(() => new Set(filteredSorted.map(anyAssetId)), [filteredSorted]);
  const viewerLegacyIndex = viewer ? filteredLegacyList.findIndex((a) => a.id === viewer.id) : -1;
  const viewerMediaIndex = mediaViewer ? filteredMediaList.findIndex((a) => a.id === mediaViewer.id) : -1;

  function toggleSelected(id: string) {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  }

  function handleSelectAll() {
    const allInView = filteredSorted.map(anyAssetId);
    const allSelected = allInView.length > 0 && allInView.every((id) => selectedIds.has(id));
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (allSelected) { for (const id of allInView) next.delete(id); }
      else { for (const id of allInView) next.add(id); }
      return next;
    });
  }

  async function handleBulkDownloadClick() {
    const urls: string[] = [];
    for (const id of selectedIds) {
      const legacy = assets.find((a) => a.id === id);
      if (legacy) {
        urls.push(assetDownloadURL(legacy));
        continue;
      }
      const media = filteredMediaAssets.find((a) => a.id === id);
      if (media) {
        urls.push(mediaDownloadURL(media));
      }
    }
    const valid = urls.filter((u) => u.length > 0);
    if (!valid.length) return;

    let successCount = 0;
    for (const url of valid) {
      const a = document.createElement("a");
      a.href = url;
      a.download = "";
      a.target = "_blank";
      a.rel = "noopener";
      a.style.display = "none";
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      successCount++;
      await new Promise<void>((resolve) => window.setTimeout(resolve, 150));
    }
    onBulkDownloadComplete?.(successCount);
  }

  function handleBulkArchive() {
    const localLegacy: ImageAsset[] = [];
    for (const a of filteredSorted) {
      if (!selectedIds.has(anyAssetId(a))) continue;
      if (a.kind === "legacy" && (a.data.storageBackend === "local" || a.data.storageBackend === "remote")) {
        localLegacy.push(a.data);
      }
    }
    for (const asset of localLegacy) onArchive(asset);
  }

  async function handleBulkDeleteClick() {
    const resources: Array<{ kind: "legacy" | "media"; id: string }> = [];
    for (const id of selectedIds) {
      if (assets.some((a) => a.id === id)) resources.push({ kind: "legacy", id });
      else if (filteredMediaAssets.some((a) => a.id === id)) resources.push({ kind: "media", id });
    }
    if (!resources.length) return;
    const totalCount = resources.length;
    try {
      const ok = await onBulkDeleteResources?.(resources);
      if (ok) {
        setSelectedIds(new Set());
        setBulkError(null);
      } else {
        setBulkError({ failedCount: totalCount, summary: "部分或全部资源删除失败，请检查网络后重试" });
      }
    } catch (err) {
      setBulkError({
        failedCount: totalCount,
        summary: (err instanceof Error ? err.message : String(err)).slice(0, 60) || "删除请求异常",
      });
    }
  }

  function handleUseSelectedForI2I() {
    const selectedList = filteredSorted.filter((a) => selectedIds.has(anyAssetId(a)));
    if (selectedList.length !== 1) return;
    const target = selectedList[0];
    if (anyAssetIsVideo(target)) return;
    if (target.kind === "legacy") onUseForImage(target.data);
    else onUseMediaForImage?.(target.data);
  }

  function getSelectedImageAssets(): AnyAsset[] {
    return filteredSorted.filter((a) => selectedIds.has(anyAssetId(a)) && !anyAssetIsVideo(a));
  }

  const numSelectedImages = useMemo(() => getSelectedImageAssets().length, [selectedIds, filteredSorted]);

  function assetRefFromAny(a: AnyAsset): AssetRef {
    return { kind: a.kind as AssetKind, id: anyAssetId(a) };
  }

  function handleUseForI2IReference() {
    const imgs = getSelectedImageAssets();
    if (imgs.length !== 1) return;
    const target = imgs[0];
    if (target.kind === "legacy") onUseForImage(target.data);
    else onUseMediaForImage?.(target.data);
  }

  function handleUseForMultiEdit() {
    const imgs = getSelectedImageAssets();
    if (imgs.length < 2 || imgs.length > 3) return;
    const refs = imgs.map(assetRefFromAny);
    onSetMultiEditImages?.(refs);
  }

  function handleUseForKeyframes() {
    const imgs = getSelectedImageAssets();
    if (imgs.length < 2 || imgs.length > 6) return;
    const refs = imgs.map(assetRefFromAny);
    onSetKeyframes?.(refs);
  }

  function handleUseForVideoReference() {
    const imgs = getSelectedImageAssets();
    if (imgs.length !== 1) return;
    const ref = assetRefFromAny(imgs[0]);
    onSetVideoReference?.(ref);
  }

  const hasLocalSelected = useMemo(() => filteredSorted.some((a) => selectedIds.has(anyAssetId(a)) && a.kind === "legacy" && (a.data.storageBackend === "local" || a.data.storageBackend === "remote")), [filteredSorted, selectedIds]);
  const exactlyOneImageSelected = useMemo(() => {
    const sel = filteredSorted.filter((a) => selectedIds.has(anyAssetId(a)));
    return sel.length === 1 && !anyAssetIsVideo(sel[0]);
  }, [filteredSorted, selectedIds]);
  const hasSelected = selectedIds.size > 0;
  const allVisibleSelected = visibleIds.size > 0 && [...visibleIds].every((id) => selectedIds.has(id));
  const selectedLegacyCount = useMemo(() => filteredSorted.filter((a) => a.kind === "legacy" && selectedIds.has(a.data.id)).length, [filteredSorted, selectedIds]);
  const selectedMediaCount = selectedIds.size - selectedLegacyCount;

  function triggerDownload(asset: ImageAsset) {
    window.open(assetDownloadURL(asset), "_blank");
  }
  function triggerMediaDownload(asset: MediaAsset) {
    const a = document.createElement('a');
    a.href = asset.downloadUrl || asset.url || '';
    a.download = '';
    a.target = '_blank';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  }

  const totalAssetCount = assets.length + (filteredMediaAssets.length || 0);
  const totalImageCount = assets.length + (mediaAssets || []).filter((a) => a.mediaType === "image").length;
  const totalVideoCount = (mediaAssets || []).filter((a) => a.mediaType === "video").length;
  const totalObjectStorage = assets.filter((asset) => asset.storageBackend === "s3").length +
    (mediaAssets || []).filter((a) => a.storageBackend === "s3" || a.storageBackend === "object_storage").length;

  async function submitUpload(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!uploadFile) return;
    const formData = new FormData();
    formData.append("image", uploadFile);
    const ok = await onUpload(formData);
    if (ok) setUploadFile(null);
  }

  useEffect(() => {
    if (!viewer) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setViewer(null);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [viewer]);

  useEffect(() => {
    if (!mediaViewer) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMediaViewer(null);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [mediaViewer]);

  return (
    <>
      <Panel
        actions={
          <>
            {privateScope && privateUnlocked ? (
              <Button disabled={busy === "private-lock"} onClick={onLockPrivate}>
                锁定
              </Button>
            ) : !privateScope ? (
              <Button onClick={() => setUploadOpen((o) => !o)} tone={uploadOpen ? "primary" : "neutral"}>
                {uploadOpen ? "收起上传" : "上传资源"}
              </Button>
            ) : null}
            <Button
              onClick={async () => {
                await onRefresh();
                if (onRefreshMedia) await onRefreshMedia();
              }}
            >
              刷新
            </Button>
          </>
        }
        subtitle={privateScope ? "私密收藏夹需要重新输入 owner 密码解锁；解锁只在当前 session 内短期有效。" : "生成结果和用户上传参考图会进入资源库；Agnes 图片和视频资源也在此管理。"}
        title={privateScope ? "私密收藏夹" : "资源库"}
      >
         <LibraryScopeSwitch active={libraryScope} onChange={onScopeChange} />
         {privateScope && !privateUnlocked ? (
           <PrivateUnlockPanel busy={busy === "private-unlock"} onUnlock={onUnlockPrivate} />
         ) : (
           <>
               <section className="card-soft my-3 grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
                 <div className="grid grid-cols-[minmax(0,1fr)_minmax(140px,1fr)_minmax(140px,1fr)_auto_auto] gap-2 max-lg:grid-cols-2 max-md:grid-cols-1">
                   <Field label="搜索">
                     <input
                       className="input"
                       onChange={(event) => setSearchQuery(event.target.value)}
                       placeholder="prompt / 模型 / 文件名 / jobId / id"
                       value={searchQuery}
                     />
                   </Field>
                   <Field label="媒体类型">
                     <select className="select" onChange={(event) => setFilterMediaType(event.target.value as "all" | "image" | "video")} value={filterMediaType}>
                       <option value="all">全部</option>
                       <option value="image">仅图片</option>
                       <option value="video">仅视频</option>
                     </select>
                   </Field>
                   <Field label="排序">
                     <select className="select" onChange={(event) => setSortOrder(event.target.value as "newest" | "oldest" | "size")} value={sortOrder}>
                       <option value="newest">最新</option>
                       <option value="oldest">最旧</option>
                       <option value="size">按大小</option>
                     </select>
                   </Field>
                   <div className="relative flex items-end">
                     <Button
                       aria-pressed={filterPopoverOpen}
                       onClick={() => setFilterPopoverOpen((o) => !o)}
                       onBlur={() => window.setTimeout(() => setFilterPopoverOpen(false), 150)}
                       tone={filterPopoverOpen ? "primary" : "neutral"}
                       type="button"
                     >
                       筛选 ▾
                     </Button>
                     {filterPopoverOpen ? (
                       <div className="absolute left-0 bottom-0 z-20 mb-10 w-[420px] rounded-md border border-[var(--line)] bg-[var(--surface)] p-3 shadow-lg max-md:w-[calc(100vw-80px)]">
                          <div className="grid grid-cols-3 gap-2">
                            <Field label="供应商">
                              <select className="select" onChange={(event) => setFilterProvider(event.target.value as "all" | "agnes" | "xai")} value={filterProvider}>
                                <option value="all">全部</option>
                                <option value="xai">xAI</option>
                                <option value="agnes">Agnes</option>
                              </select>
                            </Field>
                            <Field label="存储后端">
                              <select className="select" onChange={(event) => setFilterStorage(event.target.value as "all" | "local" | "s3" | "remote")} value={filterStorage}>
                                <option value="all">全部</option>
                                <option value="local">本地</option>
                                <option value="s3">对象存储</option>
                                <option value="remote">远端 URL</option>
                              </select>
                            </Field>
                            <Field label="私密状态">
                              <select className="select" onChange={(event) => setFilterPrivate(event.target.value as "all" | "private" | "public")} value={filterPrivate}>
                                <option value="all">全部</option>
                                <option value="private">仅私密</option>
                                <option value="public">仅公开</option>
                              </select>
                            </Field>
                          </div>
                          <div className="mt-2 grid grid-cols-1 gap-2 border-t border-[var(--line)] pt-2">
                            <Field label="生成模式">
                              <select className="select" onChange={(event) => setFilterMode(event.target.value)} value={filterMode}>
                                <option value="all">全部</option>
                                <optgroup label="图片">
                                  {IMAGE_MODES.map((m) => (
                                    <option key={m.id} value={m.id}>{m.label}</option>
                                  ))}
                                </optgroup>
                                <optgroup label="视频">
                                  {VIDEO_MODES.map((m) => (
                                    <option key={m.id} value={m.id}>{m.label}</option>
                                  ))}
                                </optgroup>
                              </select>
                            </Field>
                          </div>
                       </div>
                     ) : null}
                   </div>
                   <div className="flex items-end gap-2">
                     <Button
                       aria-pressed={selectionMode}
                       onClick={() => {
                         setSelectionMode((s) => {
                           const next = !s;
                           if (!next) setSelectedIds(new Set());
                           return next;
                         });
                       }}
                       tone={selectionMode ? "primary" : "neutral"}
                       type="button"
                     >
                       {selectionMode ? "关闭选择" : "选择模式"}
                     </Button>
                     <Button
                       disabled={!selectionMode || visibleIds.size === 0}
                       onClick={handleSelectAll}
                       type="button"
                     >
                       {allVisibleSelected ? "取消全选" : "全选当前"}
                     </Button>
                    </div>
                  </div>
                  <div className="flex flex-wrap items-center gap-2 border-t border-[var(--line)] pt-2">
                  <span className="muted mono text-xs">{filteredSorted.length} 结果</span>
                  {selectionMode && hasSelected ? (
                    <span className="muted ml-2 text-xs">
                      <span className="mono">{selectedIds.size} 选中</span>
                      {selectedLegacyCount ? <span className="ml-2">图片库 {selectedLegacyCount}</span> : null}
                      {selectedMediaCount ? <span className="ml-2">媒体资源 {selectedMediaCount}</span> : null}
                    </span>
                  ) : null}
                </div>
              </section>

              {hasSelected ? (
                <div className="mb-3 grid gap-2">
                  <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-sm">
                   <div className="flex flex-wrap items-center gap-2">
                     <span className="font-medium">已选择 {selectedIds.size} 项</span>
                     <Button className="min-h-7 px-2 text-xs" onClick={() => { setSelectedIds(new Set()); setBulkError(null); }} type="button">
                       取消选择
                     </Button>
                   </div>
                   <div className="flex flex-wrap items-center gap-2">
                    <Button className="min-h-7 px-2 text-xs" disabled={!hasSelected} onClick={handleBulkDownloadClick} type="button">
                      批量下载
                    </Button>
                    <Button className="min-h-7 px-2 text-xs" disabled={!hasLocalSelected || !objectStorageEnabled(storageSettings)} onClick={handleBulkArchive} type="button">
                       归档到对象存储
                    </Button>
                    <Button className="min-h-7 px-2 text-xs" disabled={!hasSelected || !onBulkDeleteResources} onClick={handleBulkDeleteClick} tone="danger" type="button">
                      批量删除
                    </Button>
                    {exactlyOneImageSelected ? (
                      <div className="relative">
                        <Button
                          className="min-h-7 px-2 text-xs"
                          onClick={() => setI2iMenuOpen((o) => !o)}
                          onBlur={() => window.setTimeout(() => setI2iMenuOpen(false), 150)}
                          type="button"
                        >
                           作为参考使用 ▾
                         </Button>
                         {i2iMenuOpen ? (
                           <div className="absolute right-0 z-20 mt-1 w-56 rounded-md border border-[var(--line)] bg-[var(--surface)] p-1 shadow-lg">
                              <button className="block w-full rounded px-2 py-1.5 text-left text-sm hover:bg-[var(--surface-soft)]" onClick={handleUseForI2IReference} type="button">
                                图生图参考
                              </button>
                              <button className="block w-full rounded px-2 py-1.5 text-left text-sm text-[var(--muted)] cursor-not-allowed" disabled onClick={handleUseForMultiEdit} type="button">
                                多图编辑素材（2-3 张）
                              </button>
                              <button className="block w-full rounded px-2 py-1.5 text-left text-sm text-[var(--muted)] cursor-not-allowed" disabled onClick={handleUseForKeyframes} type="button">
                                关键帧视频素材（2-6 张）
                              </button>
                              <button className="block w-full rounded px-2 py-1.5 text-left text-sm hover:bg-[var(--surface-soft)]" onClick={handleUseForVideoReference} type="button">
                                图生视频参考
                              </button>
                           </div>
                         ) : null}
                       </div>
                     ) : (
                       <div className="relative">
                         <Button
                           className="min-h-7 px-2 text-xs"
                           disabled={numSelectedImages < 1}
                           onClick={() => setI2iMenuOpen((o) => !o)}
                           onBlur={() => window.setTimeout(() => setI2iMenuOpen(false), 150)}
                           type="button"
                         >
                           作为参考使用 ▾
                         </Button>
                         {i2iMenuOpen ? (
                           <div className="absolute right-0 z-20 mt-1 w-56 rounded-md border border-[var(--line)] bg-[var(--surface)] p-1 shadow-lg">
                              <button className={`block w-full rounded px-2 py-1.5 text-left text-sm ${numSelectedImages === 1 ? "hover:bg-[var(--surface-soft)]" : "text-[var(--muted)] cursor-not-allowed"}`} disabled={numSelectedImages !== 1} onClick={handleUseForI2IReference} type="button">
                                 图生图参考（1 张）
                               </button>
                               <button className={`block w-full rounded px-2 py-1.5 text-left text-sm ${numSelectedImages >= 2 && numSelectedImages <= 3 ? "hover:bg-[var(--surface-soft)]" : "text-[var(--muted)] cursor-not-allowed"}`} disabled={numSelectedImages < 2 || numSelectedImages > 3} onClick={handleUseForMultiEdit} type="button">
                                 多图编辑素材（2-3 张）
                               </button>
                               <button className={`block w-full rounded px-2 py-1.5 text-left text-sm ${numSelectedImages >= 2 && numSelectedImages <= 6 ? "hover:bg-[var(--surface-soft)]" : "text-[var(--muted)] cursor-not-allowed"}`} disabled={numSelectedImages < 2 || numSelectedImages > 6} onClick={handleUseForKeyframes} type="button">
                                 关键帧视频素材（2-6 张）
                               </button>
                               <button className={`block w-full rounded px-2 py-1.5 text-left text-sm ${numSelectedImages === 1 ? "hover:bg-[var(--surface-soft)]" : "text-[var(--muted)] cursor-not-allowed"}`} disabled={numSelectedImages !== 1} onClick={handleUseForVideoReference} type="button">
                                 图生视频参考（1 张）
                               </button>
                          </div>
                        ) : null}
                      </div>
                     )}
                   </div>
                 </div>
                 {bulkError ? (
                   <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] px-3 py-2 text-xs text-[var(--danger)]">
                     <div className="flex items-center gap-2">
                       <span className="font-medium">失败</span>
                       <span className="mono">{bulkError.failedCount} 项未处理</span>
                       <span>·</span>
                       <span className="line-clamp-2 break-all">{bulkError.summary}</span>
                     </div>
                     <Button className="min-h-6 px-2 text-[11px] border border-[rgba(207,31,50,0.3)] hover:bg-[rgba(207,31,50,0.08)]" onClick={() => setBulkError(null)} type="button">
                       清除
                     </Button>
                   </div>
                 ) : null}
                </div>
              ) : null}

              {!privateScope && uploadOpen ? <LibraryUploadPanel busy={busy === "upload"} file={uploadFile} onFileChange={setUploadFile} onSubmit={submitUpload} /> : null}
              {filteredSorted.length ? (
                <div className="grid grid-cols-[repeat(auto-fill,minmax(180px,1fr))] gap-3">
                  {filteredSorted.map((item) => {
                    if (item.kind === "legacy") {
                      const asset = item.data as ImageAsset;
                       const selected = asset.id === selectedLegacyId;
                       const bulkSelected = selectedIds.has(asset.id);
                       return (
                        <article className={`group relative grid min-w-0 gap-2 rounded-lg border bg-[var(--surface)] p-2 transition ${bulkSelected ? "ring-2 ring-[var(--accent)]/70" : ""} ${selected ? "border-[var(--accent)] shadow-[inset_2px_0_0_var(--accent)]" : "border-[var(--line)] hover:border-[var(--line-strong)]"}`} key={asset.id}>
                          <button
                            className="relative block aspect-square overflow-hidden rounded-md border border-[var(--line)] bg-[var(--surface-soft)] text-left"
                            onClick={() => {
                              onSelect(asset);
                              setViewer(asset);
                            }}
                            type="button"
                          >
                            {asset.url ? <img alt={assetTitle(asset)} className="h-full w-full object-cover transition group-hover:scale-[1.01]" decoding="async" height={asset.height || 512} loading="lazy" src={asset.url} width={asset.width || 512} /> : <div className="grid h-full place-items-center text-xs text-[var(--muted)]">no image</div>}
                            {selectionMode ? (
                              <label
                                className="absolute top-2 left-2 inline-flex items-center gap-1 rounded-md border border-[var(--line)] bg-[var(--surface)]/90 px-2 py-1 text-xs shadow-sm"
                                onClick={(e) => e.stopPropagation()}
                              >
                                <input
                                  checked={bulkSelected}
                                  onChange={() => toggleSelected(asset.id)}
                                  type="checkbox"
                                />
                              </label>
                            ) : null}
                            <span className="absolute top-2 right-2 flex gap-1">
                              {asset.private ? <Pill tone="warn">私密</Pill> : null}
                            </span>
                          </button>
                          <div className="min-w-0">
                            <strong className="line-clamp-2 text-sm leading-snug">{assetTitle(asset)}</strong>
                            <span className="muted mt-1 block truncate text-xs">{imageAssetTypeLabel(asset.assetType)} · {formatDate(asset.createdAt) || "-"}</span>
                          </div>
                          <div className="muted mono flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-xs">
                            <span>{asset.width && asset.height ? `${asset.width}x${asset.height}` : "unknown"}</span>
                            <span>{formatBytes(asset.sizeBytes)}</span>
                          </div>
                          <div className="invisible absolute right-2 bottom-2 z-10 flex gap-1 group-hover:visible">
                            <div className="flex gap-1 rounded-md border border-[var(--line)] bg-[var(--surface)] p-1 shadow-md">
                              {asset.jobId ? (
                                <button className="rounded px-1.5 py-0.5 text-xs hover:bg-[var(--surface-soft)]" onClick={() => onOpenJob?.(asset.jobId!, "legacy")} type="button">
                                  打开任务
                                </button>
                              ) : null}
                              <button className="rounded px-1.5 py-0.5 text-xs hover:bg-[var(--surface-soft)]" onClick={() => { onSelect(asset); setViewer(asset); }} type="button">
                                预览
                              </button>
                            </div>
                          </div>
                        </article>
                      );
                    } else {
                      const asset = item.data as MediaAsset;
                      const isVideo = asset.mediaType === "video";
                      const providerLabel = PROVIDERS.find((p) => p.id === asset.provider)?.label || asset.provider;
                      const isSelected = selectedMediaId === asset.id;
                      const bulkSelected = selectedIds.has(asset.id);
                       const isArchived = asset.status === "archived" || Boolean(asset.archivedAt);
                       return (
                        <article className={`group relative grid min-w-0 gap-2 rounded-lg border bg-[var(--surface)] p-2 transition ${bulkSelected ? "ring-2 ring-[var(--accent)]/70" : ""} ${isSelected ? "border-[var(--accent)] shadow-[inset_2px_0_0_var(--accent)]" : "border-[var(--line)] hover:border-[var(--line-strong)]"}`} key={asset.id}>
                          <button
                            className={`relative block ${isVideo ? "aspect-video" : "aspect-square"} overflow-hidden rounded-md border border-[var(--line)] bg-[var(--surface-soft)] text-left`}
                            onClick={() => {
                              onSelectMedia?.(asset);
                              setMediaViewer(asset);
                            }}
                            type="button"
                          >
                            {isVideo ? (
                              (asset.url || asset.downloadUrl) ? (
                                <video className="h-full w-full object-cover" height={asset.height || 360} preload="metadata" src={asset.url || asset.downloadUrl || ""} width={asset.width || 640} />
                              ) : <div className="grid h-full place-items-center text-xs text-[var(--muted)]">video pending</div>
                            ) : (asset.url || asset.downloadUrl) ? (
                              <img alt={asset.promptPreview || "media"} className="h-full w-full object-cover transition group-hover:scale-[1.01]" decoding="async" height={asset.height || 512} loading="lazy" src={asset.url || asset.downloadUrl || ""} width={asset.width || 512} />
                            ) : <div className="grid h-full place-items-center text-xs text-[var(--muted)]">no image</div>}
                            {selectionMode ? (
                              <label
                                className="absolute top-2 left-2 inline-flex items-center gap-1 rounded-md border border-[var(--line)] bg-[var(--surface)]/90 px-2 py-1 text-xs shadow-sm"
                                onClick={(e) => e.stopPropagation()}
                              >
                                <input
                                  checked={bulkSelected}
                                  onChange={() => toggleSelected(asset.id)}
                                  type="checkbox"
                                />
                              </label>
                            ) : null}
                            <span className="absolute top-2 right-2 flex gap-1">
                              <span className={`pill pill-xs ${isArchived ? "pill-warn" : "pill-muted"}`}>{isVideo ? "VIDEO" : "IMG"}</span>
                              {isArchived ? <span className="pill pill-xs pill-warn">ARCH</span> : null}
                              {asset.private ? <Pill tone="warn">私密</Pill> : null}
                            </span>
                          </button>
                          <div className="min-w-0">
                            <strong className="line-clamp-2 text-sm leading-snug">{asset.promptPreview || asset.jobId || asset.id}</strong>
                            <span className="muted mt-1 block truncate text-xs mono">{shortHash(asset.id)} · {formatDate(asset.createdAt) || "-"}</span>
                          </div>
                          <div className="muted mono flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-xs">
                            <span>{asset.width && asset.height ? `${asset.width}x${asset.height}` : "unknown"}</span>
                            <span>{formatBytes(asset.sizeBytes || 0)}</span>
                            {isVideo && asset.durationSeconds ? <span>{asset.durationSeconds}s</span> : null}
                          </div>
                          <div className="invisible absolute right-2 bottom-2 z-10 flex gap-1 group-hover:visible">
                            <div className="flex gap-1 rounded-md border border-[var(--line)] bg-[var(--surface)] p-1 shadow-md">
                              {asset.jobId ? (
                                <button className="rounded px-1.5 py-0.5 text-xs hover:bg-[var(--surface-soft)]" onClick={() => onOpenJob?.(asset.jobId!, "media")} type="button">
                                  打开任务
                                </button>
                              ) : null}
                              <button className="rounded px-1.5 py-0.5 text-xs hover:bg-[var(--surface-soft)]" onClick={() => { onSelectMedia?.(asset); setMediaViewer(asset); }} type="button">
                                预览
                              </button>
                            </div>
                          </div>
                        </article>
                      );
                    }
                  })}
                </div>
              ) : (
                <EmptyState title={privateScope ? "暂无私密资源" : "暂无资源"} body={privateScope ? "在资源库中将资产设为私密后，这里会展示。" : "生成图片/视频、或手动上传参考图后，资源会自动进入这里。"} />
              )}
              <PaginationFooter pagination={pagination} visibleCount={filteredSorted.length} />
          </>
        )}
        {!privateScope && filteredSorted.length === 0 ? (
           <div className="mt-2 flex items-center justify-center gap-2">
             <Button onClick={onGoToGenerate}>去生成</Button>
             <Button onClick={onGoToSettings}>去设置</Button>
           </div>
         ) : null}
         {privateScope && privateUnlocked && privateExpiresAt ? <p className="muted mt-3 mb-0 text-xs">解锁有效至 {formatDate(privateExpiresAt)}</p> : null}
       </Panel>
       {viewer ? (
         <ImageViewer
           asset={viewer}
           assets={filteredLegacyList}
           index={viewerLegacyIndex}
           onArchive={onArchive}
           onClose={() => setViewer(null)}
           onDelete={(asset) => { onDelete(asset); setViewer(null); }}
           onDownload={triggerDownload}
           onNext={() => {
             const next = viewerLegacyIndex + 1;
             if (next >= 0 && next < filteredLegacyList.length) {
               setViewer(filteredLegacyList[next]);
               onSelect(filteredLegacyList[next]);
             }
           }}
           onOpenJob={(asset) => asset.jobId ? onOpenJob?.(asset.jobId, "legacy") : undefined}
           onPrev={() => {
             const prev = viewerLegacyIndex - 1;
             if (prev >= 0 && prev < filteredLegacyList.length) {
               setViewer(filteredLegacyList[prev]);
               onSelect(filteredLegacyList[prev]);
             }
           }}
           onUseForImage={(asset) => { setViewer(null); onUseForImage(asset); }}
           storageSettings={storageSettings}
         />
       ) : null}
      {mediaViewer ? (
        <MediaAssetViewer
          asset={mediaViewer}
          assets={filteredMediaList}
          index={viewerMediaIndex}
          onArchive={(asset) => { onArchiveMedia?.(asset); setMediaViewer(null); }}
          onClose={() => setMediaViewer(null)}
          onDelete={(asset) => { onDeleteMedia?.(asset); setMediaViewer(null); }}
          onDownload={triggerMediaDownload}
          onMarkPrivate={(asset, nextPrivate) => { onMarkPrivateMedia?.(asset, nextPrivate); }}
          onNext={() => {
            const next = viewerMediaIndex + 1;
            if (next >= 0 && next < filteredMediaList.length) {
              setMediaViewer(filteredMediaList[next]);
              onSelectMedia?.(filteredMediaList[next]);
            }
          }}
          onOpenJob={(asset) => asset.jobId ? onOpenJob?.(asset.jobId, "media") : undefined}
          onPrev={() => {
            const prev = viewerMediaIndex - 1;
            if (prev >= 0 && prev < filteredMediaList.length) {
              setMediaViewer(filteredMediaList[prev]);
              onSelectMedia?.(filteredMediaList[prev]);
            }
          }}
          onUseForImage={(asset) => { setMediaViewer(null); onUseMediaForImage?.(asset); }}
          storageSettings={storageSettings}
        />
      ) : null}
    </>
  );
}

function LibraryUploadPanel({
  busy,
  file,
  onFileChange,
  onSubmit,
}: {
  busy: boolean;
  file: File | null;
  onFileChange: (file: File | null) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}) {
  return (
    <form className="flex items-end justify-between gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 max-md:grid" onSubmit={onSubmit}>
      <Field label="手动上传" help="支持 jpeg、png、gif、webp；上传前会按内容 hash 去重。">
        <ImageDropInput key={file ? "selected" : "empty"} label="上传到图片库" onFiles={(files) => onFileChange(files[0] || null)} />
      </Field>
      <div className="flex items-center gap-3">
        {file ? <span className="muted max-w-64 truncate text-xs">{file.name}</span> : null}
        <Button disabled={busy || !file} tone="primary" type="submit">
          {busy ? "上传中" : "上传"}
        </Button>
      </div>
    </form>
  );
}

function LibraryScopeSwitch({ active, onChange }: { active: ImageLibraryScope; onChange: (scope: ImageLibraryScope) => void }) {
  const items: Array<{ id: ImageLibraryScope; label: string }> = [
    { id: "public", label: "资源库" },
    { id: "private", label: "私密收藏夹" },
  ];
  return (
    <div className="mb-4 flex w-fit overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-1 max-sm:w-full">
      {items.map((item) => (
        <button className={`min-h-8 rounded-md px-3 text-sm transition max-sm:flex-1 ${active === item.id ? "bg-[var(--surface)] text-[var(--text)] shadow-sm" : "text-[var(--muted-strong)] hover:bg-[var(--surface)]"}`} key={item.id} onClick={() => onChange(item.id)} type="button">
          {item.label}
        </button>
      ))}
    </div>
  );
}

function PrivateUnlockPanel({ busy, onUnlock }: { busy: boolean; onUnlock: (password: string) => Promise<void> }) {
  const [password, setPassword] = useState("");

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    await onUnlock(password);
    setPassword("");
  }

  return (
    <form className="grid max-w-md gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-4" onSubmit={(event) => void submit(event)}>
      <Field label="Owner 密码" help="使用当前控制台 owner 登录密码解锁。">
        <input autoComplete="current-password" className="input mono" name="images_private_owner_password" onChange={(event) => setPassword(event.target.value)} required type="password" value={password} />
      </Field>
      <div>
        <Button disabled={busy || password.length < 1} tone="primary" type="submit">
          {busy ? "解锁中" : "解锁私密收藏夹"}
        </Button>
      </div>
    </form>
  );
}

function LibraryMetric({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <span className="muted text-xs">{label}</span>
      <strong className="mono mt-2 block text-lg">{value}</strong>
    </div>
  );
}

function ImageViewer({
  asset,
  assets = [],
  index = -1,
  onArchive,
  onClose,
  onDelete,
  onDownload,
  onNext,
  onOpenJob,
  onPrev,
  onUseForImage,
  storageSettings,
}: {
  asset: ImageAsset;
  assets?: ImageAsset[];
  index?: number;
  onArchive?: (asset: ImageAsset) => void;
  onClose: () => void;
  onDelete?: (asset: ImageAsset) => void;
  onDownload?: (asset: ImageAsset) => void;
  onNext?: () => void;
  onOpenJob?: (asset: ImageAsset) => void;
  onPrev?: () => void;
  onUseForImage?: (asset: ImageAsset) => void;
  storageSettings?: ImageStorageSettings;
}) {
  const titleId = useId();
  const dialogRef = useRef<HTMLDivElement | null>(null);
  const closeButtonRef = useRef<HTMLButtonElement | null>(null);
  const [zoomMode, setZoomMode] = useState<"fit" | "actual">("fit");
  const [zoomLevel, setZoomLevel] = useState<number>(1);

  useEffect(() => {
    const previousFocus = document.activeElement;
    closeButtonRef.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
        return;
      }
      if (event.key === "ArrowLeft") {
        onPrev?.();
        return;
      }
      if (event.key === "ArrowRight") {
        onNext?.();
        return;
      }
      if (event.key === "0") {
        setZoomMode("fit");
        setZoomLevel(1);
        return;
      }
      if (event.key === "1") {
        setZoomMode("actual");
        setZoomLevel(1);
        return;
      }
      if (event.key === "+" || event.key === "=") {
        setZoomMode("actual");
        setZoomLevel((z) => Math.min(8, Number((z + 0.25).toFixed(2))));
        return;
      }
      if (event.key === "-" || event.key === "_") {
        setZoomMode("actual");
        setZoomLevel((z) => Math.max(0.25, Number((z - 0.25).toFixed(2))));
        return;
      }
      if (event.key !== "Tab" || !dialogRef.current) return;
      const focusable = Array.from(
        dialogRef.current.querySelectorAll<HTMLElement>("button, [href], input, select, textarea, [tabindex]:not([tabindex='-1'])"),
      ).filter((element) => !element.hasAttribute("disabled"));
      if (!focusable.length) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
      if (previousFocus instanceof HTMLElement) previousFocus.focus();
    };
  }, [onClose, onNext, onPrev]);

  const zoomStyle = zoomMode === "fit" ? {} : { transform: `scale(${zoomLevel})`, transformOrigin: "top left" };
  const showNav = assets.length > 0 && index >= 0;

  return (
    <div className="fixed inset-0 z-40 grid place-items-center overscroll-contain bg-[rgba(16,18,22,0.62)] p-4" onClick={onClose}>
      <div aria-labelledby={titleId} aria-modal="true" className="grid max-h-[92dvh] w-full max-w-6xl overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)] max-lg:grid-cols-1" onClick={(event) => event.stopPropagation()} ref={dialogRef} role="dialog">
        <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 border-b border-[var(--line)] px-3 py-2">
          <div className="flex min-w-0 items-center gap-2">
            <Button aria-label="上一张" className="min-h-8 px-2.5 text-sm" disabled={!showNav || index <= 0} onClick={onPrev} type="button">
              ‹
            </Button>
            <Button aria-label="下一张" className="min-h-8 px-2.5 text-sm" disabled={!showNav || index >= assets.length - 1} onClick={onNext} type="button">
              ›
            </Button>
            {showNav ? (
              <span className="muted mono min-w-0 truncate text-xs">{index + 1} / {assets.length}</span>
            ) : <span className="muted mono text-xs">-</span>}
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            <Button
              aria-pressed={zoomMode === "fit"}
              className="min-h-8 px-2 text-xs"
              onClick={() => { setZoomMode("fit"); setZoomLevel(1); }}
              tone={zoomMode === "fit" ? "primary" : "neutral"}
              type="button"
            >
              适应
            </Button>
            <Button
              aria-pressed={zoomMode === "actual"}
              className="min-h-8 px-2 text-xs"
              onClick={() => { setZoomMode("actual"); setZoomLevel(1); }}
              tone={zoomMode === "actual" ? "primary" : "neutral"}
              type="button"
            >
              原始
            </Button>
            <Button aria-label="缩小" className="min-h-8 px-2 text-xs" onClick={() => { setZoomMode("actual"); setZoomLevel((z) => Math.max(0.25, Number((z - 0.25).toFixed(2)))); }} type="button">
              −
            </Button>
            <span className="mono min-w-[48px] text-center text-xs">{zoomMode === "fit" ? "fit" : `${Math.round(zoomLevel * 100)}%`}</span>
            <Button aria-label="放大" className="min-h-8 px-2 text-xs" onClick={() => { setZoomMode("actual"); setZoomLevel((z) => Math.min(8, Number((z + 0.25).toFixed(2)))); }} type="button">
              ＋
            </Button>
            <div className="mx-1 h-5 w-px bg-[var(--line)]" />
             <a
               className="button min-h-8 px-2 text-xs"
               download
               href={assetDownloadURL(asset)}
               type="button"
             >
               下载
             </a>
             {asset.url || asset.downloadUrl ? (
               <Button className="min-h-8 px-2 text-xs" onClick={() => {
                 const u = asset.downloadUrl || asset.url || "";
                 navigator.clipboard?.writeText(u);
               }} type="button">
                 复制 URL
               </Button>
             ) : null}
            <Button aria-label="关闭图片预览" className="min-h-8 px-2 text-xs" onClick={onClose} ref={closeButtonRef}>
              关闭
            </Button>
          </div>
        </div>
        <div className="grid max-h-[calc(92dvh-44px)] grid-cols-[minmax(0,1fr)_320px] max-lg:grid-cols-1">
          <div className={`min-h-[320px] overflow-auto bg-[var(--surface-soft)] p-4 ${zoomMode === "actual" ? "" : "grid place-items-center"}`}>
            {asset.url ? (
              <img alt={assetTitle(asset)} className={`${zoomMode === "fit" ? "max-h-[76dvh] max-w-full rounded-lg border border-[var(--line)] object-contain" : "rounded-lg border border-[var(--line)] object-contain"}`} decoding="async" height={asset.height || 1024} src={asset.url} style={zoomStyle} width={asset.width || 1024} />
            ) : <div className="text-sm text-[var(--muted)]">no image</div>}
          </div>
          <aside className="grid content-start gap-3 border-l border-[var(--line)] p-4 max-lg:border-l-0 max-lg:border-t">
            <div className="min-w-0">
              <h3 className="m-0 break-words text-sm font-semibold" id={titleId}>{assetTitle(asset)}</h3>
              <p className="muted mt-1 mb-0 text-xs">{imageAssetTypeLabel(asset.assetType)}</p>
            </div>
            <ContextList items={assetMetadata(asset)} />
             <div className="flex flex-wrap gap-2 border-t border-[var(--line)] pt-3">
               <a
                 className="button min-h-8 px-2 text-xs"
                 download
                 href={assetDownloadURL(asset)}
               >
                 下载
               </a>
             </div>
           </aside>
         </div>
       </div>
     </div>
   );
}

function MediaAssetViewer({
  asset,
  assets = [],
  index = -1,
  onArchive,
  onClose,
  onDelete,
  onDownload,
  onMarkPrivate,
  onNext,
  onOpenJob,
  onPrev,
  onUseForImage,
  storageSettings,
}: {
  asset: MediaAsset;
  assets?: MediaAsset[];
  index?: number;
  onArchive?: (asset: MediaAsset) => void;
  onClose: () => void;
  onDelete?: (asset: MediaAsset) => void;
  onDownload?: (asset: MediaAsset) => void;
  onMarkPrivate?: (asset: MediaAsset, nextPrivate: boolean) => void;
  onNext?: () => void;
  onOpenJob?: (asset: MediaAsset) => void;
  onPrev?: () => void;
  onUseForImage?: (asset: MediaAsset) => void;
  storageSettings?: ImageStorageSettings;
}) {
  const titleId = useId();
  const dialogRef = useRef<HTMLDivElement | null>(null);
  const closeButtonRef = useRef<HTMLButtonElement | null>(null);
  const isVideo = asset.mediaType === "video";
  const providerLabel = PROVIDERS.find((p) => p.id === asset.provider)?.label || asset.provider || "-";
  const url = asset.url || asset.downloadUrl || "";
  const [zoomMode, setZoomMode] = useState<"fit" | "actual">("fit");
  const [zoomLevel, setZoomLevel] = useState<number>(1);

  useEffect(() => {
    const previousFocus = document.activeElement;
    closeButtonRef.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
        return;
      }
      if (event.key === "ArrowLeft") {
        onPrev?.();
        return;
      }
      if (event.key === "ArrowRight") {
        onNext?.();
        return;
      }
      if (!isVideo) {
        if (event.key === "0") {
          setZoomMode("fit");
          setZoomLevel(1);
          return;
        }
        if (event.key === "1") {
          setZoomMode("actual");
          setZoomLevel(1);
          return;
        }
        if (event.key === "+" || event.key === "=") {
          setZoomMode("actual");
          setZoomLevel((z) => Math.min(8, Number((z + 0.25).toFixed(2))));
          return;
        }
        if (event.key === "-" || event.key === "_") {
          setZoomMode("actual");
          setZoomLevel((z) => Math.max(0.25, Number((z - 0.25).toFixed(2))));
          return;
        }
      }
      if (event.key !== "Tab" || !dialogRef.current) return;
      const focusable = Array.from(
        dialogRef.current.querySelectorAll<HTMLElement>("button, [href], input, select, textarea, [tabindex]:not([tabindex='-1'])"),
      ).filter((element) => !element.hasAttribute("disabled"));
      if (!focusable.length) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
      if (previousFocus instanceof HTMLElement) previousFocus.focus();
    };
  }, [onClose, onNext, onPrev, isVideo]);

  const zoomStyle = zoomMode === "fit" ? {} : { transform: `scale(${zoomLevel})`, transformOrigin: "top left" };
  const showNav = assets.length > 0 && index >= 0;

  return (
    <div className="fixed inset-0 z-40 grid place-items-center overscroll-contain bg-[rgba(16,18,22,0.62)] p-4" onClick={onClose}>
      <div aria-labelledby={titleId} aria-modal="true" className="grid max-h-[92dvh] w-full max-w-6xl overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)] max-lg:grid-cols-1" onClick={(event) => event.stopPropagation()} ref={dialogRef} role="dialog">
        <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 border-b border-[var(--line)] px-3 py-2">
          <div className="flex min-w-0 items-center gap-2">
            <Button aria-label="上一条" className="min-h-8 px-2.5 text-sm" disabled={!showNav || index <= 0} onClick={onPrev} type="button">
              ‹
            </Button>
            <Button aria-label="下一条" className="min-h-8 px-2.5 text-sm" disabled={!showNav || index >= assets.length - 1} onClick={onNext} type="button">
              ›
            </Button>
            {showNav ? (
              <span className="muted mono min-w-0 truncate text-xs">{index + 1} / {assets.length}</span>
            ) : <span className="muted mono text-xs">-</span>}
          </div>
           <div className="flex flex-wrap items-center justify-end gap-2">
              {!isVideo ? (
                <>
                  <Button
                    aria-pressed={zoomMode === "fit"}
                    className="min-h-8 px-2 text-xs"
                    onClick={() => { setZoomMode("fit"); setZoomLevel(1); }}
                    tone={zoomMode === "fit" ? "primary" : "neutral"}
                    type="button"
                  >
                    适应
                  </Button>
                  <Button
                    aria-pressed={zoomMode === "actual"}
                    className="min-h-8 px-2 text-xs"
                    onClick={() => { setZoomMode("actual"); setZoomLevel(1); }}
                    tone={zoomMode === "actual" ? "primary" : "neutral"}
                    type="button"
                  >
                    原始
                  </Button>
                  <Button aria-label="缩小" className="min-h-8 px-2 text-xs" onClick={() => { setZoomMode("actual"); setZoomLevel((z) => Math.max(0.25, Number((z - 0.25).toFixed(2)))); }} type="button">
                    −
                  </Button>
                  <span className="mono min-w-[48px] text-center text-xs">{zoomMode === "fit" ? "fit" : `${Math.round(zoomLevel * 100)}%`}</span>
                  <Button aria-label="放大" className="min-h-8 px-2 text-xs" onClick={() => { setZoomMode("actual"); setZoomLevel((z) => Math.min(8, Number((z + 0.25).toFixed(2)))); }} type="button">
                    ＋
                  </Button>
                  <div className="mx-1 h-5 w-px bg-[var(--line)]" />
                </>
              ) : null}
               <a
                 className="button min-h-8 px-2 text-xs"
                 download
                 href={asset.downloadUrl || asset.url || ""}
                 type="button"
               >
                 下载
               </a>
              {url ? (
                <Button className="min-h-8 px-2 text-xs" onClick={() => navigator.clipboard?.writeText(url)} type="button">
                  复制 URL
                </Button>
              ) : null}
              <Button aria-label="关闭预览" className="min-h-8 px-2 text-xs" onClick={onClose} ref={closeButtonRef}>
                关闭
              </Button>
           </div>
        </div>
        <div className="grid max-h-[calc(92dvh-44px)] grid-cols-[minmax(0,1fr)_320px] max-lg:grid-cols-1">
          <div className={`min-h-[320px] bg-[var(--surface-soft)] p-4 ${!isVideo && zoomMode === "fit" ? "grid place-items-center" : "overflow-auto"} ${isVideo ? "grid place-items-center overflow-auto" : ""}`}>
            {isVideo ? (
              url ? <video className="max-h-[76dvh] max-w-full rounded-lg border border-[var(--line)]" controls height={asset.height || 576} src={url} width={asset.width || 1024} /> : <div className="text-sm text-[var(--muted)]">no video</div>
            ) : url ? (
              <img alt={asset.promptPreview || "media"} className={`${zoomMode === "fit" ? "max-h-[76dvh] max-w-full rounded-lg border border-[var(--line)] object-contain" : "rounded-lg border border-[var(--line)] object-contain"}`} decoding="async" height={asset.height || 1024} src={url} style={zoomStyle} width={asset.width || 1024} />
            ) : <div className="text-sm text-[var(--muted)]">no image</div>}
          </div>
          <aside className="grid content-start gap-3 border-l border-[var(--line)] p-4 max-lg:border-l-0 max-lg:border-t">
            <div className="min-w-0">
              <h3 className="m-0 break-words text-sm font-semibold" id={titleId}>{asset.originalFilename || asset.promptPreview || asset.jobId || asset.id}</h3>
              <p className="muted mt-1 mb-0 text-xs">{isVideo ? "视频资源" : "图片资源"} · {providerLabel}</p>
            </div>
            <ContextList items={mediaAssetMetadata(asset)} />
             <div className="flex flex-wrap gap-2 border-t border-[var(--line)] pt-3">
               {url ? (
                 <>
                   <a
                     className="button min-h-8 px-2 text-xs"
                     download
                     href={asset.downloadUrl || asset.url || ""}
                   >
                     下载
                   </a>
                   <a className="button min-h-8 px-2 text-xs" href={url} rel="noreferrer" target="_blank">
                     新标签页打开
                   </a>
                 </>
               ) : null}
             </div>
          </aside>
        </div>
      </div>
    </div>
  );
}

export function ProviderSettingsPanel({
  busy,
  settings,
  onSave,
}: {
  busy: boolean;
  settings: ImageProviderSettings;
  onSave: (settings: ImageSettingsDraft) => Promise<void>;
}) {
  const [draft, setDraft] = useState<ImageSettingsDraft>({ ...defaultImageSettings(), ...settings, xaiApiKey: "", clearApiKey: false });

  useEffect(() => {
    setDraft((current) => ({ ...current, ...defaultImageSettings(), ...settings, xaiApiKey: "", clearApiKey: false }));
  }, [settings]);

  return (
    <Panel
      actions={
        <Button disabled={busy} onClick={() => void onSave(draft)} tone="primary">
          保存
        </Button>
      }
      subtitle="供应商设置属于多媒体模块，不进入全局运行设置。"
      title="供应商设置"
    >
      <div className="grid gap-4">
        <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
          <Field label="供应商">
             <input className="input mono" disabled value={draft.provider} />
          </Field>
          <Field label="密钥状态">
             <input className="input mono" disabled value={draft.hasApiKey ? draft.maskedApiKey || "configured" : "未配置"} />
          </Field>
        </div>
        <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 max-md:grid-cols-1">
          <Field label="xAI 密钥" help="留空表示不修改现有 key；清除时不会在审计中写入明文。">
             <input autoComplete="new-password" className="input mono" name="images_xai_api_key" onChange={(event) => updateDraft("xaiApiKey", event.target.value)} spellCheck={false} type="password" value={draft.xaiApiKey} />
           </Field>
           <div className="flex min-h-9 items-end pb-2">
             <CheckLabel
               checked={draft.clearApiKey}
               onChange={(checked) => updateDraft("clearApiKey", checked)}
             >
               清除密钥
             </CheckLabel>
           </div>
        </div>

        <div className="grid grid-cols-4 gap-3 max-lg:grid-cols-2 max-md:grid-cols-1">
          <Field label="默认模型">
            <select className="select mono" onChange={(event) => updateDraft("defaultModel", event.target.value)} value={draft.defaultModel}>
              {GROK_MODEL_OPTIONS.map((model) => (
                <option key={model} value={model}>
                  {model}
                </option>
              ))}
            </select>
          </Field>
          <Field label="默认响应格式">
            <select className="select mono" onChange={(event) => updateDraft("defaultResponseFormat", event.target.value)} value={draft.defaultResponseFormat}>
              <option value="url">url</option>
              <option value="b64_json">b64_json</option>
            </select>
          </Field>
          <Field label="默认分辨率">
            <select className="select mono" onChange={(event) => updateDraft("defaultResolution", event.target.value)} value={draft.defaultResolution}>
              {RESOLUTION_OPTIONS.map((value) => (
                <option key={value || "default"} value={value}>
                  {value || "默认"}
                </option>
              ))}
            </select>
          </Field>
          <Field label="历史保留">
            <input className="input mono" max={2000} min={50} onChange={(event) => updateDraft("historyRetention", Number(event.target.value || 500))} type="number" value={draft.historyRetention} />
          </Field>
        </div>
      </div>
    </Panel>
  );

  function updateDraft<Key extends keyof ImageSettingsDraft>(key: Key, value: ImageSettingsDraft[Key]) {
    setDraft((current) => ({ ...current, [key]: value }));
  }
}

export function MediaProviderSettingsPanel({
  busy,
  legacyImageSettings,
  models,
  onSave,
  onTest,
  providers,
}: {
  busy?: string | boolean;
  legacyImageSettings?: {
    defaultModel?: string;
    defaultResponseFormat?: string;
    defaultResolution?: string;
    defaultAspectRatio?: string;
    historyRetention?: number;
  };
  models?: ModelCapability[];
  onSave: (drafts: MediaProviderSettingsDraft[]) => Promise<void>;
  onTest?: (provider: ProviderID) => Promise<void>;
  providers: ProviderStatus[];
}) {
  const [masked, setMasked] = useState(true);
  const busySaving = typeof busy === "string" && busy.startsWith("provider:");
  const busyTesting = typeof busy === "string" && busy.startsWith("provider-test:");
  const [drafts, setDrafts] = useState<MediaProviderDraftMap>(() => buildMediaProviderDrafts(providers, legacyImageSettings));

  useEffect(() => {
    setDrafts(buildMediaProviderDrafts(providers, legacyImageSettings));
  }, [
    providers,
    legacyImageSettings?.defaultModel,
    legacyImageSettings?.defaultResponseFormat,
    legacyImageSettings?.defaultResolution,
    legacyImageSettings?.defaultAspectRatio,
    legacyImageSettings?.historyRetention,
  ]);

  function updateDraft<Key extends keyof MediaProviderFormDraft>(provider: ProviderID, key: Key, value: MediaProviderFormDraft[Key]) {
    setDrafts((current) => ({
      ...current,
      [provider]: {
        ...current[provider],
        [key]: value,
      },
    }));
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const nextDrafts = PROVIDERS.map((providerInfo): MediaProviderSettingsDraft => {
      const draft = drafts[providerInfo.id];
      const defaultImageParams =
        providerInfo.id === "xai"
          ? {
              defaultResponseFormat: draft.defaultResponseFormat,
              defaultResolution: draft.defaultResolution,
              defaultAspectRatio: draft.defaultAspectRatio,
              historyRetention: normalizeHistoryRetention(draft.historyRetention, legacyImageSettings?.historyRetention || 500),
            }
          : {};

      return {
        provider: providerInfo.id,
        enabled: draft.enabled,
        apiKey: draft.clearApiKey ? "" : draft.apiKey.trim(),
        clearApiKey: draft.clearApiKey,
        updateApiKey: draft.clearApiKey || draft.apiKey.trim().length > 0,
        defaultImageModel: draft.defaultImageModel,
        defaultVideoModel: draft.defaultVideoModel,
        defaultImageParams,
        defaultVideoParams: {},
      };
    });

    await onSave(nextDrafts);
  }

  return (
    <Panel
      actions={
        <div className="flex items-center gap-2">
          <Button className="min-w-24" onClick={() => setMasked((m) => !m)} type="button">
            {masked ? "显示密钥" : "隐藏密钥"}
          </Button>
          <Button className="min-w-20" disabled={busySaving} form="mediaProviderSettingsForm" tone="primary" type="submit">
            {busySaving ? "保存中" : "保存"}
          </Button>
        </div>
      }
      subtitle="多 Provider 配置：统一管理 xAI、Agnes 等模型的密钥、默认模型和参数；自动拉取能力矩阵。"
      title="Provider 密钥与默认模型"
    >
      <form
        className="grid gap-5"
        id="mediaProviderSettingsForm"
        onSubmit={(event) => void submit(event)}
      >
        {PROVIDERS.map((p) => {
          const status = providers.find((s) => s.provider === p.id);
          const caps = (models || []).filter((m) => m.provider === p.id);
          const draft = drafts[p.id];
          const imageModels = providerImageModelOptions(p.id, caps, draft.defaultImageModel);
          const videoModels = providerVideoModelOptions(p.id, caps, draft.defaultVideoModel);
          const testingProvider = busyTesting ? busy.split(":")[1] : "";
          const isTesting = testingProvider === p.id;
          const configured = status?.hasApiKey;
          const hasError = status?.lastError;

          return (
            <fieldset className="m-0 grid min-w-0 gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={p.id}>
              <legend className="sr-only">{p.label}</legend>
              <div className="flex min-w-0 flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] pb-3">
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <strong className="text-sm">{p.label}</strong>
                  <Pill tone={configured ? "good" : "neutral"}>
                    {configured ? "已配置" : "未配置"}
                  </Pill>
                  <Pill>{p.id}</Pill>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <span className="muted text-xs">最小生成请求</span>
                  <Button
                    className="min-w-24"
                    disabled={busySaving || isTesting || !configured || draft.clearApiKey}
                    onClick={() => void onTest?.(p.id)}
                    type="button"
                  >
                    {isTesting ? "测试中" : "连接测试"}
                  </Button>
                </div>
              </div>

              <input name={`${p.id}:provider`} readOnly type="hidden" value={p.id} />

              <div className="grid grid-cols-[minmax(0,1fr)_minmax(280px,0.7fr)] gap-3 max-lg:grid-cols-1">
                <div className="grid gap-2">
                  <label className="text-xs font-semibold text-[var(--muted-strong)]" htmlFor={`media-provider-${p.id}-api-key`}>密钥</label>
                  <div className="grid grid-cols-[minmax(0,1fr)_112px] items-start gap-2 max-sm:grid-cols-1">
                    <input
                      autoComplete="off"
                      className={`input mono ${!configured ? "border-[rgba(237,141,21,0.45)]" : ""}`}
                      disabled={draft.clearApiKey}
                      id={`media-provider-${p.id}-api-key`}
                      name={`${p.id}:api_key`}
                      onChange={(event) => updateDraft(p.id, "apiKey", event.target.value)}
                      placeholder={configured && masked ? "••••••••••••" : `sk-${p.id}-...`}
                      spellCheck={false}
                      type={masked ? "password" : "text"}
                      value={draft.apiKey}
                    />
                    <CheckLabel
                      checked={draft.clearApiKey}
                      className="min-h-9 rounded-lg border border-[var(--line)] bg-[var(--surface)] px-2"
                      onChange={(checked) => {
                        updateDraft(p.id, "clearApiKey", checked);
                        if (checked) updateDraft(p.id, "apiKey", "");
                      }}
                    >
                      清除密钥
                    </CheckLabel>
                  </div>
                  <small className="muted text-xs">{p.label} 的密钥；留空表示不修改现有 key，保存后始终加密存储。</small>
                </div>
                <Field label="默认图片模型">
                  <select
                    className="select mono"
                    name={`${p.id}:default_image_model`}
                    onChange={(event) => updateDraft(p.id, "defaultImageModel", event.target.value)}
                    value={draft.defaultImageModel}
                  >
                    {imageModels.length ? (
                      imageModels.map((m) => <option key={m} value={m}>{m}</option>)
                    ) : (
                      <option value="">（保存后自动拉取）</option>
                    )}
                  </select>
                </Field>
                <Field label="默认视频模型">
                  <select
                    className="select mono"
                    disabled={videoModels.length === 0}
                    name={`${p.id}:default_video_model`}
                    onChange={(event) => updateDraft(p.id, "defaultVideoModel", event.target.value)}
                    value={draft.defaultVideoModel}
                  >
                    {videoModels.length ? (
                      videoModels.map((m) => <option key={m} value={m}>{m}</option>)
                    ) : p.id === "agnes" ? (
                      <optgroup label="预计支持">
                        <option value="agnes-video-v2.0">agnes-video-v2.0</option>
                        <option value="agnes-video-v1.2">agnes-video-v1.2</option>
                      </optgroup>
                    ) : (
                      <option value="">（该 provider 未支持视频）</option>
                    )}
                  </select>
                </Field>
              </div>

              {hasError ? (
                <Notice tone="warn">
                  上次连接测试：{status?.lastTestedAt ? `${formatDate(status.lastTestedAt)} ` : ""}{compactProviderError(hasError)}
                </Notice>
              ) : status?.lastTestedAt ? (
                <Notice>
                  上次连接测试：{formatDate(status.lastTestedAt)} 成功
                </Notice>
              ) : null}

              {p.id === "xai" ? (
                <div className="grid grid-cols-4 gap-3 border-t border-[var(--line)] pt-3 max-lg:grid-cols-2 max-md:grid-cols-1">
                  <Field label="默认响应格式">
                    <select
                      className="select mono"
                      name={`${p.id}:default_response_format`}
                      onChange={(event) => updateDraft(p.id, "defaultResponseFormat", event.target.value)}
                      value={draft.defaultResponseFormat}
                    >
                      <option value="url">url</option>
                      <option value="b64_json">b64_json</option>
                    </select>
                  </Field>
                  <Field label="默认分辨率">
                    <select
                      className="select mono"
                      name={`${p.id}:default_resolution`}
                      onChange={(event) => updateDraft(p.id, "defaultResolution", event.target.value)}
                      value={draft.defaultResolution}
                    >
                      {RESOLUTION_OPTIONS.map((value) => (
                        <option key={value || "default"} value={value}>
                          {value || "默认"}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Field label="默认比例">
                    <select
                      className="select mono"
                      name={`${p.id}:default_aspect_ratio`}
                      onChange={(event) => updateDraft(p.id, "defaultAspectRatio", event.target.value)}
                      value={draft.defaultAspectRatio}
                    >
                      {ASPECT_OPTIONS.map((value) => (
                        <option key={value || "default"} value={value}>
                          {value || "默认"}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Field label="历史保留条数">
                    <input
                      className="input mono"
                      max={2000}
                      min={50}
                      name={`${p.id}:history_retention`}
                      onChange={(event) => updateDraft(p.id, "historyRetention", Number(event.target.value || 500))}
                      type="number"
                      value={draft.historyRetention}
                    />
                  </Field>
                </div>
              ) : null}

              {caps.length > 0 ? (
                <details className="rounded-md border border-[var(--line)] bg-[var(--surface)]">
                  <summary className="flex cursor-pointer items-center justify-between gap-2 px-3 py-2 text-xs text-[var(--muted-strong)]">
                    模型能力矩阵（{caps.length} 个）
                    <span className="muted">展开</span>
                  </summary>
                  <ul className="grid max-h-40 gap-1 overflow-y-auto border-t border-[var(--line)] px-3 py-2 text-xs mono">
                    {caps.map((c) => (
                      <li key={`${c.mediaType}-${c.model}-${c.supportedModes?.join(",")}`} className="flex items-center gap-2">
                        <Pill>{c.mediaType}</Pill>
                        <span className="font-medium">{c.model}</span>
                        <span className="muted ml-1">{c.supportedModes?.join(" · ") || ""}</span>
                      </li>
                    ))}
                  </ul>
                </details>
              ) : null}
            </fieldset>
          );
        })}
      </form>
    </Panel>
  );
}

type MediaProviderFormDraft = {
  enabled: boolean;
  apiKey: string;
  clearApiKey: boolean;
  defaultImageModel: string;
  defaultVideoModel: string;
  defaultResponseFormat: string;
  defaultResolution: string;
  defaultAspectRatio: string;
  historyRetention: number;
};

type MediaProviderDraftMap = Record<ProviderID, MediaProviderFormDraft>;

function buildMediaProviderDrafts(
  providers: ProviderStatus[],
  legacyImageSettings?: {
    defaultModel?: string;
    defaultResponseFormat?: string;
    defaultResolution?: string;
    defaultAspectRatio?: string;
    historyRetention?: number;
  },
): MediaProviderDraftMap {
  const next = {} as MediaProviderDraftMap;
  for (const providerInfo of PROVIDERS) {
    const status = providers.find((item) => item.provider === providerInfo.id);
    const fallbackImageModel =
      providerInfo.id === "xai"
        ? legacyImageSettings?.defaultModel || GROK_MODEL_OPTIONS[0] || ""
        : "";
    next[providerInfo.id] = {
      enabled: status?.enabled ?? true,
      apiKey: "",
      clearApiKey: false,
      defaultImageModel: status?.defaultImageModel || fallbackImageModel,
      defaultVideoModel: status?.defaultVideoModel || "",
      defaultResponseFormat: legacyImageSettings?.defaultResponseFormat || "url",
      defaultResolution: legacyImageSettings?.defaultResolution || "",
      defaultAspectRatio: legacyImageSettings?.defaultAspectRatio || "",
      historyRetention: normalizeHistoryRetention(legacyImageSettings?.historyRetention, 500),
    };
  }
  return next;
}

function providerImageModelOptions(provider: ProviderID, capabilities: ModelCapability[], current: string): string[] {
  const discovered = capabilities.filter((item) => item.mediaType === "image").map((item) => item.model);
  const fallback = provider === "xai" ? GROK_MODEL_OPTIONS : [];
  return uniqueModelOptions([current, ...discovered, ...fallback]);
}

function providerVideoModelOptions(provider: ProviderID, capabilities: ModelCapability[], current: string): string[] {
  const discovered = capabilities.filter((item) => item.mediaType === "video").map((item) => item.model);
  const fallback = provider === "agnes" ? ["agnes-video-v2.0", "agnes-video-v1.2"] : [];
  return uniqueModelOptions([current, ...discovered, ...fallback]);
}

function uniqueModelOptions(values: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of values) {
    const normalized = String(value || "").trim();
    if (!normalized || seen.has(normalized)) continue;
    seen.add(normalized);
    out.push(normalized);
  }
  return out;
}

function normalizeHistoryRetention(value: unknown, fallback: number): number {
  const next = Number(value);
  if (!Number.isFinite(next) || next <= 0) return fallback;
  return Math.min(2000, Math.max(50, Math.round(next)));
}

function compactProviderError(value: string): string {
  const normalized = value.replace(/\s+/g, " ").trim();
  const prefix = normalized.match(/^([^:{]+ failed):\s*/i)?.[0] || "";
  const messageMatch = normalized.match(/"(?:error|message)"\s*:\s*"([^"]+)"/i);
  if (messageMatch?.[1]) {
    return `${prefix}${messageMatch[1]}`.trim();
  }
  if (normalized.length <= 96) return normalized;
  return `${normalized.slice(0, 96)}...`;
}

export function ImageStorageSettingsPanel({
  busy,
  objectProfiles,
  settings,
  onSave,
  onTest,
}: {
  busy: boolean;
  objectProfiles: ObjectStorageProfile[];
  settings: ImageStorageSettings;
  onSave: (settings: ImageStorageSettingsDraft) => Promise<void>;
  onTest: () => Promise<void>;
}) {
  const [draft, setDraft] = useState<ImageStorageSettingsDraft>({ ...defaultImageStorageSettings(), ...settings, s3AccessKeyId: "", s3SecretAccessKey: "", s3SessionToken: "", clearSecret: false });
  const legacyS3Enabled = draft.backend === "s3";
  const objectStorageEnabled = draft.backend === "object_storage";
  const canTest = legacyS3Enabled || (objectStorageEnabled && Boolean(draft.objectStorageProfileId));
  const selectedProfile = objectProfiles.find((profile) => profile.id === draft.objectStorageProfileId);

  useEffect(() => {
    setDraft((current) => ({ ...current, ...defaultImageStorageSettings(), ...settings, s3AccessKeyId: "", s3SecretAccessKey: "", s3SessionToken: "", clearSecret: false }));
  }, [settings]);

  return (
    <Panel
      actions={
        <>
          <Button disabled={busy || !canTest} onClick={() => void onTest()}>
            测试连接
          </Button>
          <Button disabled={busy} onClick={() => void onSave(draft)} tone="primary">
            保存
          </Button>
        </>
      }
      subtitle="默认本地保存；启用 S3 兼容对象存储后，生成结果和上传参考图优先写入对象存储。"
      title="图片存储"
    >
      <div className="grid gap-4">
        <div className="grid grid-cols-3 gap-3 max-lg:grid-cols-1">
          <Field label="存储后端">
            <select className="select mono" onChange={(event) => updateBackend(event.target.value)} value={draft.backend}>
              <option value="local">local</option>
              <option value="object_storage">object_storage profile</option>
              {legacyS3Enabled || settings.backend === "s3" ? <option value="s3">legacy direct s3</option> : null}
            </select>
          </Field>
          <Field label={objectStorageEnabled ? "对象存储 Profile" : legacyS3Enabled ? "旧版 S3" : "存储位置"}>
            <input className="input mono" disabled value={objectStorageEnabled ? selectedProfileLabel(selectedProfile, draft.objectStorageProfileId) : legacyS3Enabled ? legacyS3Label(draft) : "本地保存"} />
          </Field>
          <Field label="读取方式">
            <input className="input mono" disabled value="private bucket / backend proxy" />
          </Field>
        </div>

        {objectStorageEnabled ? (
          <fieldset className="m-0 grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <legend className="px-1 text-xs font-medium text-[var(--muted-strong)]">共享对象存储</legend>
            {objectProfiles.length ? null : <Notice>先在全局设置 / 对象存储 创建并测试一个配置。</Notice>}
            <div className="grid grid-cols-[minmax(0,1fr)_minmax(220px,0.5fr)] gap-3 max-lg:grid-cols-1">
              <Field label="对象存储配置" help="配置在全局设置 / 对象存储 中维护，密钥不会回显。">
                <select className="select mono" onChange={(event) => updateDraft("objectStorageProfileId", event.target.value)} value={draft.objectStorageProfileId || ""}>
                  <option value="">选择配置</option>
                  {objectProfiles.map((profile) => (
                    <option key={profile.id} value={profile.id}>
                      {(profile.name || profile.id) + " · " + profile.bucket}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="前缀">
                <input className="input mono" onChange={(event) => updateDraft("s3Prefix", event.target.value)} value={draft.s3Prefix} />
              </Field>
            </div>
            <CheckLabel
              checked={draft.fallbackToLocal}
              onChange={(checked) => updateDraft("fallbackToLocal", checked)}
            >
              写入失败回退本地
            </CheckLabel>
          </fieldset>
        ) : null}

        {legacyS3Enabled ? (
          <fieldset className="m-0 grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <legend className="px-1 text-xs font-medium text-[var(--muted-strong)]">旧版直连 S3</legend>
            <Notice>检测到历史 direct S3 配置。现有资产继续兼容读取；新凭据请在全局设置 / 对象存储 维护，然后切换到 object_storage 配置。</Notice>
            <ContextList
              items={[
                ["供应商", draft.s3ProviderLabel || "-"],
                ["Bucket", draft.s3Bucket || "-"],
                ["Region", draft.s3Region || "auto"],
                ["Endpoint", draft.s3Endpoint || "-"],
                ["Prefix", draft.s3Prefix || "-"],
                ["凭据", draft.hasS3Credentials ? draft.maskedAccessKeyId || "configured" : "未配置"],
                ["Fallback", draft.fallbackToLocal ? "写入失败回退本地" : "关闭"],
              ]}
            />
          </fieldset>
        ) : null}
      </div>
    </Panel>
  );

  function updateBackend(backend: string) {
    setDraft((current) => ({
      ...current,
      backend,
      objectStorageProfileId: backend === "object_storage" ? current.objectStorageProfileId : "",
    }));
  }

  function updateDraft<Key extends keyof ImageStorageSettingsDraft>(key: Key, value: ImageStorageSettingsDraft[Key]) {
    setDraft((current) => ({ ...current, [key]: value }));
  }
}

export function ImagesInspector({
  activeTab,
  asset,
  assets = [],
  jobs,
  libraryScope,
  mediaAsset,
  mediaAssets,
  mediaType,
  onArchive,
  onArchiveMedia,
  onDelete,
  onDeleteMedia,
  onMarkPrivate,
  onMarkPrivateMedia,
  onOpenJob,
  onUseForImage,
  onUseMediaForImage,
  prompt,
  prompts = [],
  status,
  storageSettings,
}: {
  activeTab: ImagesTab;
  asset?: ImageAsset;
  assets?: ImageAsset[];
  jobs: ImageGenerationJob[];
  libraryScope?: ImageLibraryScope;
  mediaAsset?: MediaAsset;
  mediaAssets?: MediaAsset[];
  mediaType?: MediaType;
  onArchive?: (asset: ImageAsset) => void;
  onArchiveMedia?: (asset: MediaAsset) => void;
  onDelete?: (asset: ImageAsset) => void;
  onDeleteMedia?: (asset: MediaAsset) => void;
  onMarkPrivate?: (asset: ImageAsset, nextPrivate: boolean) => void;
  onMarkPrivateMedia?: (asset: MediaAsset, nextPrivate: boolean) => void;
  onOpenJob?: (jobId: string, kind: "legacy" | "media") => void;
  onUseForImage?: (asset: ImageAsset) => void;
  onUseMediaForImage?: (asset: MediaAsset) => void;
  prompt?: ImagePrompt;
  prompts?: ImagePrompt[];
  status?: ImageStatus;
  storageSettings?: ImageStorageSettings;
}) {
  const last = jobs[0];
  const tone: Tone = status?.hasApiKey ? (status?.lastJobStatus === "failed" ? "warn" : "good") : "warn";
  const localAssets = assets.filter((item) => item.storageBackend === "local").length;
  const s3Assets = assets.filter((item) => item.storageBackend === "s3").length;
  const mediaAssetCount = (mediaAssets || []).length;
  const videoCount = (mediaAssets || []).filter((a) => a.mediaType === "video").length;
  const combinedCount = assets.length + mediaAssetCount;
  return (
    <aside className="grid content-start gap-4 border-l border-[var(--line)] bg-[var(--surface-soft)] p-4 max-xl:border-l-0 max-xl:border-t">
      <Panel title="多媒体">
        <ContextList
          items={[
            ["状态", <Pill tone={tone}>{imageStatusLabel(status)}</Pill>],
             ["供应商", status?.provider || "xai"],
             ["密钥", status?.hasApiKey ? status.maskedApiKey || "configured" : "未配置"],
            ["历史", status?.historyCount ?? jobs.length],
            ["Prompt", prompts.length],
            [libraryScope === "private" ? "私密图" : "图片库", assets.length],
            ["媒体资源", `${mediaAssetCount}（图片 ${mediaAssetCount - videoCount} / 视频 ${videoCount}）`],
            ["总资源", combinedCount],
            ["存储", imageStorageBackendLabel(storageSettings?.backend)],
            ["最近任务", last ? `${mediaModeLabel(last.mode, "image")} / ${imageJobStatusLabel(last.status)}` : "-"],
            ["错误", status?.lastError ? compactProviderError(status.lastError) : "-"],
          ]}
        />
      </Panel>
      {activeTab === "library" ? (
        <Panel title="选中资源">
          {asset ? (
            <div className="grid gap-3">
              {asset.url ? <img alt={assetTitle(asset)} className="aspect-square w-full rounded-lg border border-[var(--line)] object-cover" decoding="async" height={asset.height || 512} src={asset.url} width={asset.width || 512} /> : null}
              <ContextList items={assetMetadata(asset)} />
               <div className="flex flex-wrap gap-2">
                <Button className="min-h-8 px-2 text-xs" onClick={() => onUseForImage?.(asset)} tone="primary">
                  用于图生图
                </Button>
                 <a
                   className="button min-h-8 px-2 text-xs"
                   download
                   href={assetDownloadURL(asset)}
                 >
                   下载
                 </a>
                <Button className="min-h-8 px-2 text-xs" disabled={!canArchiveAsset(asset, storageSettings)} onClick={() => onArchive?.(asset)}>
                  归档
                </Button>
                <Button className="min-h-8 px-2 text-xs" onClick={() => onMarkPrivate?.(asset, !asset.private)}>
                  {asset.private ? "移出私密" : "设为私密"}
                </Button>
                <Button className="min-h-8 px-2 text-xs" onClick={() => onDelete?.(asset)} tone="danger">
                  删除
                </Button>
              </div>
            </div>
           ) : mediaAsset ? (
             <div className="grid gap-3">
               {mediaAsset.mediaType === "video" ? (
                 (mediaAsset.url || mediaAsset.downloadUrl) ? <video className="aspect-video w-full rounded-lg border border-[var(--line)] object-cover" controls height={mediaAsset.height || 360} src={mediaAsset.url || mediaAsset.downloadUrl || ""} width={mediaAsset.width || 640} /> : null
               ) : (mediaAsset.url || mediaAsset.downloadUrl) ? <img alt={mediaAsset.promptPreview || "media"} className="aspect-square w-full rounded-lg border border-[var(--line)] object-cover" decoding="async" height={mediaAsset.height || 512} src={mediaAsset.url || mediaAsset.downloadUrl || ""} width={mediaAsset.width || 512} /> : null}
               <ContextList items={mediaAssetMetadata(mediaAsset)} />
               <div className="flex flex-wrap gap-2">
                 {(mediaAsset.url || mediaAsset.downloadUrl) ? (
                   <>
                     <a
                       className="button min-h-8 px-2 text-xs"
                       download
                       href={mediaDownloadURL(mediaAsset)}
                     >
                       下载
                     </a>
                     <a className="button min-h-8 px-2 text-xs" href={mediaContentURL(mediaAsset)} rel="noreferrer" target="_blank">
                       打开
                     </a>
                   </>
                 ) : null}
                 {canArchiveMediaAsset(mediaAsset, storageSettings) ? (
                   <Button className="min-h-8 px-2 text-xs" onClick={() => onArchiveMedia?.(mediaAsset)}>
                     归档
                   </Button>
                 ) : null}
                  <Button className="min-h-8 px-2 text-xs" onClick={() => onMarkPrivateMedia?.(mediaAsset, !mediaAsset.private)}>
                    {mediaAsset.private ? "移出私密" : "设为私密"}
                  </Button>
                  {mediaAsset.jobId ? (
                    <Button className="min-h-8 px-2 text-xs" onClick={() => onOpenJob?.(mediaAsset.jobId!, "media")}>
                      打开任务
                    </Button>
                  ) : null}
                  {mediaAsset.mediaType !== "video" ? (
                    <Button className="min-h-8 px-2 text-xs" onClick={() => onUseMediaForImage?.(mediaAsset)}>
                      用于图生图
                    </Button>
                  ) : null}
                 <Button className="min-h-8 px-2 text-xs" onClick={() => onDeleteMedia?.(mediaAsset)} tone="danger">
                   删除
                 </Button>
               </div>
             </div>
            ) : (
              <div className="grid gap-3">
                <div className="grid grid-cols-2 gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3">
                  <LibraryMetric label="总资源" value={combinedCount} />
                  <LibraryMetric label="图片" value={combinedCount - videoCount} />
                  <LibraryMetric label="视频" value={videoCount} />
                  <LibraryMetric label="私密" value={assets.filter((a) => a.private).length + (mediaAssets || []).filter((a) => a.private).length} />
                </div>
                <EmptyState title="未选择资源" body="在资源库中选择任意图片或视频查看详情和可用操作。" />
              </div>
            )}
        </Panel>
      ) : null}
      {activeTab === "presets" ? (
        <Panel title="生成预设详情">
          {prompt ? (
            <ContextList
              items={[
                ["标题", prompt.title || "-"],
                ["模式", <Pill>{mediaModeLabel(prompt.mode, "image")}</Pill>],
                ["模型", prompt.model || "模块默认"],
                ["比例", prompt.aspectRatio || "默认"],
                ["分辨率", prompt.resolution || "默认"],
                ["数量", prompt.imageCount || 1],
                ["使用", prompt.useCount || 0],
                ["最近使用", formatDate(prompt.lastUsedAt) || "-"],
                ["标签", prompt.tags?.length ? prompt.tags.map((tag) => `#${tag}`).join(" ") : "-"],
              ]}
            />
          ) : (
            <EmptyState title="未选择生成预设" body="选择或新建一个生成预设查看参数摘要。" />
          )}
        </Panel>
      ) : null}
      {activeTab === "generate" ? (
        <Panel title="生成边界">
          <ContextList
            items={[
              ["最近任务", last ? `${mediaModeLabel(last.mode, "image")} / ${imageJobStatusLabel(last.status)}` : "-"],
              ["图片数量", "1-10 (Agnes 1-8)"],
              ["视频时长", "5s / 10s"],
              ["参考图上传", "jpeg/png/gif/webp, <=12 MB"],
              ["图片模式", "文生图 / 图生图 / 多图编辑"],
              ["视频模式", "文生视频 / 图生视频 / 多图视频 / 关键帧动画"],
              ["写入", imageStorageBackendLabel(storageSettings?.backend)],
              ["读取", "登录后经后端代理"],
            ]}
          />
        </Panel>
      ) : null}
      {activeTab === "history" ? (
        <Panel title="任务摘要">
          <ContextList
            items={[
              ["记录数", status?.historyCount ?? jobs.length],
              ["媒体资源", mediaAssetCount],
              ["最近模式", last ? mediaModeLabel(last.mode, "image") : "-"],
              ["最近状态", last ? imageJobStatusLabel(last.status) : "-"],
              ["最近模型", last?.model || "-"],
              ["最近错误", status?.lastError ? compactProviderError(status.lastError) : last?.errorMessage ? compactProviderError(last.errorMessage) : "-"],
            ]}
          />
        </Panel>
      ) : null}
      {activeTab === "settings" ? (
        <Panel title="设置边界">
          <ContextList
            items={[
              ["供应商", status?.provider || "xai"],
              ["密钥", status?.hasApiKey ? status.maskedApiKey || "configured" : "未配置"],
              ["存储", imageStorageBackendLabel(storageSettings?.backend)],
              ["本地资产", localAssets],
              ["对象存储资产", s3Assets],
              ["媒体资源", mediaAssetCount],
              ["读取策略", "private bucket / backend proxy"],
            ]}
          />
        </Panel>
      ) : null}
    </aside>
  );
}

function selectedProfileLabel(profile?: ObjectStorageProfile, id?: string): string {
  if (profile) return `${profile.name || profile.id} / ${profile.bucket}`;
  if (id) return "profile missing";
  return "未选择";
}

function legacyS3Label(settings: ImageStorageSettingsDraft): string {
  const provider = settings.s3ProviderLabel || "s3";
  const bucket = settings.s3Bucket || "bucket missing";
  return `${provider} / ${bucket}`;
}

function mediaJobTone(status: MediaGenerationJob["status"]): Tone {
  if (status === "success") return "good";
  if (status === "failed" || status === "interrupted") return "danger";
  if (status === "queued" || status === "running" || status === "provider_queued") return "warn";
  return "neutral";
}

function mediaJobStatusLabel(status: MediaGenerationJob["status"]): string {
  switch (status) {
    case "queued": return "排队中";
    case "provider_queued": return "供应商排队";
    case "running": return "生成中";
    case "success": return "成功";
    case "failed": return "失败";
    case "interrupted": return "已取消";
    default: return status;
  }
}

function mediaModeLabel(mode?: string, mediaType?: MediaType): string {
  if (mediaType === "image") {
    if (mode === "text_to_image") return "文生图";
    if (mode === "image_to_image") return "图生图";
    if (mode === "multi_image_edit") return "多图编辑";
  } else if (mediaType === "video") {
    if (mode === "text_to_video") return "文生视频";
    if (mode === "image_to_video") return "图生视频";
    if (mode === "multi_image_video") return "多图视频";
    if (mode === "keyframes") return "关键帧动画";
  }
  return mode || "未知";
}

function MediaJobCard({ job, libraryMediaAssets = [], onCopyParams, onOpenAsset, onRetry, onRestore, onSaveAsPreset, onUseAssetAsReference, targetJobId, targetJobKind, onOpenLogs }: { job: MediaGenerationJob; libraryMediaAssets?: MediaAsset[]; onCopyParams?: (job: MediaGenerationJob) => void; onOpenAsset?: (assetId: string) => void; onRetry?: (job: MediaGenerationJob) => void; onRestore?: (job: MediaGenerationJob) => void; onSaveAsPreset?: (job: MediaGenerationJob) => void; onUseAssetAsReference?: (asset: MediaAsset) => void; targetJobId?: string; targetJobKind?: "legacy" | "media"; onOpenLogs?: (job: MediaGenerationJob) => void }) {
  const tone = mediaJobTone(job.status);
  const providerLabel = PROVIDERS.find((p) => p.id === job.provider)?.label || job.provider;
  const active = job.status === "queued" || job.status === "running" || job.status === "provider_queued";
  const progressMsg = job.providerStatus || (job.status === "provider_queued" ? "排队中" : job.status === "running" ? "生成中" : "处理中");
  const promptPreview = (job.prompt || "").slice(0, 120) + ((job.prompt || "").length > 120 ? "…" : "");
  const params = job.parameters || {};
  const dataJobId = `media-${job.id}`;
  const isTarget = targetJobId && targetJobKind && `${targetJobKind}-${targetJobId}` === dataJobId;
  return (
    <article className={`grid gap-3 rounded-lg border bg-[var(--surface)] p-3 ${isTarget ? "border-[var(--accent)] shadow-[inset_2px_0_0_var(--accent)]" : "border-[var(--line)]"}`} data-job-id={dataJobId}>
      <div className="flex flex-wrap items-center gap-2">
        {isTarget ? <Pill tone="warn">目标任务</Pill> : null}
        <Pill tone={tone}>{mediaJobStatusLabel(job.status)}</Pill>
        <Pill>{job.mediaType === "video" ? "视频" : "图片"}</Pill>
        <Pill>{mediaModeLabel(job.mode, job.mediaType)}</Pill>
        <Pill>{providerLabel}</Pill>
        <Pill>{job.model}</Pill>
        <span className="muted ml-auto text-xs max-md:ml-0">{formatDate(job.completedAt || job.createdAt) || "-"}</span>
      </div>
      {active ? (
        <div className="rounded-md border border-[rgba(237,141,21,0.22)] bg-[var(--warn-soft)] p-2 text-xs">
          <div className="flex items-center gap-2">
            <span className="mono">{job.progress ?? 0}%</span>
            <span>{progressMsg}</span>
          </div>
        </div>
      ) : null}
      {promptPreview ? <p className="m-0 line-clamp-3 text-sm leading-relaxed">{promptPreview}</p> : null}
      {job.errorMessage ? (
        <div className="rounded-md border border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] p-2 text-xs text-[var(--danger)]">
          <div className="flex items-start justify-between gap-2">
            <p className="m-0 leading-relaxed line-clamp-6 break-all">{job.errorMessage}</p>
            <div className="flex flex-shrink-0 gap-1">
              <Button
                className="min-h-6 px-2 text-[11px] border border-[rgba(207,31,50,0.3)] hover:bg-[rgba(207,31,50,0.08)]"
                onClick={() => {
                  navigator.clipboard?.writeText(job.errorMessage || "");
                }}
                type="button"
              >
                复制错误
              </Button>
              {onOpenLogs ? (
                <Button
                  className="min-h-6 px-2 text-[11px] border border-[var(--line-strong)] hover:bg-[var(--surface-soft)]"
                  onClick={() => onOpenLogs(job)}
                  type="button"
                >
                  查看日志
                </Button>
              ) : null}
            </div>
          </div>
        </div>
      ) : null}
      {job.outputs?.length ? (
        <div className={`grid gap-2 ${job.mediaType === "video" ? "grid-cols-1" : "grid-cols-3 max-lg:grid-cols-2 max-sm:grid-cols-1"}`}>
          {job.outputs.map((output: MediaGenerationOutput) => {
            const asset = output.assetId ? libraryMediaAssets.find((a) => a.id === output.assetId) : undefined;
            const thumb = asset?.downloadUrl || asset?.url || "";
            const isImage = output.mediaType === "image";
            const w = (asset?.width || 512) as number;
            const h = (asset?.height || (isImage ? 512 : 288)) as number;
            let emptyReason = "资源未入库";
            if (active) emptyReason = "生成中";
            else if (job.status === "failed") emptyReason = "生成失败";
            else if (output.assetId && !asset) emptyReason = "资源已删除";
            return (
              <div className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-2" key={output.id}>
                {isImage ? (
                  thumb ? (
                    <img alt={output.revisedPrompt || "generated image"} className="aspect-square w-full rounded-md border border-[var(--line)] object-cover" decoding="async" height={h} loading="lazy" src={thumb} width={w} />
                  ) : (
                    <div className="grid aspect-square place-items-center rounded-md border border-[var(--line)] p-3 text-center text-xs text-[var(--muted)]">
                      {emptyReason}
                    </div>
                  )
                ) : thumb ? (
                  <video className="aspect-video w-full rounded-md border border-[var(--line)] object-cover" controls height={h} preload="metadata" src={thumb} width={w} />
                ) : (
                  <div className="grid aspect-video place-items-center rounded-md border border-[var(--line)] p-3 text-center text-xs text-[var(--muted)]">
                    {emptyReason}
                  </div>
                )}
                <div className="flex flex-wrap gap-2 text-xs">
                  {output.assetId ? (
                    <span className="mono">asset #{shortHash(output.assetId)}</span>
                  ) : null}
                  {output.storage ? (
                    <span className="muted mono">{output.storage}</span>
                  ) : null}
                  <span className="muted mono">{formatBytes(output.sizeBytes || 0)}</span>
                </div>
                {output.revisedPrompt ? <p className="muted m-0 text-xs leading-relaxed line-clamp-2">{output.revisedPrompt}</p> : null}
                {asset ? (
                  <div className="flex flex-wrap gap-2 border-t border-[var(--line)] pt-2">
                    {onOpenAsset ? (
                      <Button className="min-h-6 px-2 text-xs" onClick={() => onOpenAsset(asset.id)} type="button">
                        打开资源
                      </Button>
                    ) : null}
                     {onUseAssetAsReference && isImage ? (
                       <Button className="min-h-6 px-2 text-xs" onClick={() => onUseAssetAsReference(asset)} type="button">
                         作为参考使用
                       </Button>
                     ) : null}
                  </div>
                ) : null}
              </div>
            );
          })}
        </div>
      ) : null}
      {(onRetry || onRestore || onCopyParams || onSaveAsPreset) ? (
        <div className="flex flex-wrap gap-2 border-t border-[var(--line)] pt-2">
          {onRetry && job.status !== "queued" && job.status !== "running" && job.status !== "provider_queued" ? (
            <Button className="min-h-7 px-2 text-xs" onClick={() => onRetry(job)} type="button">
              重试
            </Button>
          ) : null}
          {onRestore && job.status !== "queued" && job.status !== "running" && job.status !== "provider_queued" ? (
            <Button className="min-h-7 px-2 text-xs" onClick={() => onRestore(job)} type="button">
              恢复参数
            </Button>
          ) : null}
          {onCopyParams ? (
            <Button className="min-h-7 px-2 text-xs" onClick={() => onCopyParams(job)} type="button">
              复制参数
            </Button>
          ) : null}
          {onSaveAsPreset && job.status === "success" ? (
            <Button className="min-h-7 px-2 text-xs" onClick={() => onSaveAsPreset(job)} type="button">
              保存为生成预设
            </Button>
          ) : null}
        </div>
      ) : null}
      <div className="muted mono flex flex-wrap gap-3 text-xs">
        <span>id {shortHash(job.id)}</span>
        <span>sources {job.sourceCount}</span>
        <span>outputs {job.outputCount}</span>
        {params.width && params.height ? <span>{String(params.width)}x{String(params.height)}</span> : null}
        {params.aspectRatio ? <span>{String(params.aspectRatio)}</span> : null}
        {params.durationSeconds ? <span>{String(params.durationSeconds)}s</span> : null}
        {params.fps ? <span>{String(params.fps)}fps</span> : null}
        {job.endpoint ? <span>{job.endpoint}</span> : null}
      </div>
    </article>
  );
}

function MediaJobMini({ job }: { job: MediaGenerationJob }) {
  const tone = mediaJobTone(job.status);
  return (
    <li className="flex items-center gap-2 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] px-2 py-1.5 text-xs">
      <Pill tone={tone}>{mediaJobStatusLabel(job.status)}</Pill>
      <span className="mono">{job.mediaType === "video" ? "V" : "I"}</span>
      <span className="mono truncate">{shortHash(job.id)}</span>
      <span className="muted ml-auto">{formatDate(job.createdAt)}</span>
    </li>
  );
}

function JobCard({ job, onCopyParams, onRetry, onRestore, onSaveAsPreset, onUseOutputAsReference, targetJobId, targetJobKind, onOpenLogs }: { job: ImageGenerationJob; onCopyParams?: (job: ImageGenerationJob) => void; onRetry?: (job: ImageGenerationJob) => void; onRestore?: (job: ImageGenerationJob) => void; onSaveAsPreset?: (job: ImageGenerationJob) => void; onUseOutputAsReference?: (assetId: string, url?: string) => void; targetJobId?: string; targetJobKind?: "legacy" | "media"; onOpenLogs?: (job: ImageGenerationJob) => void }) {
  const statusTone: Tone = job.status === "success" ? "good" : job.status === "failed" ? "danger" : "warn";
  const dataJobId = `legacy-${job.id}`;
  const isTarget = targetJobId && targetJobKind && `${targetJobKind}-${targetJobId}` === dataJobId;
  const active = job.status === "queued" || job.status === "running";
  return (
    <article className={`grid gap-3 rounded-lg border bg-[var(--surface)] p-3 ${isTarget ? "border-[var(--accent)] shadow-[inset_2px_0_0_var(--accent)]" : "border-[var(--line)]"}`} data-job-id={dataJobId}>
      <div className="flex flex-wrap items-center gap-2">
        {isTarget ? <Pill tone="warn">目标任务</Pill> : null}
        <Pill tone={statusTone}>{imageJobStatusLabel(job.status)}</Pill>
        <Pill>{mediaModeLabel(job.mode, "image")}</Pill>
        <Pill>{job.model || "model"}</Pill>
        {job.endpoint ? <Pill>{job.endpoint}</Pill> : null}
        <span className="muted ml-auto text-xs max-md:ml-0">{formatDate(job.completedAt || job.createdAt) || "-"}</span>
      </div>
      {job.prompt ? <p className="m-0 line-clamp-3 text-sm leading-relaxed">{job.prompt}</p> : null}
      {job.errorMessage ? (
        <div className="rounded-md border border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] p-2 text-xs text-[var(--danger)]">
          <div className="flex items-start justify-between gap-2">
             <p className="m-0 leading-relaxed line-clamp-6 break-all">{job.errorMessage}</p>
            <div className="flex flex-shrink-0 gap-1">
              <Button
                className="min-h-6 px-2 text-[11px] border border-[rgba(207,31,50,0.3)] hover:bg-[rgba(207,31,50,0.08)]"
                onClick={() => {
                  navigator.clipboard?.writeText(job.errorMessage || "");
                }}
                type="button"
              >
                复制错误
              </Button>
              {onOpenLogs ? (
                <Button
                  className="min-h-6 px-2 text-[11px] border border-[var(--line-strong)] hover:bg-[var(--surface-soft)]"
                  onClick={() => onOpenLogs(job)}
                  type="button"
                >
                  查看日志
                </Button>
              ) : null}
            </div>
          </div>
        </div>
      ) : null}
      {job.outputs?.length ? (
        <div className="grid grid-cols-3 gap-2 max-lg:grid-cols-2 max-sm:grid-cols-1">
          {job.outputs.map((output) => {
            let emptyReason = "资源未入库";
            if (active) emptyReason = "生成中";
            else if (job.status === "failed") emptyReason = "生成失败";
            else if (output.assetId && !output.url) emptyReason = "资源已删除";
            return (
            <div className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-2" key={output.id || `${job.id}-${output.slot}`}>
              {output.url ? <img alt={job.modeLabel || "generated image"} className="aspect-square w-full rounded-md border border-[var(--line)] object-cover" decoding="async" height={512} loading="lazy" src={output.url} width={512} /> : <div className="grid aspect-square place-items-center rounded-md border border-[var(--line)] p-3 text-center text-xs text-[var(--muted)]">{emptyReason}</div>}
              <div className="flex flex-wrap gap-2 text-xs">
                {output.url ? (
                  <a className="text-[var(--accent)] underline decoration-[rgba(207,77,16,0.35)] underline-offset-2" href={output.url} rel="noreferrer" target="_blank">
                    {output.storage === "local" ? "本地副本" : "打开图片"}
                  </a>
                ) : null}
                {output.remoteUrl && output.remoteUrl !== output.url ? (
                  <a className="text-[var(--muted-strong)] underline decoration-[var(--line-strong)] underline-offset-2" href={output.remoteUrl} rel="noreferrer" target="_blank">
                    原始地址
                  </a>
                ) : null}
              </div>
              {output.revisedPrompt ? <p className="muted m-0 text-xs leading-relaxed">{output.revisedPrompt}</p> : null}
               {onUseOutputAsReference && (output.assetId || output.url) ? (
                 <div className="border-t border-[var(--line)] pt-2">
                   <Button className="min-h-6 px-2 text-xs" onClick={() => onUseOutputAsReference(output.assetId || output.id || "", output.url)} type="button">
                     作为参考使用
                   </Button>
                 </div>
               ) : null}
            </div>
          )})}
         </div>
       ) : null}
       {(onRetry || onRestore || onCopyParams || onSaveAsPreset) ? (
         <div className="flex flex-wrap gap-2 border-t border-[var(--line)] pt-2">
           {onRetry && job.status !== "queued" && job.status !== "running" ? (
             <Button className="min-h-7 px-2 text-xs" onClick={() => onRetry(job)} type="button">
               重试
             </Button>
           ) : null}
           {onRestore && job.status !== "queued" && job.status !== "running" ? (
             <Button className="min-h-7 px-2 text-xs" onClick={() => onRestore(job)} type="button">
               恢复参数
             </Button>
           ) : null}
           {onCopyParams ? (
             <Button className="min-h-7 px-2 text-xs" onClick={() => onCopyParams(job)} type="button">
               复制参数
             </Button>
           ) : null}
           {onSaveAsPreset && job.status === "success" ? (
             <Button className="min-h-7 px-2 text-xs" onClick={() => onSaveAsPreset(job)} type="button">
               保存为生成预设
             </Button>
           ) : null}
         </div>
       ) : null}
       <div className="muted mono flex flex-wrap gap-3 text-xs">
         <span>id {job.id}</span>
         <span>sources {job.sourceCount || 0}</span>
         <span>n {job.imageCount || 1}</span>
         {job.resolution ? <span>{job.resolution}</span> : null}
         {job.aspectRatio ? <span>{job.aspectRatio}</span> : null}
       </div>
     </article>
   );
 }

function assetTitle(asset: ImageAsset): string {
  return asset.originalFilename || asset.revisedPromptPreview || asset.promptPreview || asset.id;
}

function assetDownloadURL(asset: ImageAsset): string {
  return asset.downloadUrl || `/api/images/library/assets/${encodeURIComponent(asset.id)}/download`;
}

function mediaContentURL(asset: MediaAsset): string {
  return `/api/images/media-assets/${encodeURIComponent(asset.id)}/content`;
}

function mediaDownloadURL(asset: MediaAsset): string {
  return `/api/images/media-assets/${encodeURIComponent(asset.id)}/download`;
}

function objectStorageEnabled(settings?: ImageStorageSettings): boolean {
  const backend = settings?.backend;
  return backend === "s3" || (backend === "object_storage" && Boolean(settings?.objectStorageProfileId));
}

function normalizeImageMode(value?: string): ImageMode {
  if (value === "image_to_image" || value === "multi_image_edit") return value;
  return "text_to_image";
}

function clampImageCount(value: number, max = 10): number {
  if (!Number.isFinite(value)) return 1;
  return Math.max(1, Math.min(max, Math.round(value)));
}

function videoSizeForAspectRatio(aspectRatio: string): { width: number; height: number } {
  switch (aspectRatio) {
    case "16:9":
      return { width: 1024, height: 576 };
    case "9:16":
      return { width: 576, height: 1024 };
    case "2:3":
    case "3:4":
      return { width: 768, height: 1152 };
    case "3:2":
    case "4:3":
      return { width: 1152, height: 768 };
    default:
      return { width: 1152, height: 768 };
  }
}

function canArchiveAsset(asset?: ImageAsset, settings?: ImageStorageSettings): boolean {
  const archivableStorage = asset?.storageBackend === "local" || asset?.storageBackend === "remote";
  return Boolean(asset?.id && archivableStorage && objectStorageEnabled(settings) && !asset.deletedAt);
}

function canArchiveMediaAsset(asset?: MediaAsset, settings?: ImageStorageSettings): boolean {
  const archivableStorage = asset?.storageBackend === "local" || asset?.storageBackend === "remote";
  return Boolean(asset?.id && archivableStorage && objectStorageEnabled(settings) && asset.status !== "deleted");
}

function assetMetadata(asset: ImageAsset): Array<[string, ReactNode]> {
  return [
    ["类型", imageAssetTypeLabel(asset.assetType)],
    ["状态", <Pill tone={asset.status === "deleted" ? "danger" : asset.lastError ? "warn" : "good"}>{asset.status || "available"}</Pill>],
    ["私密", asset.private ? <Pill tone="warn">私密收藏夹</Pill> : "-"],
    ["存储", <Pill tone={asset.storageBackend === "s3" ? "good" : "neutral"}>{imageStorageBackendLabel(asset.storageBackend)}</Pill>],
    ["尺寸", asset.width && asset.height ? `${asset.width}x${asset.height}` : "-"],
    ["大小", formatBytes(asset.sizeBytes)],
    ["MIME", asset.mimeType || "-"],
    ["模型", asset.model || "-"],
    ["Job", asset.jobId || "-"],
    ["Bucket", asset.s3Bucket || "-"],
    ["S3 Key", asset.s3Key || "-"],
    ["Checksum", shortHash(asset.checksumSha256)],
    ["私密时间", formatDate(asset.privateAt) || "-"],
    ["创建", formatDate(asset.createdAt) || "-"],
    ["错误", asset.lastError || "-"],
  ];
}

function mediaAssetMetadata(asset: MediaAsset): Array<[string, ReactNode]> {
  const providerLabel = PROVIDERS.find((p) => p.id === asset.provider)?.label || asset.provider || "-";
  return [
    ["供应商", providerLabel],
    ["模型", asset.model || "-"],
    ["类型", asset.assetType || "-"],
    ["状态", <Pill tone={asset.status === "deleted" ? "danger" : asset.lastError ? "warn" : "good"}>{asset.status || "available"}</Pill>],
    ["存储", <Pill tone={asset.storageBackend === "s3" || asset.storageBackend === "object_storage" ? "good" : "neutral"}>{imageStorageBackendLabel(asset.storageBackend)}</Pill>],
    ["私密", asset.private ? <Pill tone="warn">私密收藏夹</Pill> : "-"],
    ["尺寸", asset.width && asset.height ? `${asset.width}x${asset.height}` : "-"],
    ["大小", formatBytes(asset.sizeBytes || 0)],
    ["MIME", asset.mimeType || "-"],
    ["时长", asset.durationSeconds ? `${asset.durationSeconds}s` : "-"],
    ["帧率", asset.frameRate ? `${asset.frameRate}fps` : "-"],
    ["Job", asset.jobId || "-"],
    ["Bucket", asset.s3Bucket || "-"],
    ["S3 Key", asset.s3Key || "-"],
    ["Checksum", shortHash(asset.checksumSha256)],
    ["私密时间", formatDate(asset.privateAt) || "-"],
    ["创建", formatDate(asset.createdAt) || "-"],
    ["更新", formatDate(asset.updatedAt) || "-"],
    ["错误", asset.lastError || "-"],
  ];
}

function shortHash(value?: string): string {
  if (!value) return "-";
  return value.length <= 16 ? value : `${value.slice(0, 12)}...${value.slice(-6)}`;
}
