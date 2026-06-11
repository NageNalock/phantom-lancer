import type {
  DockerRegistryCredential,
  DockerRegistryRepository,
  DockerRegistrySettings,
  DockerRegistryStatus,
  DockerRegistryTag,
  ObjectStorageProfile,
} from "../../app/types";
import { Button, CheckLabel, ContextList, Field, Metric, Panel, Pill, Toggle } from "../../components/ui";
import { DockerTable } from "./DockerTable";

// registryHost derives a valid image-reference host (host[:port][/path]) from
// the configured Public URL by stripping the scheme and any trailing slash. A
// full URL like https://registry.example.com is not a legal image name, so
// docker login/tag/push must use the bare host.
function registryHost(publicUrl: string | undefined): string {
  const raw = (publicUrl || "").trim();
  if (!raw) {
    return "registry.example.com";
  }
  return raw.replace(/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//, "").replace(/\/+$/, "") || "registry.example.com";
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
  setRegistrySettings,
}: {
  busy: string;
  createCredential: () => void;
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
  setRegistrySettings: (updater: (current: DockerRegistrySettings) => DockerRegistrySettings) => void;
}) {
  return (
    <div className="grid gap-4">
      <div className="grid grid-cols-4 gap-2 max-lg:grid-cols-2">
        <Metric
          label="Registry"
          tone={registryStatus?.enabled ? (registryStatus.ready ? "good" : "warn") : "neutral"}
          value={registryStatus?.enabled ? (registryStatus.ready ? "Ready" : "Enabled") : "Disabled"}
        />
        <Metric
          label="Public URL"
          value={<span className="mono text-xs">{registryStatus?.publicUrl || "未配置"}</span>}
        />
        <Metric
          label="Storage"
          value={registryStatus?.storageBackend || "local"}
        />
        <Metric
          label="Usage"
          value={formatBytes(registryStatus?.usageBytes || 0)}
          detail={formatBytes(registryStatus?.quotaBytes || 0) + " quota"}
        />
      </div>
      {newCredentialSecret ? (
        <div className="rounded-lg border border-[var(--warn)] bg-[var(--warn-soft)] p-3">
          <strong className="block text-sm">新 secret 只显示一次</strong>
          <code className="mono mt-2 block break-all text-xs">{newCredentialSecret}</code>
        </div>
      ) : null}
      <div className="grid grid-cols-[minmax(0,1fr)_320px] gap-4 max-xl:grid-cols-1">
        <DockerTable
          empty="暂无 Registry 仓库"
          loading={loading}
          headers={["Repository", "Tags", "Size", "Last pushed", "操作"]}
          rows={repositories.map((item) => ({
            key: item.name,
            cells: [
              <span className="mono text-xs">{item.name}</span>,
              String(item.tagCount || 0),
              formatBytes(item.sizeBytes || 0),
              item.lastPushedAt || "-",
              <Button onClick={() => openRepository(item.name)}>查看 tags</Button>,
            ],
          }))}
        />
        <div className="grid gap-3">
          <Panel title="Push Instructions" subtitle="使用 Registry 凭据登录后推送镜像。">
            <pre className="code-block">{`docker login ${registryHost(registrySettings.publicUrl)}
docker tag my-app:latest ${registryHost(registrySettings.publicUrl)}/personal/my-app:latest
docker push ${registryHost(registrySettings.publicUrl)}/personal/my-app:latest`}</pre>
          </Panel>
          <Panel title="Credentials" subtitle="secret 只在创建或轮换后显示一次。">
            <div className="grid gap-2">
              <Field label="凭据名称">
                <input className="input mono" onChange={(event) => setCredentialName(event.target.value)} value={credentialName} />
              </Field>
              <Field label="命名空间前缀" help="例如 personal/ 或 team-a/；留空则无命名空间限制。">
                <input className="input mono" onChange={(event) => setCredentialPrefix(event.target.value)} value={credentialPrefix} />
              </Field>
              <Button disabled={busy === "credential-create"} onClick={() => createCredential()} tone="primary">创建凭据</Button>
              {credentials.map((item) => (
                <div className="flex items-center justify-between gap-2 border-t border-[var(--line)] pt-2" key={item.id}>
                  <span className="mono text-xs">{item.name}</span>
                  <span className="flex gap-1">
                    <Button disabled={busy === `cred-rotate-${item.id}`} onClick={() => rotateCredential(item)}>轮换</Button>
                    <Button disabled={busy === `cred-delete-${item.id}`} onClick={() => deleteCredential(item)} tone="danger">删除</Button>
                  </span>
                </div>
              ))}
            </div>
          </Panel>
        </div>
      </div>
      {selectedRepo ? (
        <Panel title={`Tags · ${selectedRepo}`} subtitle="删除 tag 不会立即释放 blob，需执行 GC。">
          <DockerTable
            empty="暂无 tag"
            loading={loading}
            headers={["Tag", "Digest", "Size", "Pushed", "操作"]}
            rows={repoTags.map((item) => ({
              key: `${item.repository}:${item.tag}`,
              cells: [
                <span className="mono text-xs">{item.tag}</span>,
                <span className="mono text-xs">{item.digest}</span>,
                formatBytes(item.manifest?.sizeBytes || 0),
                item.manifest?.pushedAt || item.updatedAt || "-",
                <Button disabled={busy === `tag-delete-${item.repository}-${item.tag}`} onClick={() => deleteTag(item)} tone="danger">删除 tag</Button>,
              ],
            }))}
          />
        </Panel>
      ) : null}
      <RegistrySettingsPanel
        busy={busy}
        objectProfiles={objectProfiles}
        registrySettings={registrySettings}
        runRegistryGC={runRegistryGC}
        saveRegistrySettings={saveRegistrySettings}
        setRegistrySettings={setRegistrySettings}
      />
    </div>
  );
}

