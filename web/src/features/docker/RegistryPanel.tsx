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
  clearNewCredentialSecret,
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
  registryView,
  repoTags,
  repositories,
  rotateCredential,
  runRegistryGC,
  saveRegistrySettings,
  selectedRepo,
  selectedTag,
  setCredentialName,
  setCredentialPrefix,
  setCredentialStatus,
  setRegistryView,
  setSelectedTag,
  useTagForContainer,
}: {
  busy: string;
  createCredential: (scopes: string[]) => void;
  credentialName: string;
  credentialPrefix: string;
  credentials: DockerRegistryCredential[];
  clearNewCredentialSecret: () => void;
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
  registryView: RegistryView;
  repoTags: DockerRegistryTag[];
  repositories: DockerRegistryRepository[];
  rotateCredential: (item: DockerRegistryCredential) => void;
  runRegistryGC: () => void;
  saveRegistrySettings: (settings: DockerRegistrySettings) => void;
  selectedRepo: string;
  selectedTag: string;
  setCredentialName: (value: string) => void;
  setCredentialPrefix: (value: string) => void;
  setCredentialStatus: (item: DockerRegistryCredential, status: "active" | "disabled") => void;
  setRegistryView: (view: RegistryView) => void;
  setSelectedTag: (tag: string) => void;
  useTagForContainer: (item: DockerRegistryTag) => void;
}) {
  const [credentialScopes, setCredentialScopes] = useState<string[]>(["registry.pull", "registry.push"]);
  const [copiedSecret, setCopiedSecret] = useState(false);
  const host = registryHost(registrySettings.publicUrl || registryStatus?.publicUrl);
  const exampleRepository = repositories[0]?.name || "project/app";
  const [expandedDigest, setExpandedDigest] = useState<string>("");

  const selectedTagDetails = useMemo(() => {
    if (!selectedTag || !selectedRepo) return null;
    const [repoName, tagName] = [selectedTag.split(":")[0], selectedTag.split(":").slice(1).join(":")];
    if (repoName !== selectedRepo) return null;
    return repoTags.find((t) => t.tag === tagName) || null;
  }, [selectedTag, selectedRepo, repoTags]);

  function toggleScope(scope: string, checked: boolean) {
    setCredentialScopes((current) => {
      const next = checked ? [...current, scope] : current.filter((item) => item !== scope);
      return next.length ? Array.from(new Set(next)) : current;
    });
  }

  async function copySecret() {
    if (!newCredentialSecret) return;
    try {
      await navigator.clipboard?.writeText(newCredentialSecret);
      setCopiedSecret(true);
      setTimeout(() => setCopiedSecret(false), 2000);
    } catch {
      // ignore
    }
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
        <div className="rounded-lg border-2 border-[var(--warn)] bg-[var(--warn-soft)] p-4">
          <div className="mb-3 flex items-start justify-between gap-3">
            <div>
              <strong className="block text-sm">新 secret 只显示一次</strong>
              <p className="muted mt-1 mb-0 text-xs">请立即复制并保存到安全位置。关闭或切页后将无法再次查看。</p>
            </div>
            <Pill tone="warn">一次性</Pill>
          </div>
          <div className="flex items-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3">
            <code className="mono min-w-0 flex-1 break-all text-xs">
              {newCredentialSecret}
            </code>
            <CommandCopyButton command={newCredentialSecret} />
          </div>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button tone="primary" onClick={() => void copySecret()}>
              {copiedSecret ? "已复制到剪贴板" : "复制 Secret"}
            </Button>
            <Button onClick={clearNewCredentialSecret}>
              我已保存，关闭
            </Button>
          </div>
        </div>
      ) : null}

      <SubTabs activeId={registryView} ariaLabel="Registry 视图" onChange={(id) => setRegistryView(id as RegistryView)} tabs={REGISTRY_VIEWS} />

      {registryView === "repositories" ? (
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
                  <Button onClick={() => openRepository(item.name)}>{selectedRepo === item.name ? "已选中" : "查看并拉取"}</Button>,
                ],
              }))}
            />
          </Panel>

          {selectedRepo ? (
            <div className="grid gap-4">
              <Panel title={`Tags · ${selectedRepo}`} subtitle="拉取后会切到本机镜像；删除 tag 不会立即释放 blob，需执行 GC。">
                <DockerTable
                  columns={[
                    { header: "Tag", width: "16%" },
                    { header: "Digest", width: "34%" },
                    { header: "大小", width: "100px" },
                    { header: "推送时间", width: "170px" },
                    { header: "操作" },
                  ]}
                  empty="暂无 tag"
                  loading={loading}
                  rows={repoTags.map((item) => {
                    const tagKey = `${item.repository}:${item.tag}`;
                    const isSelected = selectedTag === tagKey;
                    return {
                      key: tagKey,
                      cells: [
                        <span className="flex items-center gap-1.5">
                          {isSelected ? <Pill tone="good">当前</Pill> : null}
                          <DockerValue value={item.tag} />
                        </span>,
                        <div className="grid gap-1">
                          <div className="flex items-center gap-1">
                            <DockerValue
                              copyValue={item.digest}
                              value={
                                expandedDigest === tagKey
                                  ? (item.digest || "")
                                  : ((item.digest?.length ?? 0) > 24
                                      ? item.digest?.slice(0, 10) + "…" + item.digest?.slice(-8)
                                      : (item.digest || "-"))
                              }
                            />
                            {(item.digest?.length ?? 0) > 24 ? (
                              <button
                                className="shrink-0 rounded border border-[var(--line)] px-1.5 py-0.5 text-[10px] text-[var(--muted-strong)] hover:bg-[var(--surface-strong)]"
                                onClick={() => setExpandedDigest(expandedDigest === tagKey ? "" : tagKey)}
                                type="button"
                              >
                                {expandedDigest === tagKey ? "收起" : "展开"}
                              </button>
                            ) : null}
                          </div>
                        </div>,
                        <span className="text-xs">{formatBytes(item.manifest?.sizeBytes || 0)}</span>,
                        <span className="text-xs">{item.manifest?.pushedAt || item.updatedAt || "-"}</span>,
                        <span className="flex flex-wrap gap-1">
                          <Button disabled={busy === registryTagPullBusyKey(item)} onClick={() => pullRegistryTag(item)}>
                            拉取到本机
                          </Button>
                          <Button onClick={() => { setSelectedTag(tagKey); useTagForContainer(item); }}>用于创建</Button>
                          <Button disabled={busy === `tag-delete-${item.repository}-${item.tag}`} onClick={() => deleteTag(item)} tone="danger">
                            删除
                          </Button>
                        </span>,
                      ],
                    };
                  })}
                />
              </Panel>

              {selectedTagDetails ? (
                <Panel title={`Tag 详情 · ${selectedTagDetails.tag}`} subtitle="完整 digest 与推送信息；可直接从这里进入容器创建。" actions={<Button onClick={() => setSelectedTag("")}>关闭</Button>}>
                  <div className="grid max-w-3xl gap-3">
                    <div className="grid grid-cols-2 gap-3">
                      <Field label="Repository"><DockerValue value={selectedTagDetails.repository} /></Field>
                      <Field label="Tag"><DockerValue value={selectedTagDetails.tag} /></Field>
                    </div>
                    <Field label="Digest">
                      <div className="flex items-center gap-2">
                        <DockerValue clamp={false} copyValue={selectedTagDetails.digest} value={selectedTagDetails.digest || "-"} />
                      </div>
                    </Field>
                    <div className="grid grid-cols-2 gap-3">
                      <Field label="大小"><span className="text-xs">{formatBytes(selectedTagDetails.manifest?.sizeBytes || 0)}</span></Field>
                      <Field label="推送时间"><span className="text-xs">{selectedTagDetails.manifest?.pushedAt || selectedTagDetails.updatedAt || "-"}</span></Field>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      <Button onClick={() => useTagForContainer(selectedTagDetails)} tone="primary">用此镜像创建容器</Button>
                      <Button disabled={busy === registryTagPullBusyKey(selectedTagDetails)} onClick={() => pullRegistryTag(selectedTagDetails)}>拉取到本机</Button>
                    </div>
                  </div>
                </Panel>
              ) : null}
            </div>
          ) : null}
        </div>
      ) : registryView === "credentials" ? (
        <div className="grid gap-4">
          <Panel title="凭据" subtitle="secret 只在创建或轮换后显示一次；停用会立即阻止该凭据继续访问 Registry。">
            <div className="grid gap-3">
              <div className="grid grid-cols-2 gap-3 max-lg:grid-cols-1">
                <Field label="凭据名称">
                  <input className="input mono" onChange={(event) => setCredentialName(event.target.value)} value={credentialName} />
                </Field>
                <Field label="仓库前缀" help="例如 personal/ 或 team-a/；留空时后端会回退到 personal/。">
                  <input className="input mono" onChange={(event) => setCredentialPrefix(event.target.value)} value={credentialPrefix} />
                </Field>
              </div>
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
              <div>
                <Button disabled={busy === "credential-create"} onClick={() => createCredential(credentialScopes)} tone="primary">
                  创建凭据
                </Button>
              </div>
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

          <Panel title="推送与拉取指令" subtitle="三条完整命令：登录、打 tag、推送。拉取命令同样适用于其他节点。">
            <div className="grid gap-3">
              {[
                { label: "登录 Registry", cmd: `docker login ${host}`, help: "交互输入用户名和刚创建的凭据密码。" },
                { label: "打 Tag", cmd: `docker tag my-app:latest ${host}/${exampleRepository}:latest`, help: "把本地镜像打上当前 Registry 路径。" },
                { label: "推送到 Registry", cmd: `docker push ${host}/${exampleRepository}:latest`, help: "上传到当前 Registry 的仓库路径下。" },
                { label: "从 Registry 拉取", cmd: `docker pull ${host}/${exampleRepository}:latest`, help: "在其他节点或本机拉取。" },
              ].map((row) => (
                <div className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={row.label}>
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <strong className="text-xs">{row.label}</strong>
                      <p className="muted mt-0.5 mb-0 text-xs">{row.help}</p>
                    </div>
                    <CommandCopyButton command={row.cmd} />
                  </div>
                  <code className="mono break-all rounded-md border border-[var(--line)] bg-[var(--surface)] p-2 text-xs">
                    {row.cmd}
                  </code>
                </div>
              ))}
            </div>
          </Panel>
        </div>
      ) : (
        <RegistrySettingsPanel
          busy={busy}
          credentials={credentials}
          formatBytes={formatBytes}
          objectProfiles={objectProfiles}
          registrySettings={registrySettings}
          registryStatus={registryStatus}
          repositories={repositories}
          runRegistryGC={runRegistryGC}
          saveRegistrySettings={saveRegistrySettings}
        />
      )}
    </div>
  );
}

