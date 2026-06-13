import { useEffect, useMemo, useState } from "react";
import type { AppActions } from "../app/App";
import type { ApiError, AppData, ImageAsset, ImageGenerationJob, ImagePrompt, ObjectStorageProfile } from "../app/types";
import { friendlyError } from "../api/client";
import { Button, Panel, Pill, SubTabs, useDangerConfirm } from "../components/ui";
import { defaultImageSettings, defaultImageStorageSettings, formatDate } from "../domain/labels";
import { useQueryParamState } from "../hooks/useQueryParamState";
import { GeneratePanel, HistoryPanel, ImagePromptFormState, ImageStorageSettingsPanel, ImagesInspector, ImagesTabs, LibraryPanel, MediaProviderSettingsPanel, PromptLibraryPanel } from "../images/components";
import type {
  AppliedImagePrompt,
  AssetRef,
  ImageJobResponse,
  ImageLibraryScope,
  ImagePrivateStatus,
  ImagePromptDraft,
  ImageSettingsDraft,
  ImageStorageSettingsDraft,
  ImageUploadResponse,
  ImagesTab,
  MediaAsset,
  MediaGenerationJob,
  MediaJobResponse,
  MediaMode,
  MediaProviderSettingsDraft,
  MediaType,
  ModelCapability,
  ProviderID,
  ProviderStatus,
  ProvidersStatus,
} from "../images/types";
import { DURATION_PRESETS, MEDIA_TYPES, PROVIDERS, VIDEO_MODES } from "../images/types";

const IMAGE_TAB_IDS: ImagesTab[] = ["generate", "presets", "library", "history", "settings"];
const IMAGE_CLEAR_KEYS = ["codex", "codexInbox", "codexRuntime", "gateway", "docker", "settings"];

