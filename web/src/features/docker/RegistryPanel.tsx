import { useEffect, useMemo, useState } from "react";
import type {
  DockerRegistryCredential,
  DockerRegistryRepository,
  DockerRegistrySettings,
  DockerRegistryStatus,
  DockerRegistryTag,
  ObjectStorageProfile,
} from "../../app/types";
import type { Tone } from "../../app/types";
import { Button, CheckLabel, Field, Metric, Panel, Pill, SubTabs, Toggle } from "../../components/ui";
import { DockerTable, DockerValue } from "./DockerTable";

type RegistryView = "repositories" | "credentials" | "settings";

const REGISTRY_VIEWS: { id: RegistryView; label: string }[] = [
  { id: "repositories", label: "仓库" },
  { id: "credentials", label: "凭据" },
  { id: "settings", label: "设置" },
];

const REGISTRY_SCOPES = [
  { id: "registry.pull", label: "Pull", help: "拉取 manifest 和 blob" },
  { id: "registry.push", label: "Push", help: "推送 blob 和 manifest" },
  { id: "registry.delete", label: "Delete", help: "删除 tag 或 manifest" },
  { id: "registry.admin", label: "Admin", help: "保留给后续管理操作" },
];

const GiB = 1024 * 1024 * 1024;

function registryHost(publicUrl: string | undefined): string {
  const raw = (publicUrl || "").trim();
  if (!raw) {
    return "registry.example.com";
  }
  return raw.replace(/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//, "").replace(/\/+$/, "") || "registry.example.com";
}

function scopeLabel(scope: string): string {
  switch (scope) {
    case "registry.pull":
      return "pull";
    case "registry.push":
      return "push";
    case "registry.delete":
      return "delete";
    case "registry.admin":
      return "admin";
    default:
      return scope.replace(/^registry\./, "");
  }
}

function scopeTone(scope: string): Tone {
  if (scope === "registry.push") return "good";
  if (scope === "registry.delete" || scope === "registry.admin") return "warn";
  return "neutral";
}

function statusTone(status: string | undefined): Tone {
  return status === "active" ? "good" : status === "disabled" || status === "revoked" ? "danger" : "neutral";
}

function registryTagPullBusyKey(tag: DockerRegistryTag): string {
  return `registry-pull-${tag.repository}:${tag.tag}`;
}

function serializeSettings(settings: DockerRegistrySettings): string {
  return JSON.stringify({
    enabled: Boolean(settings.enabled),
    publicUrl: settings.publicUrl || "",
    storageBackend: settings.storageBackend || "local",
    objectStorageProfileId: settings.objectStorageProfileId || "",
    objectPrefix: settings.objectPrefix || "phantom-lancer/docker-registry",
    storageDir: settings.storageDir || "",
    quotaBytes: Number(settings.quotaBytes || 0),
    maxRepositories: Number(settings.maxRepositories || 0),
    maxTagsPerRepository: Number(settings.maxTagsPerRepository || 0),
    requireTls: Boolean(settings.requireTls),
    allowAnonymousPull: Boolean(settings.allowAnonymousPull),
    allowInsecureLocal: Boolean(settings.allowInsecureLocal),
  });
}

export function RegistryPanel({
  busy,
  createCredential,
  credentialName,
  credentialPrefix,
  credentials,
  deleteCredential,
  deleteTag,
  formatBytes,
  loading,
  newCredentialSecret,
  objectProfiles,
  openRepository,
  pullRegistryTag,
  registrySettings,
  registryStatus,
  repoTags,
  repositories,
  rotateCredential,
  runRegistryGC,
  saveRegistrySettings,
  selectedRepo,
  setCredentialName,
  setCredentialPrefix,
  setCredentialStatus,
  useTagForContainer,
}: {
  busy: string;
  createCredential: (scopes: string[]) => void;
  credentialName: string;
  credentialPrefix: string;
  credentials: DockerRegistryCredential[];
  deleteCredential: (item: DockerRegistryCredential) => void;
  deleteTag: (item: DockerRegistryTag) => void;
  formatBytes: (bytes: number) => string;
  loading: boolean;
  newCredentialSecret: string;
  objectProfiles: ObjectStorageProfile[];
  openRepository: (repo: string) => void;
  pullRegistryTag: (item: DockerRegistryTag) => void;
  registrySettings: DockerRegistrySettings;
  registryStatus: DockerRegistryStatus | null;
  repoTags: DockerRegistryTag[];
  repositories: DockerRegistryRepository[];
  rotateCredential: (item: DockerRegistryCredential) => void;
  runRegistryGC: () => void;
  saveRegistrySettings: (settings: DockerRegistrySettings) => void;
  selectedRepo: string;
  setCredentialName: (value: string) => void;
  setCredentialPrefix: (value: string) => void;
  setCredentialStatus: (item: DockerRegistryCredential, status: "active" | "disabled") => void;
  useTagForContainer: (item: DockerRegistryTag) => void;
}) {
  const [view, setView] = useState<RegistryView>("repositories");
  const [credentialScopes, setCredentialScopes] = useState<string[]>(["registry.pull", "registry.push"]);
  const host = registryHost(registrySettings.publicUrl || registryStatus?.publicUrl);

  function toggleScope(scope: string, checked: boolean) {
    setCredentialScopes((current) => {
      const next = checked ? [...current, scope] : current.filter((item) => item !== scope);
      return next.length ? Array.from(new Set(next)) : current;
    });
  }

  return (
    <div className="grid gap-4">
      <div className="grid grid-cols-4 gap-2 max-lg:grid-cols-2">
        <Metric
          label="Registry"
          tone={registryStatus?.enabled ? (registryStatus.ready ? "good" : "warn") : "neutral"}
          value={registryStatus?.enabled ? (registryStatus.ready ? "就绪" : "已启用") : "关闭"}
        />
        <Metric label="公开 URL" value={<span className="mono text-xs">{registryStatus?.publicUrl || "未配置"}</span>} />
        <Metric label="仓库" value={String(registryStatus?.repositoryCount || repositories.length || 0)} detail={`${registryStatus?.credentialCount || credentials.length || 0} 个凭据`} />
        <Metric label="用量" value={formatBytes(registryStatus?.usageBytes || 0)} detail={`${formatBytes(registryStatus?.quotaBytes || registrySettings.quotaBytes || 0)} quota`} />
      </div>

      {newCredentialSecret ? (
        <div className="rounded-lg border border-[var(--warn)] bg-[var(--warn-soft)] p-3">
          <strong className="block text-sm">新 secret 只显示一次</strong>
          <code className="mono mt-2 block break-all text-xs">{newCredentialSecret}</code>
        </div>
      ) : null}

      <SubTabs activeId={view} ariaLabel="Registry 视图" onChange={(id) => setView(id as RegistryView)} tabs={REGISTRY_VIEWS} />

      {view === "repositories" ? (
        <div className="grid gap-4">
          <Panel title="仓库" subtitle="Registry 中已记录的 repository。选中仓库后查看 tag、digest 和本机拉取操作。">
            <DockerTable
              columns={[
                { header: "仓库", width: "38%" },
                { header: "Tag 数", width: "72px" },
                { header: "大小", width: "110px" },
                { header: "最近推送", width: "170px" },
                { header: "操作", width: "120px" },
              ]}
              empty="暂无 Registry 仓库"
              loading={loading}
              rows={repositories.map((item) => ({
                key: item.name,
                cells: [
                  <DockerValue value={item.name} />,
                  <span className="text-xs">{item.tagCount || 0}</span>,
                  <span className="text-xs">{formatBytes(item.sizeBytes || 0)}</span>,
                  <span className="text-xs">{item.lastPushedAt || "-"}</span>,
                  <Button onClick={() => openRepository(item.name)}>查看并拉取</Button>,
                ],
              }))}
            />
          </Panel>

          {selectedRepo ? (
            <Panel title={`Tags · ${selectedRepo}`} subtitle="拉取后会切到本机镜像；删除 tag 不会立即释放 blob，需执行 GC。">
              <DockerTable
                columns={[
                  { header: "Tag", width: "18%" },
                  { header: "Digest", width: "36%" },
                  { header: "大小", width: "100px" },
                  { header: "推送时间", width: "170px" },
                  { header: "操作", width: "260px" },
                ]}
                empty="暂无 tag"
                loading={loading}
                rows={repoTags.map((item) => ({
                  key: `${item.repository}:${item.tag}`,
                  cells: [
                    <DockerValue value={item.tag} />,
                    <DockerValue clamp={false} value={item.digest} />,
                    <span className="text-xs">{formatBytes(item.manifest?.sizeBytes || 0)}</span>,
                    <span className="text-xs">{item.manifest?.pushedAt || item.updatedAt || "-"}</span>,
                    <span className="flex flex-wrap gap-1">
                      <Button disabled={busy === registryTagPullBusyKey(item)} onClick={() => pullRegistryTag(item)}>
                        拉取到本机
                      </Button>
                      <Button onClick={() => useTagForContainer(item)}>用于创建</Button>
                      <Button disabled={busy === `tag-delete-${item.repository}-${item.tag}`} onClick={() => deleteTag(item)} tone="danger">
                        删除
                      </Button>
                    </span>,
                  ],
                }))}
              />
            </Panel>
          ) : null}
        </div>
      ) : view === "credentials" ? (
        <div className="grid grid-cols-[minmax(0,1fr)_360px] gap-4 max-xl:grid-cols-1">
          <Panel title="凭据" subtitle="secret 只在创建或轮换后显示一次；停用会立即阻止该凭据继续访问 Registry。">
            <div className="grid gap-3">
              <Field label="凭据名称">
                <input className="input mono" onChange={(event) => setCredentialName(event.target.value)} value={credentialName} />
              </Field>
              <Field label="仓库前缀" help="例如 personal/ 或 team-a/；留空时后端会回退到 personal/。">
                <input className="input mono" onChange={(event) => setCredentialPrefix(event.target.value)} value={credentialPrefix} />
              </Field>
              <div className="grid grid-cols-2 gap-2">
                {REGISTRY_SCOPES.map((scope) => (
                  <CheckLabel
                    checked={credentialScopes.includes(scope.id)}
                    key={scope.id}
                    onChange={(checked) => toggleScope(scope.id, checked)}
                  >
                    <span className="grid gap-0.5">
                      <span className="mono text-xs">{scope.label}</span>
                      <span className="muted text-xs">{scope.help}</span>
                    </span>
                  </CheckLabel>
                ))}
              </div>
              <Button disabled={busy === "credential-create"} onClick={() => createCredential(credentialScopes)} tone="primary">
                创建凭据
              </Button>
              <div className="grid gap-3">
                {credentials.map((item) => (
                  <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={item.id}>
                    <div className="flex min-w-0 items-start justify-between gap-3">
                      <div className="min-w-0">
                        <div className="flex min-w-0 items-center gap-2">
                          <DockerValue value={item.name} />
                          <Pill tone={statusTone(item.status)}>{item.status || "unknown"}</Pill>
                        </div>
                        <div className="muted mt-2 grid gap-1 text-xs">
                          <span>
                            前缀：<code className="mono">{item.repositoryPrefix || "无限制"}</code>
                          </span>
                          <span>
                            最近使用：<code className="mono">{item.lastUsedAt || "从未使用"}</code>
                          </span>
                        </div>
                      </div>
                      <span className="flex shrink-0 flex-wrap justify-end gap-1">
                        {item.scopes?.map((scope) => (
                          <Pill key={scope} tone={scopeTone(scope)}>
                            {scopeLabel(scope)}
                          </Pill>
                        ))}
                      </span>
                    </div>
                    <div className="mt-3 flex flex-wrap gap-1">
                      <Button disabled={busy === `cred-rotate-${item.id}`} onClick={() => rotateCredential(item)}>
                        轮换
                      </Button>
                      <Button
                        disabled={busy === `cred-status-${item.id}`}
                        onClick={() => setCredentialStatus(item, item.status === "active" ? "disabled" : "active")}
                      >
                        {item.status === "active" ? "停用" : "启用"}
                      </Button>
                      <Button disabled={busy === `cred-delete-${item.id}`} onClick={() => deleteCredential(item)} tone="danger">
                        删除
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </Panel>
          <Panel title="推送指令" subtitle="Docker 术语保留英文，命令值使用等宽展示。">
            <pre className="code-block">{`docker login ${host}
docker tag my-app:latest ${host}/personal/my-app:latest
docker push ${host}/personal/my-app:latest`}</pre>
          </Panel>
        </div>
      ) : (
        <RegistrySettingsPanel
          busy={busy}
          formatBytes={formatBytes}
          objectProfiles={objectProfiles}
          registrySettings={registrySettings}
          registryStatus={registryStatus}
          runRegistryGC={runRegistryGC}
          saveRegistrySettings={saveRegistrySettings}
        />
      )}
    </div>
  );
}

function RegistrySettingsPanel({
  busy,
  formatBytes,
  objectProfiles,
  registrySettings,
  registryStatus,
  runRegistryGC,
  saveRegistrySettings,
}: {
  busy: string;
  formatBytes: (bytes: number) => string;
  objectProfiles: ObjectStorageProfile[];
  registrySettings: DockerRegistrySettings;
  registryStatus: DockerRegistryStatus | null;
  runRegistryGC: () => void;
  saveRegistrySettings: (settings: DockerRegistrySettings) => void;
}) {
  const [draft, setDraft] = useState<DockerRegistrySettings>(registrySettings);
  const saved = useMemo(() => serializeSettings(registrySettings), [registrySettings]);
  const current = useMemo(() => serializeSettings(draft), [draft]);
  const dirty = saved !== current;
  const quotaGiB = Math.round(Number(draft.quotaBytes || GiB) / GiB);
  const insecureHttp = Boolean((draft.publicUrl || "").trim().startsWith("http://"));

  useEffect(() => {
    setDraft(registrySettings);
  }, [saved, registrySettings]);

  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirty]);

  return (
    <Panel
      title="Registry 设置"
      subtitle="配置修改会先停留在本页草稿中，保存后才写入后端。"
      actions={dirty ? <Pill tone="warn">未保存</Pill> : <Pill tone="good">已保存</Pill>}
    >
      <div className="grid max-w-3xl gap-4">
        {dirty ? (
          <div className="rounded-lg border border-[var(--warn)] bg-[var(--warn-soft)] p-3 text-sm">
            <strong className="block">有未保存的 Registry 设置</strong>
            <p className="muted mt-1 mb-0 text-xs">切换一级页面前请保存或重置；刷新浏览器会触发离开确认。</p>
          </div>
        ) : null}
        {insecureHttp ? (
          <div className="rounded-lg border border-[var(--warn)] bg-[var(--warn-soft)] p-3 text-sm">
            <strong className="block">HTTP Registry 仅适合本机调试</strong>
            <p className="muted mt-1 mb-0 text-xs">公网访问应使用 HTTPS；若必须使用 http://localhost 或 127.0.0.1，请显式开启 insecure local。</p>
          </div>
        ) : null}

        <Toggle
          checked={Boolean(draft.enabled)}
          label="启用内嵌 Registry"
          name="docker_registry_enabled"
          onChange={(checked) => setDraft((current) => ({ ...current, enabled: checked }))}
        />
        <Field label="公开 URL" help="示例：https://registry.example.com；不能包含 token/query。">
          <input className="input mono" onChange={(event) => setDraft((current) => ({ ...current, publicUrl: event.target.value }))} value={draft.publicUrl || ""} />
        </Field>
        <Field label="存储目录" help="为空时使用 data_dir/docker/registry；非空路径必须落在 data_dir/docker 下。">
          <input className="input mono" onChange={(event) => setDraft((current) => ({ ...current, storageDir: event.target.value }))} value={draft.storageDir || ""} />
        </Field>
        <Field label="存储后端" help="object_storage 会把最终 blob/manifest 写入所选对象存储 profile；临时 upload 仍使用受控本地目录。">
          <select className="select mono" onChange={(event) => setDraft((current) => ({ ...current, storageBackend: event.target.value }))} value={draft.storageBackend || "local"}>
            <option value="local">local</option>
            <option value="object_storage">object_storage</option>
          </select>
        </Field>
        {draft.storageBackend === "object_storage" ? (
          <Field label="Object Storage Profile" help="profile 在全局设置 > Object Storage 中维护，secret 不会回显。">
            <select className="select mono" onChange={(event) => setDraft((current) => ({ ...current, objectStorageProfileId: event.target.value }))} value={draft.objectStorageProfileId || ""}>
              <option value="">选择 profile</option>
              {objectProfiles.map((profile) => (
                <option key={profile.id} value={profile.id}>
                  {profile.name} · {profile.bucket}
                </option>
              ))}
            </select>
          </Field>
        ) : null}
        <Field label="Object Prefix" help="默认 phantom-lancer/docker-registry；不能与 Images prefix 混用。">
          <input className="input mono" onChange={(event) => setDraft((current) => ({ ...current, objectPrefix: event.target.value }))} value={draft.objectPrefix || "phantom-lancer/docker-registry"} />
        </Field>
        <div className="grid grid-cols-3 gap-3 max-xl:grid-cols-1">
          <Field label="配额 (GiB)" help={`当前用量 ${formatBytes(registryStatus?.usageBytes || 0)}。`}>
            <input className="input mono" min={1} onChange={(event) => setDraft((current) => ({ ...current, quotaBytes: Math.max(1, Number(event.target.value || 1)) * GiB }))} type="number" value={quotaGiB} />
          </Field>
          <Field label="仓库上限" help="0 表示不限制。">
            <input className="input mono" min={0} onChange={(event) => setDraft((current) => ({ ...current, maxRepositories: Math.max(0, Number(event.target.value || 0)) }))} type="number" value={draft.maxRepositories || 0} />
          </Field>
          <Field label="单仓库 tag 上限" help="0 表示不限制。">
            <input className="input mono" min={0} onChange={(event) => setDraft((current) => ({ ...current, maxTagsPerRepository: Math.max(0, Number(event.target.value || 0)) }))} type="number" value={draft.maxTagsPerRepository || 0} />
          </Field>
        </div>
        <div className="grid grid-cols-3 gap-2 max-xl:grid-cols-1">
          <CheckLabel checked={Boolean(draft.requireTls)} name="docker_registry_require_tls" onChange={(checked) => setDraft((current) => ({ ...current, requireTls: checked }))}>
            要求 HTTPS
          </CheckLabel>
          <CheckLabel checked={Boolean(draft.allowInsecureLocal)} name="docker_registry_allow_insecure_local" onChange={(checked) => setDraft((current) => ({ ...current, allowInsecureLocal: checked }))}>
            允许本机 HTTP
          </CheckLabel>
          <CheckLabel checked={Boolean(draft.allowAnonymousPull)} name="docker_registry_anonymous_pull" onChange={(checked) => setDraft((current) => ({ ...current, allowAnonymousPull: checked }))}>
            允许匿名 pull
          </CheckLabel>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button disabled={!dirty || busy === "registry-settings"} onClick={() => saveRegistrySettings(draft)} tone="primary">
            保存 Registry 设置
          </Button>
          <Button disabled={!dirty || busy === "registry-settings"} onClick={() => setDraft(registrySettings)}>
            重置
          </Button>
          <Button disabled={busy === "registry-gc"} onClick={() => runRegistryGC()} tone="danger">
            执行 Registry GC
          </Button>
        </div>
      </div>
    </Panel>
  );
}
