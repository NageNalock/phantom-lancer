import { useEffect, useMemo, useState } from "react";
import type { AppActions } from "../app/App";
import type { ApiError, AppData, ImageAsset, ImageGenerationJob, ObjectStorageProfile } from "../app/types";
import { friendlyError } from "../api/client";
import { defaultImageSettings, defaultImageStorageSettings } from "../domain/labels";
import { GeneratePanel, HistoryPanel, ImageStorageSettingsPanel, ImagesInspector, ImagesTabs, LibraryPanel, ProviderSettingsPanel } from "../images/components";
import type { ImageJobResponse, ImageLibraryScope, ImagePrivateStatus, ImageSettingsDraft, ImageStorageSettingsDraft, ImageUploadResponse, ImagesTab } from "../images/types";

export function ImagesView({ actions, data }: { actions: AppActions; data: AppData }) {
  const [activeTab, setActiveTab] = useState<ImagesTab>("generate");
  const [busy, setBusy] = useState("");
  const [currentJob, setCurrentJob] = useState<ImageGenerationJob | undefined>(undefined);
  const [selectedAssetId, setSelectedAssetId] = useState("");
  const [libraryScope, setLibraryScope] = useState<ImageLibraryScope>("public");
  const [privateUnlocked, setPrivateUnlocked] = useState(false);
  const [privateExpiresAt, setPrivateExpiresAt] = useState("");
  const [privateAssets, setPrivateAssets] = useState<ImageAsset[]>([]);
  const [imageToImageAsset, setImageToImageAsset] = useState<ImageAsset | undefined>(undefined);
  const [objectProfiles, setObjectProfiles] = useState<ObjectStorageProfile[]>([]);

  const settings = useMemo(() => ({ ...defaultImageSettings(), ...(data.images.settings || {}) }), [data.images.settings]);
  const storageSettings = useMemo(() => ({ ...defaultImageStorageSettings(), ...(data.images.storageSettings || {}) }), [data.images.storageSettings]);
  const status = data.images.status || data.dashboard.images;
  const jobs = data.images.jobs || [];
  const assets = data.images.assets || [];
  const libraryAssets = libraryScope === "private" ? privateAssets : assets;
  const selectedAsset = libraryAssets.find((asset) => asset.id === selectedAssetId) || libraryAssets[0];
  const historyJobs = useMemo(() => {
    if (!currentJob || jobs.some((job) => job.id === currentJob.id)) return jobs;
    return [currentJob, ...jobs];
  }, [currentJob, jobs]);
  const latestJob = currentJob || jobs[0];
  const hasActiveJob = historyJobs.some(isActiveImageJob);

  useEffect(() => {
    if (!currentJob?.id) return;
    const updated = jobs.find((job) => job.id === currentJob.id);
    if (updated) setCurrentJob(updated);
  }, [currentJob?.id, jobs]);

  useEffect(() => {
    if (!selectedAssetId || libraryAssets.some((asset) => asset.id === selectedAssetId)) return;
    setSelectedAssetId("");
  }, [libraryAssets, selectedAssetId]);

  useEffect(() => {
    if (!hasActiveJob) return;
    const timer = window.setInterval(() => {
      void actions.refreshImages();
    }, 2500);
    return () => window.clearInterval(timer);
  }, [actions, hasActiveJob]);

  useEffect(() => {
    if (activeTab !== "library" || libraryScope !== "private") return;
    void refreshPrivateStatus(true);
  }, [activeTab, libraryScope]);

  useEffect(() => {
    if (activeTab !== "settings") return;
    void refreshObjectProfiles();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab]);

  async function submitJob(formData: FormData) {
    setBusy("job");
    try {
      const result = await actions.api<ImageJobResponse>("/api/images/jobs", {
        method: "POST",
        csrf: actions.csrf,
        body: formData,
      });
      if (result.job) {
        setCurrentJob(result.job);
      }
      await actions.refreshImages();
      actions.setToast("Images 任务已提交", "good");
    } catch (error) {
      const payload = (error as ApiError).payload as ImageJobResponse | undefined;
      if (payload?.job) {
        setCurrentJob(payload.job);
        await actions.refreshImages();
      }
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
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
      actions.setToast("Images 设置已保存", "good");
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
      actions.setToast("Images 存储设置已保存", "good");
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

  async function deleteAsset(asset: ImageAsset) {
    setBusy(`delete:${asset.id}`);
    try {
      await actions.api(`/api/images/library/assets/${encodeURIComponent(asset.id)}`, {
        method: "DELETE",
        csrf: actions.csrf,
      });
      await actions.refreshImages();
      if (libraryScope === "private") await refreshPrivateAssets();
      if (selectedAssetId === asset.id) setSelectedAssetId("");
      actions.setToast("图片已删除", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
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
      setSelectedAssetId(result.asset?.id || asset.id);
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
      if (result.asset?.id) setSelectedAssetId(result.asset.id);
      actions.setToast(result.duplicate ? "图片已存在，已复用 Library 资产" : "图片已上传", "good");
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
    setActiveTab("generate");
    actions.setToast("已选择 Library 图片作为图生图参考", "good");
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
      setSelectedAssetId("");
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
        if (selectedAssetId === asset.id) setSelectedAssetId("");
        actions.setToast("已加入私密收藏夹", "good");
      } else {
        setSelectedAssetId(result.asset?.id || "");
        actions.setToast("已移出私密收藏夹", "good");
      }
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="grid min-h-[calc(100dvh-105px)] grid-cols-[minmax(0,1fr)_320px] max-xl:grid-cols-1">
      <div className="grid content-start gap-4 p-4">
        <ImagesTabs active={activeTab} onChange={setActiveTab} />
        {activeTab === "generate" ? <GeneratePanel busy={busy === "job"} hasApiKey={Boolean(settings.hasApiKey)} latestJob={latestJob} libraryImage={imageToImageAsset} onClearLibraryImage={() => setImageToImageAsset(undefined)} onSubmit={submitJob} settings={settings} storageSettings={storageSettings} /> : null}
        {activeTab === "library" ? (
          <LibraryPanel
            assets={libraryAssets}
            busy={busy}
            libraryScope={libraryScope}
            onArchive={(asset) => void archiveAsset(asset)}
            onDelete={(asset) => void deleteAsset(asset)}
            onLockPrivate={() => void lockPrivateCollection()}
            onMarkPrivate={(asset, nextPrivate) => void setAssetPrivate(asset, nextPrivate)}
            onRefresh={libraryScope === "private" ? refreshPrivateAssets : actions.refreshImages}
            onSelect={(asset) => setSelectedAssetId(asset.id)}
            onScopeChange={(scope) => {
              setLibraryScope(scope);
              setSelectedAssetId("");
            }}
            onUpload={uploadAsset}
            onUseForImage={useAssetForImageToImage}
            onUnlockPrivate={unlockPrivateCollection}
            privateExpiresAt={privateExpiresAt}
            privateUnlocked={privateUnlocked}
            selectedId={selectedAsset?.id || ""}
            storageSettings={storageSettings}
          />
        ) : null}
        {activeTab === "history" ? <HistoryPanel jobs={historyJobs} onRefresh={actions.refreshImages} /> : null}
        {activeTab === "settings" ? (
          <div className="grid gap-4">
            <ProviderSettingsPanel busy={busy === "settings"} onSave={saveSettings} settings={settings} />
            <ImageStorageSettingsPanel busy={busy === "storage" || busy === "storage-test"} objectProfiles={objectProfiles} onSave={saveStorageSettings} onTest={testStorageSettings} settings={storageSettings} />
          </div>
        ) : null}
      </div>
      <ImagesInspector asset={selectedAsset} assets={libraryAssets} jobs={historyJobs} libraryScope={libraryScope} onArchive={(asset) => void archiveAsset(asset)} onDelete={(asset) => void deleteAsset(asset)} onMarkPrivate={(asset, nextPrivate) => void setAssetPrivate(asset, nextPrivate)} status={status} storageSettings={storageSettings} />
    </div>
  );
}

function isActiveImageJob(job: ImageGenerationJob): boolean {
  return job.status === "queued" || job.status === "running";
}