export function ImagesView({ actions, data }: { actions: AppActions; data: AppData }) {
  const [activeTab, setActiveTab, tabHref] = useQueryParamState<ImagesTab>("images", IMAGE_TAB_IDS, "generate", { clearKeys: IMAGE_CLEAR_KEYS });
  const [busy, setBusy] = useState("");
  const [currentJob, setCurrentJob] = useState<ImageGenerationJob | undefined>(undefined);
  const [currentMediaJob, setCurrentMediaJob] = useState<MediaGenerationJob | undefined>(undefined);
  const [selectedResource, setSelectedResource] = useState<{ kind: "legacy" | "media"; id: string } | undefined>(undefined);
  const [libraryScope, setLibraryScope] = useState<ImageLibraryScope>("public");
  const [privateUnlocked, setPrivateUnlocked] = useState(false);
  const [privateExpiresAt, setPrivateExpiresAt] = useState("");
  const [privateAssets, setPrivateAssets] = useState<ImageAsset[]>([]);
  const [privateMediaAssets, setPrivateMediaAssets] = useState<MediaAsset[]>([]);
  const [imageToImageAsset, setImageToImageAsset] = useState<ImageAsset | undefined>(undefined);
  const [mediaImageToImageAsset, setMediaImageToImageAsset] = useState<MediaAsset | undefined>(undefined);
  const [multiEditRefs, setMultiEditRefs] = useState<AssetRef[]>([]);
  const [keyframeRefs, setKeyframeRefs] = useState<AssetRef[]>([]);
  const [videoReferenceRef, setVideoReferenceRef] = useState<AssetRef | undefined>(undefined);
  const [appliedPrompt, setAppliedPrompt] = useState<AppliedImagePrompt | undefined>(undefined);
  const [selectedPromptId, setSelectedPromptId] = useState("");
  const [objectProfiles, setObjectProfiles] = useState<ObjectStorageProfile[]>([]);

  const [mediaType, setMediaType] = useState<MediaType>("image");
  const [currentProvider, setCurrentProvider] = useState<ProviderID>("xai");
  const [settingsSubTab, setSettingsSubTab] = useState<"providers" | "storage">("providers");
  const [providersStatus, setProvidersStatus] = useState<ProvidersStatus | null>(null);
  const [mediaJobs, setMediaJobs] = useState<MediaGenerationJob[]>([]);
  const [mediaAssets, setMediaAssets] = useState<MediaAsset[]>([]);
  const [targetJobRef, setTargetJobRef] = useState<{ kind: "legacy" | "media"; id: string } | undefined>(undefined);
  const [pendingPresetDraft, setPendingPresetDraft] = useState<ImagePromptFormState | undefined>(undefined);

  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const settings = useMemo(() => ({ ...defaultImageSettings(), ...(data.images.settings || {}) }), [data.images.settings]);
  const storageSettings = useMemo(() => ({ ...defaultImageStorageSettings(), ...(data.images.storageSettings || {}) }), [data.images.storageSettings]);
  const status = data.images.status || data.dashboard.images;
  const jobs = data.images.jobs || [];
  const assets = data.images.assets || [];
  const prompts = data.images.prompts || [];

  const allMediaJobs = useMemo(() => {
    if (!currentMediaJob || mediaJobs.some((job) => job.id === currentMediaJob.id)) return mediaJobs;
    return [currentMediaJob, ...mediaJobs];
  }, [currentMediaJob, mediaJobs]);

  const hasApiKeyForProvider = useMemo(() => {
    const ps = providersStatus?.providers || [];
    const match = ps.find((p) => p.provider === currentProvider);
    if (match) return match.hasApiKey;
    if (currentProvider === "xai") return Boolean(settings?.hasApiKey);
    return false;
  }, [providersStatus, currentProvider, settings?.hasApiKey]);

  const providerCapabilities = useMemo(() => {
    const models = providersStatus?.models || [];
    return models.filter((m: ModelCapability) => m.provider === currentProvider && m.mediaType === mediaType && !m.deprecated);
  }, [providersStatus, currentProvider, mediaType]);

  const providerDefaults = useMemo(() => {
    const ps = providersStatus?.providers || [];
    return ps.find((p: ProviderStatus) => p.provider === currentProvider);
  }, [providersStatus, currentProvider]);

  const libraryAssets = libraryScope === "private" ? privateAssets : assets;
  const libraryMediaAssets = libraryScope === "private" ? privateMediaAssets : mediaAssets;
  const selectedAsset: ImageAsset | undefined =
    selectedResource?.kind === "legacy"
      ? libraryAssets.find((a) => a.id === selectedResource.id)
      : undefined;
  const selectedMediaAsset: MediaAsset | undefined =
    selectedResource?.kind === "media"
      ? libraryMediaAssets.find((a) => a.id === selectedResource.id)
      : undefined;
  const selectedPrompt = prompts.find((prompt) => prompt.id === selectedPromptId) || prompts[0];

  const libraryImageRef = useMemo<ImageAsset | undefined>(() => {
    if (imageToImageAsset) return imageToImageAsset;
    if (mediaImageToImageAsset) {
      return {
        id: mediaImageToImageAsset.id,
        url: mediaImageToImageAsset.url || mediaImageToImageAsset.downloadUrl || "",
        assetType: "source_upload",
        storageBackend: mediaImageToImageAsset.storageBackend,
        mimeType: mediaImageToImageAsset.mimeType,
        sizeBytes: mediaImageToImageAsset.sizeBytes || 0,
        width: mediaImageToImageAsset.width,
        height: mediaImageToImageAsset.height,
        createdAt: mediaImageToImageAsset.createdAt,
        updatedAt: mediaImageToImageAsset.createdAt,
        hasApiKey: false,
        provider: "",
      } as unknown as ImageAsset;
    }
    return undefined;
  }, [imageToImageAsset, mediaImageToImageAsset]);
  const libraryImageAssetRef = useMemo<AssetRef | undefined>(() => {
    if (imageToImageAsset?.id) return { kind: "legacy", id: imageToImageAsset.id };
    if (mediaImageToImageAsset?.id) return { kind: "media", id: mediaImageToImageAsset.id };
    return undefined;
  }, [imageToImageAsset?.id, mediaImageToImageAsset?.id]);
  const historyJobs = useMemo(() => {
    if (!currentJob || jobs.some((job) => job.id === currentJob.id)) return jobs;
    return [currentJob, ...jobs];
  }, [currentJob, jobs]);
  const latestJob = currentJob || jobs[0];
  const hasActiveJob = historyJobs.some(isActiveImageJob) || allMediaJobs.some(isActiveMediaJob);

  useEffect(() => {
    void fetchProvidersStatus();
  }, []);

  useEffect(() => {
    if (activeTab !== "generate" && activeTab !== "history" && activeTab !== "library") return;
    void refreshMediaData();
  }, [activeTab, mediaType, currentProvider]);

  useEffect(() => {
    if (mediaType === "video" && currentProvider !== "agnes") {
      setCurrentProvider("agnes");
    }
  }, [mediaType, currentProvider]);

  useEffect(() => {
    if (!currentJob?.id) return;
    const updated = jobs.find((job) => job.id === currentJob.id);
    if (updated) setCurrentJob(updated);
  }, [currentJob?.id, jobs]);

  useEffect(() => {
    if (!currentMediaJob?.id) return;
    const updated = allMediaJobs.find((job) => job.id === currentMediaJob.id);
    if (updated) setCurrentMediaJob(updated);
  }, [currentMediaJob?.id, allMediaJobs]);

  useEffect(() => {
    if (!selectedResource) return;
    if (selectedResource.kind === "legacy") {
      if (libraryAssets.some((asset) => asset.id === selectedResource.id)) return;
    } else {
      if (libraryMediaAssets.some((asset) => asset.id === selectedResource.id)) return;
    }
    setSelectedResource(undefined);
  }, [libraryAssets, libraryMediaAssets, selectedResource]);

  useEffect(() => {
    if (!selectedPromptId || prompts.some((prompt) => prompt.id === selectedPromptId)) return;
    setSelectedPromptId("");
  }, [prompts, selectedPromptId]);

  useEffect(() => {
    if (!hasActiveJob) return;
    const timer = window.setInterval(() => {
      void actions.refreshImages();
      void refreshMediaData();
    }, 3000);
    return () => window.clearInterval(timer);
  }, [actions, hasActiveJob]);

  useEffect(() => {
    if (activeTab !== "library" || libraryScope !== "private") return;
    void (async () => {
      try {
        const status = await actions.api<ImagePrivateStatus>("/api/images/library/private/status");
        setPrivateUnlocked(Boolean(status.unlocked));
        setPrivateExpiresAt(status.expiresAt || "");
        if (status.unlocked) {
          await refreshPrivateAssets();
          await refreshPrivateMediaAssets();
        } else {
          setPrivateAssets([]);
          setPrivateMediaAssets([]);
        }
      } catch (error) {
        setPrivateUnlocked(false);
        setPrivateExpiresAt("");
        setPrivateAssets([]);
        setPrivateMediaAssets([]);
        actions.setToast(friendlyError(error), "danger");
      }
    })();
  }, [activeTab, libraryScope]);

  useEffect(() => {
    if (activeTab !== "settings") return;
    void refreshObjectProfiles();
    void fetchProvidersStatus();
  }, [activeTab]);

  async function fetchProvidersStatus() {
    try {
      const result = await actions.api<ProvidersStatus>("/api/images/providers");
      setProvidersStatus(result);
      const hasXAI = (result.providers || []).some((p: ProviderStatus) => p.provider === "xai" && p.hasApiKey);
      const hasAgnes = (result.providers || []).some((p: ProviderStatus) => p.provider === "agnes" && p.hasApiKey);
      if (!hasXAI && hasAgnes) setCurrentProvider("agnes");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function refreshMediaData() {
    const scopeParam = activeTab === "library" && libraryScope === "private" ? "&scope=private" : "";
    const assetMediaType = activeTab === "library" || activeTab === "generate" ? "" : mediaType;
    const assetProvider = activeTab === "library" || activeTab === "generate" ? "" : currentProvider;
    try {
      const [jobsResult, assetsResult] = await Promise.all([
        actions.api<{ items?: MediaGenerationJob[]; legacyItems?: unknown[]; count?: number }>(`/api/images/generations?limit=80&mediaType=${mediaType}&provider=${currentProvider}`),
        actions.api<{ items?: MediaAsset[]; count?: number }>(`/api/images/media-assets?limit=120&mediaType=${assetMediaType}&provider=${assetProvider}${scopeParam}`),
      ]);
      setMediaJobs(jobsResult.items || []);
      if (libraryScope === "private" && activeTab === "library") {
        setPrivateMediaAssets(assetsResult.items || []);
      } else {
        setMediaAssets(assetsResult.items || []);
      }
    } catch (error) {
      if (activeTab !== "generate") {
        actions.setToast(friendlyError(error), "danger");
      }
    }
  }

  async function submitJob(formData: FormData) {
    setBusy("job");
    try {
      formData.append("media_type", mediaType);
      formData.append("provider", currentProvider);
      const result = await actions.api<MediaJobResponse>("/api/images/generations", {
        method: "POST",
        csrf: actions.csrf,
        body: formData,
      });
      if (result.job) {
        if (result.jobType === "legacy_image") {
          setCurrentJob(result.job as unknown as ImageGenerationJob);
        } else {
          setCurrentMediaJob(result.job);
        }
      }
      await actions.refreshImages();
      await refreshMediaData();
      actions.setToast(mediaType === "video" ? "视频生成任务已提交，可离开页面；完成后在历史和资源库查看" : "图片生成任务已提交", "good");
    } catch (error) {
      const payload = (error as ApiError).payload as MediaJobResponse | ImageJobResponse | undefined;
      if (payload?.job) {
        if ("mediaType" in payload.job) {
          setCurrentMediaJob(payload.job);
        } else {
          setCurrentJob(payload.job as ImageGenerationJob);
        }
        await actions.refreshImages();
        await refreshMediaData();
      }
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function retryJob(kind: "legacy" | "media", rawJob: unknown) {
    const jobId = kind === "legacy" ? (rawJob as ImageGenerationJob).id : (rawJob as MediaGenerationJob).id;
    setBusy(`retry:${jobId}`);
    try {
      let result: MediaJobResponse | ImageJobResponse | undefined;
      if (kind === "media") {
        result = await actions.api<MediaJobResponse>(`/api/images/generations/${encodeURIComponent(jobId)}/retry`, {
          method: "POST",
          csrf: actions.csrf,
        });
      } else {
        const job = rawJob as ImageGenerationJob;
        const sourceRefs = sourceRefsFromJob(job.sources, "legacy");
        if (Number(job.sourceCount || 0) > 0 && sourceRefs.length !== Number(job.sourceCount || 0)) {
          restoreJobToGenerate(kind, rawJob);
          actions.setToast("原任务参考图没有完整保存，已恢复参数；请重新选择参考图后生成", "warn");
          return;
        }
        result = await actions.api<MediaJobResponse | ImageJobResponse>("/api/images/generations", {
          method: "POST",
          csrf: actions.csrf,
          body: {
            mediaType: "image",
            provider: "xai",
            mode: job.mode || "text_to_image",
            model: job.model || "",
            prompt: job.prompt || "",
            n: job.imageCount || 1,
            aspectRatio: job.aspectRatio || "",
            parameters: {
              aspectRatio: job.aspectRatio || "",
              resolution: job.resolution || "",
              responseFormat: job.responseFormat || "url",
              n: job.imageCount || 1,
            },
            sources: sourceRefsForRetryPayload(sourceRefs),
          },
        });
      }
      if (result?.job) {
        if ("mediaType" in result.job) {
          setCurrentMediaJob(result.job);
          setCurrentProvider(result.job.provider);
          setMediaType(result.job.mediaType);
        } else {
          setCurrentJob(result.job as ImageGenerationJob);
          setCurrentProvider("xai");
          setMediaType("image");
        }
      }
      await actions.refreshImages();
      await refreshMediaData();
      actions.setToast("历史任务已重新提交", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function copyJobParams(kind: "legacy" | "media", rawJob: unknown) {
    let payload: Record<string, unknown> = {};
    if (kind === "legacy") {
      const j = rawJob as ImageGenerationJob;
      payload = { kind, provider: "xai", mode: j.mode, model: j.model, prompt: j.prompt, resolution: j.resolution, aspectRatio: j.aspectRatio, imageCount: j.imageCount, sourceCount: j.sourceCount };
    } else {
      const j = rawJob as MediaGenerationJob;
      payload = { kind, provider: j.provider, mediaType: j.mediaType, mode: j.mode, model: j.model, prompt: j.prompt, parameters: j.parameters, sourceCount: j.sourceCount, endpoint: j.endpoint };
    }
    try {
      await navigator.clipboard.writeText(JSON.stringify(payload, null, 2));
      actions.setToast("参数已复制到剪贴板", "good");
    } catch {
      actions.setToast("复制失败：浏览器不支持剪贴板", "warn");
    }
  }

  function restoreJobToGenerate(kind: "legacy" | "media", rawJob: unknown) {
    setActiveTab("generate");
    setImageToImageAsset(undefined);
    setMediaImageToImageAsset(undefined);
    setMultiEditRefs([]);
    setKeyframeRefs([]);
    setVideoReferenceRef(undefined);
    if (kind === "legacy") {
      const j = rawJob as ImageGenerationJob;
      setCurrentProvider("xai");
      setMediaType("image");
      restoreSourceRefs("image", String(j.mode || "text_to_image"), sourceRefsFromJob(j.sources, "legacy"));
      setAppliedPrompt({
        nonce: Date.now(),
        prompt: {
          id: j.id,
          title: `从历史恢复 · ${formatDate(j.createdAt) || ""}`,
          prompt: j.prompt || "",
          mode: (j.mode || "text_to_image") as "text_to_image" | "image_to_image" | "multi_image_edit",
          model: j.model || "",
          aspectRatio: j.aspectRatio || "",
          resolution: j.resolution || "",
          imageCount: j.imageCount || 1,
          status: "active",
          tags: [],
          useCount: 0,
          createdAt: j.createdAt,
          updatedAt: j.createdAt,
        } as unknown as ImagePrompt,
      });
    } else {
      const j = rawJob as MediaGenerationJob;
      setCurrentProvider(j.provider);
      setMediaType(j.mediaType);
      restoreSourceRefs(j.mediaType, String(j.mode || ""), sourceRefsFromJob(j.sources, "media"));
      const params = j.parameters || {};
      const modeVal = j.mode || (j.mediaType === "image" ? "text_to_image" : "text_to_video");
      const videoParams = j.mediaType === "video" ? {
        duration: params.durationSeconds as number | undefined,
        fps: params.fps as number | undefined,
        numFrames: params.numFrames as number | undefined,
        videoWidth: params.width as number | undefined,
        videoHeight: params.height as number | undefined,
      } : undefined;
      setAppliedPrompt({
        nonce: Date.now(),
        prompt: {
          id: j.id,
          title: `从历史恢复 · ${formatDate(j.createdAt) || ""}`,
          prompt: j.prompt || "",
          mode: modeVal as "text_to_image" | "image_to_image" | "multi_image_edit",
          model: j.model || "",
          aspectRatio: (params.aspectRatio as string) || "",
          resolution: "",
          imageCount: Number((params.n as number) || j.outputCount || 1),
          status: "active",
          tags: [],
          useCount: 0,
          createdAt: j.createdAt,
          updatedAt: j.createdAt,
          _videoParams: videoParams,
        } as unknown as ImagePrompt,
      });
    }
  }

  function sourceRefsFromJob(sources: Array<{ assetId?: string; slot?: number }> | undefined, fallbackKind: AssetRef["kind"]): AssetRef[] {
    return (sources || [])
      .slice()
      .sort((a, b) => Number(a.slot || 0) - Number(b.slot || 0))
      .map((source) => assetRefFromStoredID(source.assetId || "", fallbackKind))
      .filter(Boolean) as AssetRef[];
  }

  function assetRefFromStoredID(value: string, fallbackKind: AssetRef["kind"]): AssetRef | undefined {
    let raw = value.trim();
    if (!raw) return undefined;
    if (raw.startsWith("asset:")) raw = raw.slice("asset:".length).trim();
    const separator = raw.indexOf(":");
    if (separator > 0) {
      const prefix = raw.slice(0, separator);
      const id = raw.slice(separator + 1).trim();
      if ((prefix === "legacy" || prefix === "media") && id) return { kind: prefix, id };
    }
    if (raw.startsWith("medasset_")) return { kind: "media", id: raw };
    if (raw.startsWith("imgasset_")) return { kind: "legacy", id: raw };
    return { kind: fallbackKind, id: raw };
  }

  function restoreSourceRefs(nextMediaType: MediaType, mode: string, refs: AssetRef[]) {
    if (!refs.length) return;
    if (nextMediaType === "image") {
      if (mode === "image_to_image") {
        setMultiEditRefs(refs.slice(0, 1));
      } else if (mode === "multi_image_edit") {
        setMultiEditRefs(refs.slice(0, 3));
      }
      return;
    }
    if (mode === "image_to_video") {
      setVideoReferenceRef(refs[0]);
    } else if (mode === "keyframes") {
      setKeyframeRefs(refs.slice(0, 6));
    } else if (mode === "multi_image_video") {
      setKeyframeRefs(refs.slice(0, 3));
    }
  }

  function sourceRefsForRetryPayload(refs: AssetRef[]) {
    return refs.map((ref) => ({
      type: "library_asset",
      assetId: `${ref.kind}:${ref.id}`,
    }));
  }

  function openPresetDraft(draft: ImagePromptFormState) {
    setPendingPresetDraft({
      ...draft,
      title: draft.title || `预设 · ${new Date().toLocaleDateString()}`,
    });
    setActiveTab("presets");
  }

  function handleSaveAsPreset(form: ImagePromptFormState) {
    openPresetDraft({ ...form, _source: "来自当前生成表单" });
  }

  function handleSaveJobAsPreset(kind: "legacy" | "media", rawJob: unknown) {
    if (kind === "legacy") {
      const j = rawJob as ImageGenerationJob;
      openPresetDraft({
        title: "",
        description: `从历史任务恢复 · ${formatDate(j.createdAt) || ""}`,
        prompt: j.prompt || "",
        mediaType: "image",
        mode: String(j.mode || "text_to_image"),
        model: j.model || "",
        aspectRatio: j.aspectRatio || "",
        resolution: j.resolution || "",
        imageCount: j.imageCount || 1,
        tagsText: "",
        _source: `来自历史任务 · ${formatDate(j.createdAt)}`,
      });
    } else {
      const j = rawJob as MediaGenerationJob;
      const params = j.parameters || {};
      const isVideo = j.mediaType === "video";
      openPresetDraft({
        title: "",
        description: `从历史任务恢复 · ${formatDate(j.createdAt) || ""}`,
        prompt: j.prompt || "",
        mediaType: j.mediaType,
        mode: j.mode || (isVideo ? "text_to_video" : "text_to_image"),
        model: j.model || "",
        aspectRatio: (params.aspectRatio as string) || "",
        resolution: "",
        imageCount: Number(params.n || j.outputCount || 1),
        tagsText: "",
        videoDuration: isVideo ? (params.durationSeconds as number | undefined) : undefined,
        videoFps: isVideo ? (params.fps as number | undefined) : undefined,
        _source: `来自历史任务 · ${formatDate(j.createdAt)}`,
      });
    }
  }

  function openMediaAsset(assetId: string) {
    setSelectedResource({ kind: "media", id: assetId });
    setActiveTab("library");
  }

  function useMediaAssetAsI2iFromHistory(asset: MediaAsset) {
    if (asset.mediaType !== "image") return;
    useMediaAssetForImageToImage(asset);
    setActiveTab("generate");
  }

  function useLegacyOutputAsI2iFromHistory(assetId: string, url?: string) {
    const found = libraryAssets.find((a) => a.id === assetId || a.id?.startsWith(assetId.slice(0, 8)));
    if (found) {
      useAssetForImageToImage(found);
    } else if (url) {
      setAppliedPrompt(undefined);
      setImageToImageAsset(undefined);
      actions.setToast("无法定位本地资产，URL 参考需手动从浏览器复制", "warn");
      return;
    }
    setActiveTab("generate");
  }

  function openCurrentJobInHistory() {
    if (currentMediaJob?.id) {
      setTargetJobRef({ kind: "media", id: currentMediaJob.id });
      setActiveTab("history");
    } else if (latestJob?.id) {
      setTargetJobRef({ kind: "legacy", id: latestJob.id });
      setActiveTab("history");
    }
  }

  function useCurrentResultAsReference() {
    if (currentMediaJob?.id && currentMediaJob.mediaType === "image") {
      const firstAssetId = currentMediaJob.outputs?.[0]?.assetId;
      if (firstAssetId) {
        const found = libraryMediaAssets.find((a) => a.id === firstAssetId);
        if (found) useMediaAssetForImageToImage(found);
      }
    } else if (latestJob?.id) {
      const firstOutputId = latestJob.outputs?.[0]?.id || latestJob.outputs?.[0]?.assetId;
      if (firstOutputId) {
        const found = libraryAssets.find((a) => a.id === firstOutputId || a.id === latestJob.outputs?.[0]?.assetId);
        if (found) useAssetForImageToImage(found);
      }
    }
  }

  function resubmitCurrentJob() {
    if (currentMediaJob) {
      restoreJobToGenerate("media", currentMediaJob);
      actions.setToast("参数已恢复，确认后点击生成按钮提交", "good");
    } else if (latestJob) {
      restoreJobToGenerate("legacy", latestJob);
      actions.setToast("参数已恢复，确认后点击生成按钮提交", "good");
    }
  }

  async function saveSettings(draft: ImageSettingsDraft) {
    setBusy("settings");
    try {
      await actions.api("/api/images/settings", {
        method: "PUT",
        csrf: actions.csrf,
        body: {
          xaiApiKey: draft.xaiApiKey,
          clearApiKey: draft.clearApiKey,
          defaultModel: draft.defaultModel,
          defaultResponseFormat: draft.defaultResponseFormat,
          defaultResolution: draft.defaultResolution,
          defaultAspectRatio: draft.defaultAspectRatio,
          historyRetention: Number(draft.historyRetention || 500),
        },
      });
      await actions.refreshImages();
      await fetchProvidersStatus();
      actions.setToast("多媒体设置已保存", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function saveMediaProviderSettings(drafts: MediaProviderSettingsDraft[]) {
    const savedProviders: string[] = [];
    setBusy("provider:all");
    try {
      for (const draft of drafts) {
        setBusy(`provider:${draft.provider}`);
        await actions.api(`/api/images/providers/${draft.provider}`, {
          method: "PUT",
          csrf: actions.csrf,
          body: {
            enabled: draft.enabled,
            apiKey: draft.apiKey,
            clearApiKey: draft.clearApiKey,
            updateApiKey: draft.updateApiKey,
            defaultImageModel: draft.defaultImageModel,
            defaultVideoModel: draft.defaultVideoModel,
            defaultImageParams: draft.defaultImageParams,
            defaultVideoParams: draft.defaultVideoParams,
          },
        });
        if (draft.provider === "xai") {
          await actions.api("/api/images/settings", {
            method: "PUT",
            csrf: actions.csrf,
            body: {
              defaultModel: draft.defaultImageModel,
              defaultResponseFormat: (draft.defaultImageParams?.defaultResponseFormat as string) || settings.defaultResponseFormat,
              defaultResolution: (draft.defaultImageParams?.defaultResolution as string) || settings.defaultResolution,
              defaultAspectRatio: (draft.defaultImageParams?.defaultAspectRatio as string) || settings.defaultAspectRatio,
              historyRetention: Number((draft.defaultImageParams?.historyRetention as number) || settings.historyRetention || 500),
              xaiApiKey: "",
              clearApiKey: draft.clearApiKey,
            },
          });
        }
        savedProviders.push(providerLabel(draft.provider));
      }
      await actions.refreshImages();
      await fetchProvidersStatus();
      actions.setToast(`Provider 设置已保存：${savedProviders.join(" / ")}`, "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
      await fetchProvidersStatus();
    } finally {
      setBusy("");
    }
  }

  async function testMediaProvider(provider: ProviderID) {
    setBusy(`provider-test:${provider}`);
    try {
      await actions.api(`/api/images/providers/${provider}/test`, {
        method: "POST",
        csrf: actions.csrf,
      });
      await fetchProvidersStatus();
      actions.setToast(`${providerLabel(provider)} 连接测试通过`, "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function saveStorageSettings(draft: ImageStorageSettingsDraft) {
    setBusy("storage");
    try {
      await actions.api("/api/images/storage-settings", {
        method: "PUT",
        csrf: actions.csrf,
        body: {
          backend: draft.backend,
          objectStorageProfileId: draft.objectStorageProfileId,
          s3ProviderLabel: draft.s3ProviderLabel,
          s3Bucket: draft.s3Bucket,
          s3Region: draft.s3Region,
          s3Endpoint: draft.s3Endpoint,
          s3Prefix: draft.s3Prefix,
          s3ForcePathStyle: draft.s3ForcePathStyle,
          s3AccessKeyId: draft.s3AccessKeyId,
          s3SecretAccessKey: draft.s3SecretAccessKey,
          s3SessionToken: draft.s3SessionToken,
          s3AccessMode: draft.s3AccessMode,
          fallbackToLocal: draft.fallbackToLocal,
          clearSecret: draft.clearSecret,
        },
      });
      await actions.refreshImages();
      actions.setToast("多媒体存储设置已保存", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function testStorageSettings() {
    setBusy("storage-test");
    try {
      await actions.api("/api/images/storage-settings/test", {
        method: "POST",
        csrf: actions.csrf,
      });
      actions.setToast("对象存储连接测试通过", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function refreshObjectProfiles() {
    try {
      const result = await actions.api<{ items?: ObjectStorageProfile[] }>("/api/object-storage/profiles");
      setObjectProfiles(result.items || []);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function deleteAsset(asset: ImageAsset, skipConfirm = false) {
    if (!skipConfirm) {
      const confirmed = await confirmDanger({
        title: "删除图片资产",
        objectName: imageAssetTitle(asset),
        body: "该操作会删除图片库中的图片资产记录，并按后端策略清理对应存储对象。",
        confirmLabel: "删除图片",
        impact: [
          asset.private ? "图片会从私密收藏夹移除。" : "图片会从普通图片库移除。",
          "相关生成历史仍保留参数和状态摘要。",
        ],
        recovery: "删除通常不可恢复；如果对象存储清理失败，后端会返回错误并保持当前状态。",
      });
      if (!confirmed) return;
    }
    setBusy(`delete:${asset.id}`);
    try {
      await actions.api(`/api/images/library/assets/${encodeURIComponent(asset.id)}`, {
        method: "DELETE",
        csrf: actions.csrf,
      });
      await actions.refreshImages();
      if (libraryScope === "private") await refreshPrivateAssets();
      if (selectedResource?.kind === "legacy" && selectedResource.id === asset.id) setSelectedResource(undefined);
      if (!skipConfirm) actions.setToast("图片已删除", "good");
    } catch (error) {
      if (!skipConfirm) actions.setToast(friendlyError(error), "danger");
      throw error;
    } finally {
      setBusy("");
    }
  }

  async function deleteMediaAsset(asset: MediaAsset, skipConfirm = false) {
    const label = asset.originalFilename || asset.promptPreview || asset.id;
    const typeLabel = asset.mediaType === "video" ? "视频" : "图片";
    if (!skipConfirm) {
      const confirmed = await confirmDanger({
        title: `删除${typeLabel}资产`,
        objectName: label,
        body: `该操作会删除媒体库中的${typeLabel}资产记录，并按后端策略清理对应存储对象。`,
        confirmLabel: `删除${typeLabel}`,
        impact: [
          asset.private ? "会从私密收藏夹移除。" : "会从媒体库移除。",
          "相关生成历史仍保留参数和状态摘要。",
        ],
        recovery: "删除通常不可恢复；如果对象存储清理失败，后端会返回错误并保持当前状态。",
      });
      if (!confirmed) return;
    }
    setBusy(`delete:${asset.id}`);
    try {
      await actions.api(`/api/images/media-assets/${encodeURIComponent(asset.id)}`, {
        method: "DELETE",
        csrf: actions.csrf,
      });
      await refreshMediaData();
      if (selectedResource?.kind === "media" && selectedResource.id === asset.id) setSelectedResource(undefined);
      if (!skipConfirm) actions.setToast(`${typeLabel}已删除`, "good");
    } catch (error) {
      if (!skipConfirm) actions.setToast(friendlyError(error), "danger");
      throw error;
    } finally {
      setBusy("");
    }
  }

  async function bulkDeleteResources(resources: Array<{ kind: "legacy" | "media"; id: string }>): Promise<boolean> {
    const legacy = resources
      .filter((r) => r.kind === "legacy")
      .map((r) => libraryAssets.find((a) => a.id === r.id))
      .filter(Boolean) as ImageAsset[];
    const media = resources
      .filter((r) => r.kind === "media")
      .map((r) => libraryMediaAssets.find((a) => a.id === r.id))
      .filter(Boolean) as MediaAsset[];
    const total = legacy.length + media.length;
    if (total === 0) return false;

    const confirmed = await confirmDanger({
      title: "批量删除资源",
      objectName: `${total} 个资源`,
      body: `即将删除 ${legacy.length} 张图片资产 + ${media.length} 个媒体资源。相关生成历史不会被删除。`,
      confirmLabel: "确认删除",
      impact: [
        legacy.length ? `删除 ${legacy.length} 张图片资产记录和文件。` : "",
        media.length ? `删除 ${media.length} 个媒体资源记录和文件。` : "",
      ].filter(Boolean),
      recovery: "对象存储物理删除不可恢复；本地删除通常不可恢复。后端失败会保留记录并报错。",
    });
    if (!confirmed) return false;

    const results = await Promise.allSettled([
      ...legacy.map((a) => (async () => {
        await deleteAsset(a, true);
        return "ok";
      })()),
      ...media.map((a) => (async () => {
        await deleteMediaAsset(a, true);
        return "ok";
      })()),
    ]);
    const succeeded = results.filter((r) => r.status === "fulfilled").length;
    const failed = total - succeeded;
    if (failed > 0) {
      actions.setToast(`成功删除 ${succeeded} 个，${failed} 个失败`, failed === total ? "danger" : "warn");
    } else {
      actions.setToast(`已删除 ${succeeded} 个资源`, "good");
    }
    return true;
  }

  async function archiveAsset(asset: ImageAsset) {
    setBusy(`archive:${asset.id}`);
    try {
      const result = await actions.api<{ asset?: ImageAsset }>(`/api/images/library/assets/${encodeURIComponent(asset.id)}/archive-s3`, {
        method: "POST",
        csrf: actions.csrf,
      });
      await actions.refreshImages();
      if (libraryScope === "private") await refreshPrivateAssets();
      setSelectedResource(result.asset?.id ? { kind: "legacy", id: result.asset.id } : undefined);
      actions.setToast("图片已归档到对象存储", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function uploadAsset(formData: FormData): Promise<boolean> {
    setBusy("upload");
    try {
      const result = await actions.api<ImageUploadResponse>("/api/images/library/assets", {
        method: "POST",
        csrf: actions.csrf,
        body: formData,
      });
      await actions.refreshImages();
      if (result.asset?.id) setSelectedResource({ kind: "legacy", id: result.asset.id });
      actions.setToast(result.duplicate ? "图片已存在，已复用图片库资产" : "图片已上传", "good");
      return true;
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
      return false;
    } finally {
      setBusy("");
    }
  }

  function useAssetForImageToImage(asset: ImageAsset) {
    setImageToImageAsset(asset);
    setMediaImageToImageAsset(undefined);
    setMultiEditRefs([]);
    setKeyframeRefs([]);
    setVideoReferenceRef(undefined);
    setMediaType("image");
    setActiveTab("generate");
    actions.setToast("已选择图片库图片作为图生图参考", "good");
  }

  function useMediaAssetForImageToImage(asset: MediaAsset) {
    const provider = asset.provider === "xai" || asset.provider === "agnes" ? asset.provider : "agnes";
    setMediaImageToImageAsset(asset);
    setImageToImageAsset(undefined);
    setMultiEditRefs([]);
    setKeyframeRefs([]);
    setVideoReferenceRef(undefined);
    setCurrentProvider(provider);
    setMediaType("image");
    setActiveTab("generate");
    actions.setToast("已选择资源库图片作为图生图参考", "good");
  }

  function handleSetMultiEditImages(refs: AssetRef[]) {
    setMultiEditRefs(refs);
    setKeyframeRefs([]);
    setVideoReferenceRef(undefined);
    setImageToImageAsset(undefined);
    setMediaImageToImageAsset(undefined);
    setCurrentProvider("agnes");
    setMediaType("image");
    setActiveTab("generate");
    actions.setToast(`已选择 ${refs.length} 张图片用于多图编辑（Agnes · 多图编辑模式）`, "good");
  }

  function handleSetKeyframes(refs: AssetRef[]) {
    setKeyframeRefs(refs);
    setMultiEditRefs([]);
    setVideoReferenceRef(undefined);
    setImageToImageAsset(undefined);
    setMediaImageToImageAsset(undefined);
    setCurrentProvider("agnes");
    setMediaType("video");
    setActiveTab("generate");
    actions.setToast(`已选择 ${refs.length} 张关键帧（Agnes · 关键帧模式）`, "good");
  }

  function handleSetVideoReference(ref: AssetRef) {
    setVideoReferenceRef(ref);
    setMultiEditRefs([]);
    setKeyframeRefs([]);
    setImageToImageAsset(undefined);
    setMediaImageToImageAsset(undefined);
    setCurrentProvider("agnes");
    setMediaType("video");
    setActiveTab("generate");
    actions.setToast("已选择图片作为视频参考图（Agnes · 图生视频模式）", "good");
  }

  function applyReferenceRefsFromGenerate(refs: AssetRef[], context: { mediaType: MediaType; mode: MediaMode }) {
    const picked = refs.filter((ref) => ref.id);
    if (!picked.length) return;

    setImageToImageAsset(undefined);
    setMediaImageToImageAsset(undefined);
    setActiveTab("generate");

    if (context.mediaType === "image") {
      setMediaType("image");
      setKeyframeRefs([]);
      setVideoReferenceRef(undefined);
      setMultiEditRefs(context.mode === "image_to_image" ? picked.slice(0, 1) : picked);
      if (context.mode === "multi_image_edit") {
        setCurrentProvider("agnes");
        actions.setToast(`已选择 ${picked.length} 张图片用于多图编辑（Agnes · 多图编辑模式）`, "good");
      } else {
        actions.setToast("已选择图片库图片作为图生图参考", "good");
      }
      return;
    }

    setMediaType("video");
    setCurrentProvider("agnes");
    setMultiEditRefs([]);
    if (context.mode === "image_to_video") {
      setVideoReferenceRef(picked[0]);
      setKeyframeRefs([]);
      actions.setToast("已选择图片作为视频参考图（Agnes · 图生视频模式）", "good");
      return;
    }
    setVideoReferenceRef(undefined);
    setKeyframeRefs(picked);
    actions.setToast(`已选择 ${picked.length} 张图片用于视频参考（Agnes · ${context.mode === "multi_image_video" ? "多图视频" : "关键帧模式"}）`, "good");
  }

  async function createPrompt(draft: ImagePromptDraft): Promise<ImagePrompt | undefined> {
    setBusy("prompt-save");
    try {
      const result = await actions.api<{ prompt?: ImagePrompt }>("/api/images/prompts", {
        method: "POST",
        csrf: actions.csrf,
        body: draft,
      });
      await actions.refreshImages();
      if (result.prompt?.id) setSelectedPromptId(result.prompt.id);
      actions.setToast("Prompt 已保存", "good");
      return result.prompt;
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
      return undefined;
    } finally {
      setBusy("");
    }
  }

  async function updatePrompt(id: string, draft: ImagePromptDraft): Promise<ImagePrompt | undefined> {
    setBusy(`prompt:${id}`);
    try {
      const result = await actions.api<{ prompt?: ImagePrompt }>(`/api/images/prompts/${encodeURIComponent(id)}`, {
        method: "PATCH",
        csrf: actions.csrf,
        body: draft,
      });
      await actions.refreshImages();
      if (result.prompt?.id) setSelectedPromptId(result.prompt.id);
      actions.setToast("Prompt 已更新", "good");
      return result.prompt;
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
      return undefined;
    } finally {
      setBusy("");
    }
  }

  async function deletePrompt(prompt: ImagePrompt) {
    const confirmed = await confirmDanger({
      title: "删除生成预设",
      objectName: prompt.title || prompt.id,
      body: "该操作会将生成预设从库中移除，已经创建的生成任务不会被删除。",
      confirmLabel: "删除生成预设",
      impact: ["生成预设库默认不再展示该条目。", "已经复制到生成表单里的内容不会被自动清空。"],
      recovery: "当前版本不提供前端恢复入口；后端会保留软删除记录。",
    });
    if (!confirmed) return;
    setBusy(`prompt-delete:${prompt.id}`);
    try {
      await actions.api(`/api/images/prompts/${encodeURIComponent(prompt.id)}`, {
        method: "DELETE",
        csrf: actions.csrf,
      });
      await actions.refreshImages();
      if (selectedPromptId === prompt.id) setSelectedPromptId("");
      actions.setToast("生成预设已删除", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function usePrompt(prompt: ImagePrompt) {
    setBusy(`prompt-use:${prompt.id}`);
    try {
      const result = await actions.api<{ prompt?: ImagePrompt }>(`/api/images/prompts/${encodeURIComponent(prompt.id)}/use`, {
        method: "POST",
        csrf: actions.csrf,
      });
      const nextPrompt = result.prompt || prompt;
      setAppliedPrompt({ prompt: nextPrompt, nonce: Date.now() });
      setSelectedPromptId(nextPrompt.id);
      setActiveTab("generate");
      const modeStr = prompt.mode || "";
      if (VIDEO_MODES.some((m) => m.id === modeStr)) {
        setMediaType("video");
      } else {
        setMediaType("image");
      }
      await actions.refreshImages();
      actions.setToast("生成预设已带入生成任务", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function refreshPrivateStatus(loadAssets = false) {
    try {
      const status = await actions.api<ImagePrivateStatus>("/api/images/library/private/status");
      setPrivateUnlocked(Boolean(status.unlocked));
      setPrivateExpiresAt(status.expiresAt || "");
      if (status.unlocked && loadAssets) await refreshPrivateAssets();
      if (!status.unlocked) setPrivateAssets([]);
    } catch (error) {
      setPrivateUnlocked(false);
      setPrivateExpiresAt("");
      setPrivateAssets([]);
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function refreshPrivateAssets() {
    try {
      const result = await actions.api<{ items?: ImageAsset[] }>("/api/images/library/assets?limit=80&privacy=private");
      setPrivateAssets(result.items || []);
      setPrivateUnlocked(true);
    } catch (error) {
      const apiError = error as ApiError;
      if (apiError.code === "images_private_locked") {
        setPrivateUnlocked(false);
        setPrivateExpiresAt("");
        setPrivateAssets([]);
      }
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function refreshPrivateMediaAssets() {
    try {
      const result = await actions.api<{ items?: MediaAsset[] }>("/api/images/media-assets?limit=120&scope=private");
      setPrivateMediaAssets(result.items || []);
      if (!privateUnlocked) {
        setPrivateUnlocked(true);
        void refreshPrivateStatus();
      }
    } catch (error) {
      const apiError = error as ApiError;
      if (apiError.code === "images_private_locked") {
        setPrivateUnlocked(false);
        setPrivateExpiresAt("");
        setPrivateMediaAssets([]);
      }
      if (apiError.code !== "images_private_locked") {
        actions.setToast(friendlyError(error), "danger");
      }
    }
  }

  async function unlockPrivateCollection(password: string) {
    setBusy("private-unlock");
    try {
      const status = await actions.api<ImagePrivateStatus>("/api/images/library/private/unlock", {
        method: "POST",
        csrf: actions.csrf,
        body: { password },
      });
      setPrivateUnlocked(Boolean(status.unlocked));
      setPrivateExpiresAt(status.expiresAt || "");
      await refreshPrivateAssets();
      await refreshPrivateMediaAssets();
      actions.setToast("私密收藏夹已解锁", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function lockPrivateCollection() {
    setBusy("private-lock");
    try {
      await actions.api("/api/images/library/private/lock", {
        method: "POST",
        csrf: actions.csrf,
      });
      setPrivateUnlocked(false);
      setPrivateExpiresAt("");
      setPrivateAssets([]);
      setPrivateMediaAssets([]);
      setSelectedResource(undefined);
      actions.setToast("私密收藏夹已锁定", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function setAssetPrivate(asset: ImageAsset, nextPrivate: boolean) {
    setBusy(`private:${asset.id}`);
    try {
      const result = await actions.api<{ asset?: ImageAsset }>(`/api/images/library/assets/${encodeURIComponent(asset.id)}/private`, {
        method: "POST",
        csrf: actions.csrf,
        body: { private: nextPrivate },
      });
      await actions.refreshImages();
      if (privateUnlocked) await refreshPrivateAssets();
      if (nextPrivate) {
        if (selectedResource?.kind === "legacy" && selectedResource.id === asset.id) setSelectedResource(undefined);
        actions.setToast("已加入私密收藏夹", "good");
      } else {
        setSelectedResource(result.asset?.id ? { kind: "legacy", id: result.asset.id } : undefined);
        actions.setToast("已移出私密收藏夹", "good");
      }
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function setMediaAssetPrivate(asset: MediaAsset, nextPrivate: boolean) {
    setBusy(`private:${asset.id}`);
    try {
      const result = await actions.api<{ asset?: MediaAsset }>(`/api/images/media-assets/${encodeURIComponent(asset.id)}/private`, {
        method: "POST",
        csrf: actions.csrf,
        body: { private: nextPrivate },
      });
      if (libraryScope === "private") {
        await refreshPrivateMediaAssets();
      } else {
        await refreshMediaData();
      }
      if (nextPrivate) {
        if (selectedResource?.kind === "media" && selectedResource.id === asset.id) setSelectedResource(undefined);
        actions.setToast("已加入私密收藏夹", "good");
      } else {
        setSelectedResource(result.asset?.id ? { kind: "media", id: result.asset.id } : undefined);
        actions.setToast("已移出私密收藏夹", "good");
      }
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function archiveMediaAsset(asset: MediaAsset) {
    setBusy(`archive:${asset.id}`);
    try {
      const result = await actions.api<{ asset?: MediaAsset }>(`/api/images/media-assets/${encodeURIComponent(asset.id)}/archive-s3`, {
        method: "POST",
        csrf: actions.csrf,
      });
      if (libraryScope === "private") {
        await refreshPrivateMediaAssets();
      } else {
        await refreshMediaData();
      }
      setSelectedResource(result.asset?.id ? { kind: "media", id: result.asset.id } : undefined);
      actions.setToast("媒体资产已归档到对象存储", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  function openJobFromAsset(jobId: string, kind: "legacy" | "media" = "media") {
    setTargetJobRef({ kind, id: jobId });
    if (kind === "legacy") {
      setCurrentProvider("xai");
    }
    setActiveTab("history");
  }

  function providerLabel(p: ProviderID): string {
    return PROVIDERS.find((pr) => pr.id === p)?.label || p;
  }

  function openJobLogs(kind: "legacy" | "media", rawJob: unknown) {
    const id = (kind === "legacy"
      ? (rawJob as ImageGenerationJob).id
      : (rawJob as MediaGenerationJob).id) || "";
    actions.setMainTab("logs");
    actions.setToast(`已跳转到日志页，可搜索 job id: ${id}`, "good");
  }

  return (
    <>
      <div className="grid min-h-[calc(100dvh-105px)] grid-cols-[minmax(0,1fr)_320px] max-xl:grid-cols-1">
        <div className="grid content-start gap-4 p-4">
          <ImagesTabs active={activeTab} hrefFor={tabHref} onChange={setActiveTab} />
          {activeTab === "generate" ? (
            <GeneratePanel
              appliedPrompt={appliedPrompt}
              busy={busy === "job"}
              capabilities={providerCapabilities}
              currentMediaJob={currentMediaJob}
              currentProvider={currentProvider}
              hasApiKey={hasApiKeyForProvider}
              keyframeRefs={keyframeRefs}
              latestJob={latestJob}
              libraryAssets={libraryAssets}
              libraryImageAssetRef={libraryImageAssetRef}
              libraryImage={libraryImageRef}
              libraryMediaAssets={libraryMediaAssets}
              mediaJobs={allMediaJobs}
              mediaType={mediaType}
              multiEditRefs={multiEditRefs}
              onApplyPrompt={(prompt) => void usePrompt(prompt)}
              onApplyReferenceRefs={applyReferenceRefsFromGenerate}
              onClearKeyframeRefs={() => setKeyframeRefs([])}
              onClearLibraryImage={() => {
                setImageToImageAsset(undefined);
                setMediaImageToImageAsset(undefined);
              }}
              onClearMultiEditRefs={() => setMultiEditRefs([])}
              onClearVideoReferenceRef={() => setVideoReferenceRef(undefined)}
              onRemoveKeyframeRefAtIndex={(i) => setKeyframeRefs((prev) => prev.filter((_, j) => j !== i))}
              onRemoveMultiEditRefAtIndex={(i) => setMultiEditRefs((prev) => prev.filter((_, j) => j !== i))}
              onMediaTypeChange={(t) => {
                setMediaType(t);
                if (t === "video") setCurrentProvider("agnes");
                setCurrentMediaJob(undefined);
              }}
              onOpenCurrentJobInHistory={openCurrentJobInHistory}
              onOpenPromptLibrary={() => setActiveTab("presets")}
              onOpenResourceLibrary={() => setActiveTab("library")}
              onProviderChange={(p) => {
                setCurrentProvider(p);
                setCurrentMediaJob(undefined);
              }}
              onResubmit={resubmitCurrentJob}
              onSaveAsPreset={handleSaveAsPreset}
              onUseCurrentAsReference={useCurrentResultAsReference}
              providers={providersStatus?.providers || []}
              onSubmit={submitJob}
              prompts={prompts}
              providerDefaults={providerDefaults}
              settings={settings}
              storageSettings={storageSettings}
              videoReferenceRef={videoReferenceRef}
            />
          ) : null}
          {activeTab === "presets" ? (
            <PromptLibraryPanel
              busy={busy}
              onCreate={createPrompt}
              onDelete={(prompt) => void deletePrompt(prompt)}
              onExternalDraftConsumed={() => setPendingPresetDraft(undefined)}
              externalDraft={pendingPresetDraft}
              onRefresh={actions.refreshImages}
              onSelect={(prompt) => setSelectedPromptId(prompt.id)}
              onUpdate={updatePrompt}
              onUse={(prompt) => void usePrompt(prompt)}
              prompts={prompts}
              selectedId={selectedPrompt?.id || ""}
              settings={settings}
            />
          ) : null}
          {activeTab === "library" ? (
             <LibraryPanel
               assets={libraryAssets}
               busy={busy}
               libraryScope={libraryScope}
               mediaAssets={libraryMediaAssets}
               mediaJobs={allMediaJobs}
               legacyJobs={historyJobs}
               mediaType={mediaType}
              onArchive={(asset) => void archiveAsset(asset)}
              onArchiveMedia={(asset) => void archiveMediaAsset(asset)}
              onBulkDeleteResources={(resources) => bulkDeleteResources(resources)}
              onBulkDownloadComplete={(n) => actions.setToast(`已触发 ${n} 个下载`, "good")}
              onDelete={(asset) => void deleteAsset(asset)}
              onDeleteMedia={(asset) => void deleteMediaAsset(asset)}
              onGoToGenerate={() => setActiveTab("generate")}
              onGoToSettings={() => setActiveTab("settings")}
              onLockPrivate={() => void lockPrivateCollection()}
              onMarkPrivate={(asset, nextPrivate) => void setAssetPrivate(asset, nextPrivate)}
              onMarkPrivateMedia={(asset, nextPrivate) => void setMediaAssetPrivate(asset, nextPrivate)}
              onMediaTypeChange={setMediaType}
              onOpenJob={(jobId, kind) => openJobFromAsset(jobId, kind)}
               onSetKeyframes={handleSetKeyframes}
               onSetMultiEditImages={handleSetMultiEditImages}
               onSetVideoReference={handleSetVideoReference}
               onRefresh={libraryScope === "private" ? refreshPrivateAssets : actions.refreshImages}
              onRefreshMedia={libraryScope === "private" ? refreshPrivateMediaAssets : refreshMediaData}
               onSelect={(asset) => setSelectedResource({ kind: "legacy", id: asset.id })}
               onSelectMedia={(asset) => setSelectedResource({ kind: "media", id: asset.id })}
               onScopeChange={(scope) => {
                 setLibraryScope(scope);
                 setSelectedResource(undefined);
               }}
              onUpload={uploadAsset}
              onUseForImage={useAssetForImageToImage}
              onUseMediaForImage={useMediaAssetForImageToImage}
              onUnlockPrivate={unlockPrivateCollection}
              privateExpiresAt={privateExpiresAt}
              privateUnlocked={privateUnlocked}
               selectedLegacyId={selectedResource?.kind === "legacy" ? selectedResource.id : ""}
               selectedMediaId={selectedResource?.kind === "media" ? selectedResource.id : ""}
              storageSettings={storageSettings}
            />
          ) : null}
          {activeTab === "history" ? (
            <HistoryPanel
              jobs={historyJobs}
              jobsMediaType={mediaType}
              jobsProvider={currentProvider}
              libraryMediaAssets={libraryMediaAssets}
              mediaJobs={allMediaJobs}
              mediaType={mediaType}
              onMediaTypeChange={setMediaType}
              onCopyJobParams={copyJobParams}
              onOpenAsset={openMediaAsset}
              onProviderChange={setCurrentProvider}
              onRefresh={async () => {
                await Promise.all([actions.refreshImages(), refreshMediaData()]);
              }}
              onRetryJob={retryJob}
              onRestoreJob={restoreJobToGenerate}
              onSaveJobAsPreset={handleSaveJobAsPreset}
              onUseAssetAsReference={useMediaAssetAsI2iFromHistory}
              onUseLegacyOutputAsReference={useLegacyOutputAsI2iFromHistory}
              provider={currentProvider}
              targetJobId={targetJobRef?.id}
              targetJobKind={targetJobRef?.kind}
               onJobScrolled={() => setTargetJobRef(undefined)}
               onOpenJobLogs={openJobLogs}
            />
          ) : null}
          {activeTab === "settings" ? (
            <div className="grid gap-3">
               <SubTabs
                 activeId={settingsSubTab}
                 onChange={(id) => setSettingsSubTab(id as "providers" | "storage")}
                 tabs={[
                   { id: "providers", label: "供应商" },
                   { id: "storage", label: "存储" },
                 ]}
               />
               {settingsSubTab === "providers" ? (
                 <div className="grid gap-3">
                   {(providersStatus?.providers || []).length > 0 ? (
                     <div className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
                       <div className="flex items-center justify-between gap-2">
                         <div className="muted text-xs">各 Provider 默认模型（详细配置见下方卡片）</div>
                         <div className="flex flex-wrap justify-end gap-2 max-md:hidden">
                           <Pill>{PROVIDERS.find((p) => p.id === currentProvider)?.label || currentProvider}</Pill>
                           <Pill>{MEDIA_TYPES.find((t) => t.id === mediaType)?.label}</Pill>
                         </div>
                       </div>
                       <div className="grid grid-cols-2 gap-2 max-md:grid-cols-1">
                         {(providersStatus?.providers || []).map((p) => (
                           <div key={p.provider} className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] items-center gap-x-2 gap-y-1 rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1.5">
                             <Pill>{PROVIDERS.find((pr) => pr.id === p.provider)?.label || p.provider}</Pill>
                             <span className="mono truncate text-xs">图 {p.defaultImageModel || "-"}</span>
                             {p.defaultVideoModel ? (
                               <span className="mono col-start-2 truncate text-xs text-[var(--muted-strong)]">视 {p.defaultVideoModel}</span>
                             ) : null}
                           </div>
                         ))}
                       </div>
                     </div>
                   ) : null}
                   <MediaProviderSettingsPanel
                     busy={busy}
                     legacyImageSettings={{
                       defaultModel: settings.defaultModel,
                       defaultResponseFormat: settings.defaultResponseFormat,
                       defaultResolution: settings.defaultResolution,
                       defaultAspectRatio: settings.defaultAspectRatio,
                       historyRetention: settings.historyRetention,
                     }}
                     models={providersStatus?.models || []}
                     onSave={saveMediaProviderSettings}
                     onTest={testMediaProvider}
                     providers={providersStatus?.providers || []}
                   />
                 </div>
               ) : null}
              {settingsSubTab === "storage" ? (
                <ImageStorageSettingsPanel
                  busy={busy === "storage" || busy === "storage-test"}
                  objectProfiles={objectProfiles}
                  onSave={saveStorageSettings}
                  onTest={testStorageSettings}
                  settings={storageSettings}
                />
              ) : null}
            </div>
          ) : null}
        </div>
        <ImagesInspector
          activeTab={activeTab}
          asset={selectedAsset}
          assets={libraryAssets}
          jobs={historyJobs}
          libraryScope={libraryScope}
          mediaAsset={selectedMediaAsset}
          mediaAssets={libraryMediaAssets}
          mediaType={mediaType}
          onArchive={(asset) => void archiveAsset(asset)}
          onArchiveMedia={(asset) => void archiveMediaAsset(asset)}
          onDelete={(asset) => void deleteAsset(asset)}
          onDeleteMedia={(asset) => void deleteMediaAsset(asset)}
          onMarkPrivate={(asset, nextPrivate) => void setAssetPrivate(asset, nextPrivate)}
          onMarkPrivateMedia={(asset, nextPrivate) => void setMediaAssetPrivate(asset, nextPrivate)}
          onOpenJob={(jobId, kind) => openJobFromAsset(jobId, kind)}
          onUseMediaForImage={useMediaAssetForImageToImage}
          prompt={selectedPrompt}
          prompts={prompts}
          status={status}
          storageSettings={storageSettings}
        />
      </div>
      {dangerConfirmDialog}
    </>
  );
}

function isActiveImageJob(job: ImageGenerationJob): boolean {
  return job.status === "queued" || job.status === "running";
}

function isActiveMediaJob(job: MediaGenerationJob): boolean {
  return job.status === "queued" || job.status === "running" || job.status === "provider_queued";
}

function imageAssetTitle(asset: ImageAsset): string {
  return asset.originalFilename || asset.revisedPromptPreview || asset.promptPreview || asset.id;
}