function RegistrySettingsPanel({
  busy,
  objectProfiles,
  registrySettings,
  runRegistryGC,
  saveRegistrySettings,
  setRegistrySettings,
}: {
  busy: string;
  objectProfiles: ObjectStorageProfile[];
  registrySettings: DockerRegistrySettings;
  runRegistryGC: () => void;
  saveRegistrySettings: (settings: DockerRegistrySettings) => void;
  setRegistrySettings: (updater: (current: DockerRegistrySettings) => DockerRegistrySettings) => void;
}) {
  return (
    <Panel title="Registry Settings" subtitle="Registry 存储、TLS 与匿名 pull 策略。">
      <div className="grid max-w-3xl gap-3">
        <Toggle
          checked={Boolean(registrySettings.enabled)}
          label="启用内嵌 Registry"
          onChange={(checked) => setRegistrySettings((current) => ({ ...current, enabled: checked }))}
        />
        <Field label="Public URL" help="示例：https://registry.example.com；不能包含 token/query。">
          <input className="input mono" onChange={(event) => setRegistrySettings((current) => ({ ...current, publicUrl: event.target.value }))} value={registrySettings.publicUrl || ""} />
        </Field>
        <Field label="Storage Dir" help="为空时使用 data_dir/docker/registry；非空路径必须落在 data_dir/docker 下。">
          <input className="input mono" onChange={(event) => setRegistrySettings((current) => ({ ...current, storageDir: event.target.value }))} value={registrySettings.storageDir || ""} />
        </Field>
        <Field label="Storage Backend" help="object_storage 会把最终 blob/manifest 写入所选对象存储 profile；临时 upload 仍使用受控本地目录。">
          <select className="select mono" onChange={(event) => setRegistrySettings((current) => ({ ...current, storageBackend: event.target.value }))} value={registrySettings.storageBackend || "local"}>
            <option value="local">local</option>
            <option value="object_storage">object_storage</option>
          </select>
        </Field>
        {registrySettings.storageBackend === "object_storage" ? (
          <Field label="Object Storage Profile" help="profile 在全局设置 > Object Storage 中维护，secret 不会回显。">
            <select className="select mono" onChange={(event) => setRegistrySettings((current) => ({ ...current, objectStorageProfileId: event.target.value }))} value={registrySettings.objectStorageProfileId || ""}>
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
          <input className="input mono" onChange={(event) => setRegistrySettings((current) => ({ ...current, objectPrefix: event.target.value }))} value={registrySettings.objectPrefix || "phantom-lancer/docker-registry"} />
        </Field>
        <div className="flex flex-wrap gap-3">
          <CheckLabel
            checked={Boolean(registrySettings.requireTls)}
            onChange={(checked) => setRegistrySettings((current) => ({ ...current, requireTls: checked }))}
          >
            Require TLS
          </CheckLabel>
          <CheckLabel
            checked={Boolean(registrySettings.allowAnonymousPull)}
            onChange={(checked) => setRegistrySettings((current) => ({ ...current, allowAnonymousPull: checked }))}
          >
            Anonymous pull
          </CheckLabel>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button disabled={busy === "registry-settings"} onClick={() => saveRegistrySettings(registrySettings)} tone="primary">保存 Registry 设置</Button>
          <Button disabled={busy === "registry-gc"} onClick={() => runRegistryGC()} tone="danger">执行 Registry GC</Button>
        </div>
      </div>
    </Panel>
  );
}
