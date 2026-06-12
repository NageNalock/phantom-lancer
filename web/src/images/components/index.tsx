import type { FormEvent, ReactNode } from "react";
import { useEffect, useState } from "react";
import type { ImageAsset, ImageGenerationJob, ImageProviderSettings, ImageStatus, ImageStorageSettings, ObjectStorageProfile, Tone } from "../../app/types";
import { Button, CheckLabel, ContextList, EmptyState, Field, ImageDropInput, Notice, Panel, Pill, SubTabs } from "../../components/ui";
import { formatBytes } from "../../utils/format";
import { defaultImageSettings, defaultImageStorageSettings, formatDate, imageAssetTypeLabel, imageJobStatusLabel, imageModeLabel, imageStatusLabel, imageStorageBackendLabel } from "../../domain/labels";
import type { ImageLibraryScope, ImageMode, ImageSettingsDraft, ImagesTab, ImageStorageSettingsDraft } from "../types";
import { ASPECT_OPTIONS, IMAGE_MODES, MODEL_OPTIONS, RESOLUTION_OPTIONS } from "../types";

export function ImagesTabs({ active, onChange }: { active: ImagesTab; onChange: (tab: ImagesTab) => void }) {
  const tabs: Array<{ id: ImagesTab; label: string }> = [
    { id: "generate", label: "Generate" },
    { id: "library", label: "Library" },
    { id: "history", label: "History" },
    { id: "settings", label: "Settings" },
  ];
  return <SubTabs activeId={active} onChange={(id) => onChange(id as ImagesTab)} tabs={tabs} />;
}