function RegistrySettingsPanel({
  busy,
  credentials,
  formatBytes,
  objectProfiles,
  registrySettings,
  registryStatus,
  repositories,
  runRegistryGC,
  saveRegistrySettings,
}: {
  busy: string;
  credentials: DockerRegistryCredential[];
  formatBytes: (bytes: number) => string;
  objectProfiles: ObjectStorageProfile[];
  registrySettings: DockerRegistrySettings;
  registryStatus: DockerRegistryStatus | null;
  repositories: DockerRegistryRepository[];
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
    <>
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
        </div>
      </div>
    </Panel>

    <Panel
      title="维护与危险操作"
      subtitle="Registry GC、blob 回收和清理动作。这些操作有数据风险，请确认没有并发 push 依赖待回收对象。"
    >
      <div className="grid max-w-3xl gap-4">
        <div className="grid grid-cols-3 gap-3 max-lg:grid-cols-1">
          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <strong className="block text-xs">当前用量</strong>
            <p className="mono mt-1 mb-0 text-sm">{formatBytes(registryStatus?.usageBytes || 0)}</p>
            <p className="muted mt-0.5 mb-0 text-xs">Quota: {formatBytes(registryStatus?.quotaBytes || registrySettings.quotaBytes || 0)}</p>
          </div>
          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <strong className="block text-xs">仓库 / Tag</strong>
            <p className="mono mt-1 mb-0 text-sm">{registryStatus?.repositoryCount || repositories.length || 0} 仓库</p>
            <p className="muted mt-0.5 mb-0 text-xs">{registryStatus?.credentialCount || credentials.length || 0} 个凭据</p>
          </div>
          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <strong className="block text-xs">最近 GC</strong>
            <p className="mono mt-1 mb-0 text-sm">{"-"}</p>
            <p className="muted mt-0.5 mb-0 text-xs">Job 事件可在 Events 页回看</p>
          </div>
        </div>

        <div className="rounded-lg border-2 border-[rgba(207,31,50,0.2)] bg-[var(--danger-soft)] p-3">
          <div className="mb-3 flex items-start justify-between gap-3">
            <div>
              <strong className="block text-sm">Registry Garbage Collection</strong>
              <p className="muted mt-1 mb-0 text-xs">
                清理过期 upload 临时文件，并回收未被任何 manifest 引用的 layer/config blob。运行期间读写可能短暂受影响。
              </p>
            </div>
            <Pill tone="danger">高风险</Pill>
          </div>
          <ul className="muted mb-3 pl-5 text-xs leading-relaxed">
            <li>只清理未被 manifest 引用的 blob；正在 push 的 manifest 不会被触及。</li>
            <li>Blob 回收不可恢复；删除 tag 后通常需要 GC 才能释放底层存储。</li>
            <li>GC 任务会进入 Docker Job 事件流，可在 Events / Jobs 页观察进度。</li>
          </ul>
          <Button disabled={busy === "registry-gc"} tone="danger" onClick={() => runRegistryGC()}>
            {busy === "registry-gc" ? "GC 提交中" : "执行 Registry GC"}
          </Button>
        </div>
      </div>
    </Panel>
    </>
  );
}

function CommandCopyButton({ command }: { command: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard?.writeText(command);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      // ignore
    }
  }
  return (
    <button
      className={`shrink-0 rounded-md border px-2.5 py-1 text-[11px] transition ${
        copied
          ? "border-[rgba(18,132,79,0.3)] bg-[var(--good-soft)] text-[var(--good)]"
          : "border-[var(--line)] bg-[var(--surface)] hover:bg-[var(--surface-strong)]"
      }`}
      onClick={() => void copy()}
      type="button"
    >
      {copied ? "已复制" : "复制"}
    </button>
  );
}