export function GeneratePanel({
  busy,
  hasApiKey,
  libraryImage,
  latestJob,
  onClearLibraryImage,
  settings,
  storageSettings,
  onSubmit,
}: {
  busy: boolean;
  hasApiKey: boolean;
  libraryImage?: ImageAsset;
  latestJob?: ImageGenerationJob;
  onClearLibraryImage?: () => void;
  settings: ImageProviderSettings;
  storageSettings: ImageStorageSettings;
  onSubmit: (data: FormData) => Promise<void>;
}) {
  const [mode, setMode] = useState<ImageMode>("text_to_image");
  const defaults = { ...defaultImageSettings(), ...settings };
  const responseFormatDefault = objectStorageEnabled(storageSettings) ? "b64_json" : defaults.defaultResponseFormat;
  const referenceSlots = mode === "text_to_image" ? 0 : mode === "image_to_image" ? 1 : 3;

  useEffect(() => {
    if (libraryImage) setMode("image_to_image");
  }, [libraryImage]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    await onSubmit(new FormData(event.currentTarget));
  }

  return (
    <div className="grid grid-cols-[minmax(0,1fr)_minmax(300px,0.85fr)] gap-4 max-xl:grid-cols-1">
      <Panel
        actions={
          <Button disabled={busy || !hasApiKey} tone="primary" type="submit" form="imagesGenerateForm">
            {busy ? "调用中" : "生成"}
          </Button>
        }
        subtitle="所有调用都经过后端校验、密钥边界和历史记录。"
        title="生成任务"
      >
        <form className="grid gap-4" id="imagesGenerateForm" onSubmit={(event) => void submit(event)}>
          <fieldset className="m-0 grid grid-cols-3 gap-1 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-1 max-md:grid-cols-1">
            <legend className="sr-only">调用类型</legend>
            {IMAGE_MODES.map((item) => (
              <label className={`grid gap-1 rounded-md px-3 py-2 text-sm transition ${mode === item.id ? "bg-[var(--surface)] shadow-sm" : "hover:bg-[var(--surface)]"}`} key={item.id}>
                <span className="font-medium">{item.label}</span>
                <small className="muted text-xs">{item.hint}</small>
                <input checked={mode === item.id} className="sr-only" name="mode" onChange={() => setMode(item.id)} type="radio" value={item.id} />
              </label>
            ))}
          </fieldset>

          <Field label="Prompt" help="最多 8000 字符；详细描述主体、风格、镜头和约束。">
            <textarea className="textarea min-h-44" maxLength={8000} name="prompt" required />
          </Field>

          <div className="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
            <div className="col-span-2 max-sm:col-span-1">
              <Field label="模型">
                <select className="select mono" defaultValue={defaults.defaultModel} name="model">
                  {MODEL_OPTIONS.map((model) => (
                    <option key={model} value={model}>
                      {model}
                    </option>
                  ))}
                </select>
              </Field>
            </div>
            <Field label="比例">
              <select className="select mono" defaultValue={defaults.defaultAspectRatio} name="aspect_ratio">
                {ASPECT_OPTIONS.map((value) => (
                  <option key={value || "default"} value={value}>
                    {value || "默认"}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="分辨率">
              <select className="select mono" defaultValue={defaults.defaultResolution} name="resolution">
                {RESOLUTION_OPTIONS.map((value) => (
                  <option key={value || "default"} value={value}>
                    {value || "默认"}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="数量">
              <input className="input mono" defaultValue={1} max={10} min={1} name="n" type="number" />
            </Field>
            <Field label="响应">
              <select className="select mono" defaultValue={responseFormatDefault} name="response_format">
                <option value="url">url</option>
                <option value="b64_json">b64_json</option>
              </select>
            </Field>
          </div>

          {referenceSlots > 0 ? (
            <section className="grid gap-3 card-soft">
              <div className="flex items-center justify-between gap-3">
                <strong className="text-sm">参考图</strong>
                <span className="muted text-xs">{mode === "image_to_image" ? "需要 1 张" : "需要 2-3 张"}</span>
              </div>
              <div className="grid grid-cols-1 gap-3 2xl:grid-cols-3">
                {Array.from({ length: referenceSlots }, (_, index) => (
                  <ReferenceSlot index={index + 1} key={`${index}-${libraryImage?.id || "empty"}`} libraryImage={mode === "image_to_image" && index === 0 ? libraryImage : undefined} onClearLibraryImage={index === 0 ? onClearLibraryImage : undefined} />
                ))}
              </div>
            </section>
          ) : null}

          {!hasApiKey ? <Notice>需要先在本模块 Settings 中配置 xAI API Key，才能发起模型调用。</Notice> : null}
        </form>
      </Panel>

      <Panel title="本次结果" subtitle="最近一次生成结果会保留在这里；完整记录见 History。">
        {latestJob ? <JobCard job={latestJob} /> : <EmptyState title="等待生成" body="填写 prompt 后创建一次 Images job。" />}
      </Panel>
    </div>
  );
}

function ReferenceSlot({ index, libraryImage, onClearLibraryImage }: { index: number; libraryImage?: ImageAsset; onClearLibraryImage?: () => void }) {
  return (
    <div className="grid min-h-44 content-start gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3">
      <strong className="mono text-xs text-[var(--muted-strong)]">source {String(index).padStart(2, "0")}</strong>
      {libraryImage ? (
        <div className="grid gap-2 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-2">
          {libraryImage.url ? <img alt={assetTitle(libraryImage)} className="aspect-video w-full rounded border border-[var(--line)] object-cover" src={libraryImage.url} /> : null}
          <input name={`image_asset_${index}`} type="hidden" value={libraryImage.id} />
          <div className="flex items-center justify-between gap-2">
            <span className="min-w-0 truncate text-xs font-medium">{assetTitle(libraryImage)}</span>
            <Button className="min-h-7 px-2 text-xs" onClick={onClearLibraryImage} type="button">
              清除
            </Button>
          </div>
        </div>
      ) : null}
      <Field label="URL">
        <input className="input mono" disabled={Boolean(libraryImage)} name={`image_url_${index}`} placeholder="https://..." type="url" />
      </Field>
      <Field label="上传">
        <ImageDropInput disabled={Boolean(libraryImage)} label="上传参考图" name={`image_file_${index}`} />
      </Field>
    </div>
  );
}

export function HistoryPanel({ jobs, onRefresh }: { jobs: ImageGenerationJob[]; onRefresh: () => Promise<void> }) {
  return (
    <Panel actions={<Button onClick={() => void onRefresh()}>刷新</Button>} subtitle="成功和失败的调用都会保留，便于追踪模型参数与上游错误。" title="历史记录">
      {jobs.length ? (
        <div className="grid gap-3">
          {jobs.map((job) => (
            <JobCard job={job} key={job.id} />
          ))}
        </div>
      ) : (
        <EmptyState title="暂无历史" body="生成或编辑图片后，这里会展示调用记录。" />
      )}
    </Panel>
  );
}

export function LibraryPanel({
  assets,
  busy,
  libraryScope,
  onArchive,
  onDelete,
  onLockPrivate,
  onMarkPrivate,
  onRefresh,
  onSelect,
  onScopeChange,
  onUpload,
  onUseForImage,
  onUnlockPrivate,
  privateExpiresAt,
  privateUnlocked,
  selectedId,
  storageSettings,
}: {
  assets: ImageAsset[];
  busy: string;
  libraryScope: ImageLibraryScope;
  onArchive: (asset: ImageAsset) => void;
  onDelete: (asset: ImageAsset) => void;
  onLockPrivate: () => void;
  onMarkPrivate: (asset: ImageAsset, nextPrivate: boolean) => void;
  onRefresh: () => Promise<void>;
  onSelect: (asset: ImageAsset) => void;
  onScopeChange: (scope: ImageLibraryScope) => void;
  onUpload: (data: FormData) => Promise<boolean>;
  onUseForImage: (asset: ImageAsset) => void;
  onUnlockPrivate: (password: string) => Promise<void>;
  privateExpiresAt?: string;
  privateUnlocked: boolean;
  selectedId?: string;
  storageSettings: ImageStorageSettings;
}) {
  const [viewer, setViewer] = useState<ImageAsset | null>(null);
  const [uploadFile, setUploadFile] = useState<File | null>(null);
  const privateScope = libraryScope === "private";
  const generated = assets.filter((asset) => asset.assetType === "generated").length;
  const uploaded = assets.filter((asset) => asset.assetType === "source_upload" || asset.assetType === "manual_upload").length;
  const s3Count = assets.filter((asset) => asset.storageBackend === "s3").length;

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

  return (
    <>
      <Panel
        actions={
          <>
            {privateScope && privateUnlocked ? (
              <Button disabled={busy === "private-lock"} onClick={onLockPrivate}>
                锁定
              </Button>
            ) : null}
            <Button onClick={() => void onRefresh()}>刷新</Button>
          </>
        }
        subtitle={privateScope ? "私密收藏夹需要重新输入 owner 密码解锁；解锁只在当前 session 内短期有效。" : "生成结果和用户上传参考图会进入图片库；对象存储私有读通过后端代理返回。"}
        title={privateScope ? "私密收藏夹" : "图片库"}
      >
        <LibraryScopeSwitch active={libraryScope} onChange={onScopeChange} />
        {privateScope && !privateUnlocked ? (
          <PrivateUnlockPanel busy={busy === "private-unlock"} onUnlock={onUnlockPrivate} />
        ) : assets.length ? (
          <div className="grid gap-4">
            {!privateScope ? <LibraryUploadPanel busy={busy === "upload"} file={uploadFile} onFileChange={setUploadFile} onSubmit={submitUpload} /> : null}
            <div className="grid grid-cols-4 gap-2 max-lg:grid-cols-2 max-sm:grid-cols-1">
              <LibraryMetric label="全部" value={assets.length} />
              <LibraryMetric label="生成结果" value={generated} />
              <LibraryMetric label="用户上传" value={uploaded} />
              <LibraryMetric label="对象存储" value={s3Count} />
            </div>
            <div className="grid grid-cols-[repeat(auto-fill,minmax(180px,1fr))] gap-3">
              {assets.map((asset) => {
                const selected = asset.id === selectedId;
                const archiveEnabled = canArchiveAsset(asset, storageSettings);
                const deleteBusy = busy === `delete:${asset.id}`;
                const archiveBusy = busy === `archive:${asset.id}`;
                return (
                  <article className={`grid min-w-0 gap-2 rounded-lg border bg-[var(--surface)] p-2 transition ${selected ? "border-[var(--accent)] shadow-[inset_2px_0_0_var(--accent)]" : "border-[var(--line)] hover:border-[var(--line-strong)]"}`} key={asset.id}>
                    <button
                      className="group relative block aspect-square overflow-hidden rounded-md border border-[var(--line)] bg-[var(--surface-soft)] text-left"
                      onClick={() => {
                        onSelect(asset);
                        setViewer(asset);
                      }}
                      type="button"
                    >
                      {asset.url ? <img alt={assetTitle(asset)} className="h-full w-full object-cover transition group-hover:scale-[1.01]" src={asset.url} /> : <div className="grid h-full place-items-center text-xs text-[var(--muted)]">no image</div>}
                      <span className="absolute top-2 left-2">
                        <Pill tone={asset.storageBackend === "s3" ? "good" : "neutral"}>{imageStorageBackendLabel(asset.storageBackend)}</Pill>
                      </span>
                      {asset.private ? (
                        <span className="absolute top-2 right-2">
                          <Pill tone="warn">私密</Pill>
                        </span>
                      ) : null}
                    </button>
                    <button className="min-w-0 text-left" onClick={() => onSelect(asset)} type="button">
                      <strong className="line-clamp-2 text-sm leading-snug">{assetTitle(asset)}</strong>
                      <span className="muted mt-1 block truncate text-xs">{imageAssetTypeLabel(asset.assetType)} · {formatDate(asset.createdAt) || "-"}</span>
                    </button>
                    <div className="muted mono flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-xs">
                      <span>{asset.width && asset.height ? `${asset.width}x${asset.height}` : "unknown"}</span>
                      <span>{formatBytes(asset.sizeBytes)}</span>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      <a className="button min-h-8 px-2 text-xs" href={assetDownloadURL(asset)}>
                        下载
                      </a>
                      <Button className="min-h-8 px-2 text-xs" disabled={!archiveEnabled || archiveBusy} onClick={() => onArchive(asset)}>
                        {archiveBusy ? "归档中" : "归档"}
                      </Button>
                      <Button className="min-h-8 px-2 text-xs" onClick={() => onUseForImage(asset)}>
                        用于图生图
                      </Button>
                      <Button className="min-h-8 px-2 text-xs" disabled={busy === `private:${asset.id}`} onClick={() => onMarkPrivate(asset, !asset.private)}>
                        {asset.private ? "移出私密" : "设为私密"}
                      </Button>
                      <Button className="min-h-8 px-2 text-xs" disabled={deleteBusy} onClick={() => confirmDelete(asset, onDelete)} tone="danger">
                        {deleteBusy ? "删除中" : "删除"}
                      </Button>
                    </div>
                  </article>
                );
              })}
            </div>
          </div>
        ) : (
          <div className="grid gap-4">
            {!privateScope ? <LibraryUploadPanel busy={busy === "upload"} file={uploadFile} onFileChange={setUploadFile} onSubmit={submitUpload} /> : null}
            <EmptyState title={privateScope ? "暂无私密图片" : "暂无图片"} body={privateScope ? "在普通图片库中将图片设为私密后，这里会展示。" : "生成图片或手动上传后，图片会自动进入这里。"} />
          </div>
        )}
        {privateScope && privateUnlocked && privateExpiresAt ? <p className="muted mt-3 mb-0 text-xs">解锁有效至 {formatDate(privateExpiresAt)}</p> : null}
      </Panel>
      {viewer ? <ImageViewer asset={viewer} onArchive={onArchive} onClose={() => setViewer(null)} onDelete={onDelete} onMarkPrivate={onMarkPrivate} storageSettings={storageSettings} /> : null}
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
        <ImageDropInput key={file ? "selected" : "empty"} label="上传到 Library" onFiles={(files) => onFileChange(files[0] || null)} />
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
    { id: "public", label: "图片库" },
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
        <input className="input mono" onChange={(event) => setPassword(event.target.value)} required type="password" value={password} />
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
  onArchive,
  onClose,
  onDelete,
  onMarkPrivate,
  storageSettings,
}: {
  asset: ImageAsset;
  onArchive: (asset: ImageAsset) => void;
  onClose: () => void;
  onDelete: (asset: ImageAsset) => void;
  onMarkPrivate: (asset: ImageAsset, nextPrivate: boolean) => void;
  storageSettings: ImageStorageSettings;
}) {
  return (
    <div className="fixed inset-0 z-40 grid place-items-center bg-[rgba(16,18,22,0.62)] p-4" onClick={onClose} role="dialog" aria-modal="true">
      <div className="grid max-h-[92dvh] w-full max-w-6xl grid-cols-[minmax(0,1fr)_320px] overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)] max-lg:grid-cols-1" onClick={(event) => event.stopPropagation()}>
        <div className="grid min-h-[320px] place-items-center overflow-auto bg-[var(--surface-soft)] p-4">
          {asset.url ? <img alt={assetTitle(asset)} className="max-h-[76dvh] max-w-full rounded-lg border border-[var(--line)] object-contain" src={asset.url} /> : <div className="text-sm text-[var(--muted)]">no image</div>}
        </div>
        <aside className="grid content-start gap-3 border-l border-[var(--line)] p-4 max-lg:border-l-0 max-lg:border-t">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <h3 className="m-0 break-words text-sm font-semibold">{assetTitle(asset)}</h3>
              <p className="muted mt-1 mb-0 text-xs">{imageAssetTypeLabel(asset.assetType)}</p>
            </div>
            <Button className="min-h-8 px-2 text-xs" onClick={onClose}>
              关闭
            </Button>
          </div>
          <ContextList items={assetMetadata(asset)} />
          <div className="flex flex-wrap gap-2">
            <a className="button min-h-8 px-2 text-xs" href={assetDownloadURL(asset)}>
              下载
            </a>
            <Button className="min-h-8 px-2 text-xs" disabled={!canArchiveAsset(asset, storageSettings)} onClick={() => onArchive(asset)}>
              归档到 S3
            </Button>
            <Button className="min-h-8 px-2 text-xs" onClick={() => onMarkPrivate(asset, !asset.private)}>
              {asset.private ? "移出私密" : "设为私密"}
            </Button>
            <Button className="min-h-8 px-2 text-xs" onClick={() => confirmDelete(asset, onDelete)} tone="danger">
              删除
            </Button>
          </div>
        </aside>
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
      subtitle="Provider 设置属于 Images 模块，不进入全局运行设置。"
      title="Provider 设置"
    >
      <div className="grid gap-4">
        <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
          <Field label="Provider">
            <input className="input mono" disabled value={draft.provider} />
          </Field>
          <Field label="API Key 状态">
            <input className="input mono" disabled value={draft.hasApiKey ? draft.maskedApiKey || "configured" : "未配置"} />
          </Field>
        </div>
        <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 max-md:grid-cols-1">
          <Field label="xAI API Key" help="留空表示不修改现有 key；清除时不会在审计中写入明文。">
            <input className="input mono" onChange={(event) => updateDraft("xaiApiKey", event.target.value)} type="password" value={draft.xaiApiKey} />
          </Field>
          <div className="flex min-h-9 items-end pb-2">
            <CheckLabel
              checked={draft.clearApiKey}
              onChange={(checked) => updateDraft("clearApiKey", checked)}
            >
              清除 API Key
            </CheckLabel>
          </div>
        </div>

        <div className="grid grid-cols-4 gap-3 max-lg:grid-cols-2 max-md:grid-cols-1">
          <Field label="默认模型">
            <select className="select mono" onChange={(event) => updateDraft("defaultModel", event.target.value)} value={draft.defaultModel}>
              {MODEL_OPTIONS.map((model) => (
                <option key={model} value={model}>
                  {model}
                </option>
              ))}
            </select>
          </Field>
          <Field label="默认响应">
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
  const s3Enabled = draft.backend === "s3";
  const objectStorageEnabled = draft.backend === "object_storage";
  const canTest = s3Enabled || (objectStorageEnabled && Boolean(draft.objectStorageProfileId));
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
          <Field label="Backend">
            <select className="select mono" onChange={(event) => updateBackend(event.target.value)} value={draft.backend}>
              <option value="local">local</option>
              <option value="object_storage">object_storage profile</option>
              <option value="s3">s3 compatible</option>
            </select>
          </Field>
          <Field label={objectStorageEnabled ? "Object Profile" : "S3 Access Key"}>
            <input className="input mono" disabled value={objectStorageEnabled ? selectedProfileLabel(selectedProfile, draft.objectStorageProfileId) : draft.hasS3Credentials ? draft.maskedAccessKeyId || "configured" : "未配置"} />
          </Field>
          <Field label="读取方式">
            <input className="input mono" disabled value="private bucket / backend proxy" />
          </Field>
        </div>

        {objectStorageEnabled ? (
          <fieldset className="m-0 grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <legend className="px-1 text-xs font-medium text-[var(--muted-strong)]">共享对象存储</legend>
            {objectProfiles.length ? null : <Notice>先在全局 Settings / Object Storage 创建并测试一个 profile。</Notice>}
            <div className="grid grid-cols-[minmax(0,1fr)_minmax(220px,0.5fr)] gap-3 max-lg:grid-cols-1">
              <Field label="Object Storage Profile" help="profile 在全局 Settings / Object Storage 中维护，密钥不会回显。">
                <select className="select mono" onChange={(event) => updateDraft("objectStorageProfileId", event.target.value)} value={draft.objectStorageProfileId || ""}>
                  <option value="">选择 profile</option>
                  {objectProfiles.map((profile) => (
                    <option key={profile.id} value={profile.id}>
                      {(profile.name || profile.id) + " · " + profile.bucket}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="Prefix">
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

        {!objectStorageEnabled ? (
          <fieldset className="m-0 grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" disabled={!s3Enabled}>
            <legend className="px-1 text-xs font-medium text-[var(--muted-strong)]">S3 兼容对象存储</legend>
            <div className="grid grid-cols-3 gap-3 max-lg:grid-cols-1">
              <Field label="Provider Label" help="用于展示和审计，例如 aliyun、tencent、minio。">
                <input className="input mono" onChange={(event) => updateDraft("s3ProviderLabel", event.target.value)} placeholder="aliyun" value={draft.s3ProviderLabel} />
              </Field>
              <Field label="Bucket">
                <input className="input mono" onChange={(event) => updateDraft("s3Bucket", event.target.value)} value={draft.s3Bucket} />
              </Field>
              <Field label="Region">
                <input className="input mono" onChange={(event) => updateDraft("s3Region", event.target.value)} placeholder="auto / oss-cn-hangzhou" value={draft.s3Region} />
              </Field>
            </div>
            <div className="grid grid-cols-[minmax(0,1fr)_minmax(220px,0.5fr)] gap-3 max-lg:grid-cols-1">
              <Field label="Endpoint" help="填写服务商的 S3 兼容 endpoint，bucket 不需要公网读。">
                <input className="input mono" onChange={(event) => updateDraft("s3Endpoint", event.target.value)} placeholder="https://oss-cn-hangzhou.aliyuncs.com" value={draft.s3Endpoint} />
              </Field>
              <Field label="Prefix">
                <input className="input mono" onChange={(event) => updateDraft("s3Prefix", event.target.value)} value={draft.s3Prefix} />
              </Field>
            </div>
            <div className="grid grid-cols-3 gap-3 max-lg:grid-cols-1">
              <Field label="Access Key ID">
                <input className="input mono" onChange={(event) => updateDraft("s3AccessKeyId", event.target.value)} type="password" value={draft.s3AccessKeyId} />
              </Field>
              <Field label="Secret Access Key">
                <input className="input mono" onChange={(event) => updateDraft("s3SecretAccessKey", event.target.value)} type="password" value={draft.s3SecretAccessKey} />
              </Field>
              <Field label="Session Token">
                <input className="input mono" onChange={(event) => updateDraft("s3SessionToken", event.target.value)} type="password" value={draft.s3SessionToken} />
              </Field>
            </div>
            <div className="flex flex-wrap gap-4 text-sm">
              <CheckLabel
                checked={draft.s3ForcePathStyle}
                onChange={(checked) => updateDraft("s3ForcePathStyle", checked)}
              >
                Force path style
              </CheckLabel>
              <CheckLabel
                checked={draft.fallbackToLocal}
                onChange={(checked) => updateDraft("fallbackToLocal", checked)}
              >
                写入失败回退本地
              </CheckLabel>
              <CheckLabel
                checked={draft.clearSecret}
                onChange={(checked) => updateDraft("clearSecret", checked)}
              >
                清除 S3 密钥
              </CheckLabel>
            </div>
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
  asset,
  assets = [],
  jobs,
  libraryScope,
  onArchive,
  onDelete,
  onMarkPrivate,
  status,
  storageSettings,
}: {
  asset?: ImageAsset;
  assets?: ImageAsset[];
  jobs: ImageGenerationJob[];
  libraryScope?: ImageLibraryScope;
  onArchive?: (asset: ImageAsset) => void;
  onDelete?: (asset: ImageAsset) => void;
  onMarkPrivate?: (asset: ImageAsset, nextPrivate: boolean) => void;
  status?: ImageStatus;
  storageSettings?: ImageStorageSettings;
}) {
  const last = jobs[0];
  const tone: Tone = status?.hasApiKey ? (status?.lastJobStatus === "failed" ? "warn" : "good") : "warn";
  const localAssets = assets.filter((item) => item.storageBackend === "local").length;
  const s3Assets = assets.filter((item) => item.storageBackend === "s3").length;
  return (
    <aside className="grid content-start gap-4 border-l border-[var(--line)] bg-[var(--surface-soft)] p-4 max-xl:border-l-0 max-xl:border-t">
      <Panel title="Images">
        <ContextList
          items={[
            ["状态", <Pill tone={tone}>{imageStatusLabel(status)}</Pill>],
            ["Provider", status?.provider || "xai"],
            ["API Key", status?.hasApiKey ? status.maskedApiKey || "configured" : "未配置"],
            ["历史", status?.historyCount ?? jobs.length],
            [libraryScope === "private" ? "私密图" : "图片库", assets.length],
            ["存储", imageStorageBackendLabel(storageSettings?.backend)],
            ["最近任务", last ? `${imageModeLabel(last.mode)} / ${imageJobStatusLabel(last.status)}` : "-"],
            ["错误", status?.lastError || "-"],
          ]}
        />
      </Panel>
      <Panel title="选中图片">
        {asset ? (
          <div className="grid gap-3">
            {asset.url ? <img alt={assetTitle(asset)} className="aspect-square w-full rounded-lg border border-[var(--line)] object-cover" src={asset.url} /> : null}
            <ContextList items={assetMetadata(asset)} />
            <div className="flex flex-wrap gap-2">
              <a className="button min-h-8 px-2 text-xs" href={assetDownloadURL(asset)}>
                下载
              </a>
              <Button className="min-h-8 px-2 text-xs" disabled={!canArchiveAsset(asset, storageSettings)} onClick={() => onArchive?.(asset)}>
                归档
              </Button>
              <Button className="min-h-8 px-2 text-xs" onClick={() => onMarkPrivate?.(asset, !asset.private)}>
                {asset.private ? "移出私密" : "设为私密"}
              </Button>
              <Button className="min-h-8 px-2 text-xs" onClick={() => confirmDelete(asset, (next) => onDelete?.(next))} tone="danger">
                删除
              </Button>
            </div>
          </div>
        ) : (
          <EmptyState title="未选择图片" body="在 Library 中选择一张图片查看元数据。" />
        )}
      </Panel>
      <Panel title="参数边界">
        <ContextList
          items={[
            ["数量", "1-10"],
            ["上传", "jpeg/png/gif/webp, <=12 MB"],
            ["模式", "文生图 / 图生图 / 多图编辑"],
            ["本地资产", localAssets],
            ["S3 资产", s3Assets],
            ["当前视图", libraryScope === "private" ? "私密收藏夹" : "图片库"],
            ["读取", "登录后经后端代理"],
          ]}
        />
      </Panel>
    </aside>
  );
}

function selectedProfileLabel(profile?: ObjectStorageProfile, id?: string): string {
  if (profile) return `${profile.name || profile.id} / ${profile.bucket}`;
  if (id) return "profile missing";
  return "未选择";
}

function JobCard({ job }: { job: ImageGenerationJob }) {
  const statusTone: Tone = job.status === "success" ? "good" : job.status === "failed" ? "danger" : "warn";
  return (
    <article className="grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Pill tone={statusTone}>{imageJobStatusLabel(job.status)}</Pill>
        <Pill>{imageModeLabel(job.mode)}</Pill>
        <Pill>{job.model || "model"}</Pill>
        {job.endpoint ? <Pill>{job.endpoint}</Pill> : null}
        <span className="muted ml-auto text-xs max-md:ml-0">{formatDate(job.completedAt || job.createdAt) || "-"}</span>
      </div>
      {job.prompt ? <p className="m-0 line-clamp-3 text-sm leading-relaxed">{job.prompt}</p> : null}
      {job.errorMessage ? <div className="rounded-md border border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] p-2 text-xs text-[var(--danger)]">{job.errorMessage}</div> : null}
      {job.outputs?.length ? (
        <div className="grid grid-cols-3 gap-2 max-lg:grid-cols-2 max-sm:grid-cols-1">
          {job.outputs.map((output) => (
            <div className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-2" key={output.id || `${job.id}-${output.slot}`}>
              {output.url ? <img alt={job.modeLabel || "generated image"} className="aspect-square w-full rounded-md border border-[var(--line)] object-cover" src={output.url} /> : <div className="grid aspect-square place-items-center rounded-md border border-[var(--line)] text-xs text-[var(--muted)]">no image</div>}
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
            </div>
          ))}
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

function objectStorageEnabled(settings?: ImageStorageSettings): boolean {
  const backend = settings?.backend;
  return backend === "s3" || (backend === "object_storage" && Boolean(settings?.objectStorageProfileId));
}

function canArchiveAsset(asset?: ImageAsset, settings?: ImageStorageSettings): boolean {
  const archivableStorage = asset?.storageBackend === "local" || asset?.storageBackend === "remote";
  return Boolean(asset?.id && archivableStorage && objectStorageEnabled(settings) && !asset.deletedAt);
}

function confirmDelete(asset: ImageAsset, onDelete: (asset: ImageAsset) => void) {
  if (window.confirm(`删除图片 ${assetTitle(asset)}？`)) onDelete(asset);
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

function shortHash(value?: string): string {
  if (!value) return "-";
  return value.length <= 16 ? value : `${value.slice(0, 12)}...${value.slice(-6)}`;
}
