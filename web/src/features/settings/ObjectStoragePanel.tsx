import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { AppActions } from "../../app/App";
import type { ObjectStorageProfile } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CheckLabel, EmptyState, Field, Panel, Pill, useDangerConfirm } from "../../components/ui";
import { formatDate } from "../../domain/labels";

interface ProfileDraft {
  name: string;
  bucket: string;
  region: string;
  endpoint: string;
  forcePathStyle: boolean;
  accessKeyId: string;
  secretAccessKey: string;
  sessionToken: string;
}

function emptyDraft(): ProfileDraft {
  return { name: "", bucket: "", region: "", endpoint: "", forcePathStyle: false, accessKeyId: "", secretAccessKey: "", sessionToken: "" };
}

function statusTone(status: string): "good" | "warn" | "danger" | "neutral" {
  if (status === "ok") return "good";
  if (status === "error") return "danger";
  if (status === "untested") return "warn";
  return "neutral";
}

// Object Storage Profiles are a global, cross-module capability (Images, Docker
// Registry). Module-level prefix/policy lives in each module's own settings.
export function ObjectStoragePanel({ actions }: { actions: AppActions }) {
  const [items, setItems] = useState<ObjectStorageProfile[]>([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState("");
  const [draft, setDraft] = useState<ProfileDraft>(emptyDraft());
  const [createOpen, setCreateOpen] = useState(false);
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await actions.api<{ items?: ObjectStorageProfile[] }>("/api/object-storage/profiles");
      setItems(response.items || []);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setLoading(false);
    }
  }, [actions]);

  useEffect(() => {
    void load();
  }, [load]);

  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!draft.bucket.trim() || !draft.endpoint.trim()) {
      actions.setToast("请填写 bucket 和 endpoint", "warn");
      return;
    }
    setBusy("create");
    try {
      await actions.api("/api/object-storage/profiles", {
        method: "POST",
        csrf: actions.csrf,
        body: {
          name: draft.name.trim(),
          bucket: draft.bucket.trim(),
          region: draft.region.trim(),
          endpoint: draft.endpoint.trim(),
          forcePathStyle: draft.forcePathStyle,
          accessKeyId: draft.accessKeyId.trim(),
          secretAccessKey: draft.secretAccessKey.trim(),
          sessionToken: draft.sessionToken.trim(),
        },
      });
      setDraft(emptyDraft());
      setCreateOpen(false);
      await load();
      actions.setToast("已创建对象存储 profile", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function test(profile: ObjectStorageProfile) {
    setBusy(`test-${profile.id}`);
    try {
      await actions.api(`/api/object-storage/profiles/${profile.id}/test`, { method: "POST", csrf: actions.csrf });
      await load();
      actions.setToast("连接测试通过", "good");
    } catch (error) {
      await load();
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  async function remove(profile: ObjectStorageProfile) {
    const confirmed = await confirmDanger({
      title: "删除对象存储 profile",
      objectName: profile.name || profile.id,
      body: "该操作会删除全局对象存储连接信息，使用它的模块将无法继续写入或读取新对象。",
      confirmLabel: "删除 profile",
      impact: [
        `Bucket: ${profile.bucket || "-"}`,
        "多媒体、Docker Registry 等引用该 profile 的配置会失去有效凭据。",
        "历史资产如果只存在于该对象存储中，可能无法继续通过控制台读取。",
      ],
      recovery: "删除不可恢复；如需恢复，需要重新创建 profile 并让各模块重新选择。",
    });
    if (!confirmed) return;
    setBusy(`delete-${profile.id}`);
    try {
      await actions.api(`/api/object-storage/profiles/${profile.id}`, { method: "DELETE", csrf: actions.csrf });
      await load();
      actions.setToast("已删除 profile", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  return (
    <>
      <Panel
        title="Object Storage"
        subtitle="S3 兼容对象存储连接，供 Images、Docker Registry 等模块共用。各模块自行决定 prefix 与读写策略。"
        actions={
          <>
            {items.length ? (
              <Button onClick={() => setCreateOpen((open) => !open)}>
                {createOpen ? "收起" : "新建 profile"}
              </Button>
            ) : null}
            <Button onClick={() => void load()}>{loading ? "加载中" : "刷新"}</Button>
          </>
        }
      >
        <div className="grid gap-3">
          <div className="grid content-start gap-2">
            {items.length ? (
              items.map((profile) => (
                <div className="card-soft" key={profile.id}>
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0">
                      <strong className="block truncate text-sm">{profile.name || profile.id}</strong>
                      <span className="mono mt-1 block truncate text-xs text-[var(--muted-strong)]">{profile.endpoint}</span>
                    </div>
                    <Pill tone={statusTone(profile.status)}>{profile.status}</Pill>
                  </div>
                  <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-[var(--muted)]">
                    <span className="mono">bucket {profile.bucket || "-"}</span>
                    <span className="mono">region {profile.region || "auto"}</span>
                    <span>{profile.hasCredentials ? `凭据 ${profile.maskedAccessKeyId}` : "无凭据"}</span>
                    {profile.forcePathStyle ? <span>path-style</span> : null}
                    {profile.lastTestedAt ? <span>测试 {formatDate(profile.lastTestedAt)}</span> : null}
                  </div>
                  {profile.status === "error" && profile.lastError ? <p className="mono mt-2 mb-0 text-xs text-[var(--danger)]">{profile.lastError}</p> : null}
                  <div className="mt-2 flex flex-wrap gap-2">
                    <Button disabled={busy === `test-${profile.id}`} onClick={() => void test(profile)}>
                      {busy === `test-${profile.id}` ? "测试中" : "连接测试"}
                    </Button>
                    <Button disabled={busy === `delete-${profile.id}`} tone="danger" onClick={() => void remove(profile)}>
                      删除
                    </Button>
                  </div>
                </div>
              ))
            ) : (
              <EmptyState body={loading ? "正在加载 profile。" : "尚未配置对象存储 profile，使用下方表单添加。"} title="暂无 profile" />
            )}
          </div>

          {createOpen || !items.length ? (
            <form className="grid gap-3 card-soft" onSubmit={create}>
              <div className="flex items-start justify-between gap-3">
                <div>
                  <strong className="block text-sm">创建 profile</strong>
                  <p className="muted mt-1 mb-0 text-xs">连接信息只作为全局 profile 保存，模块各自配置 prefix 和策略。</p>
                </div>
                {items.length ? (
                  <Button onClick={() => setCreateOpen(false)}>
                    取消
                  </Button>
                ) : null}
              </div>
              <div className="grid grid-cols-3 gap-3 max-lg:grid-cols-1">
                <Field label="名称（可选）">
                  <input autoComplete="off" className="input" name="object_storage_name" onChange={(event) => setDraft((d) => ({ ...d, name: event.target.value }))} placeholder="例如 default object storage" spellCheck={false} value={draft.name} />
                </Field>
                <Field label="Endpoint" help="S3 兼容服务地址，仅保存 scheme 和 host。">
                  <input autoComplete="off" className="input" name="object_storage_endpoint" onChange={(event) => setDraft((d) => ({ ...d, endpoint: event.target.value }))} placeholder="https://s3.example.com" spellCheck={false} type="url" value={draft.endpoint} />
                </Field>
                <Field label="Bucket">
                  <input autoComplete="off" className="input" name="object_storage_bucket" onChange={(event) => setDraft((d) => ({ ...d, bucket: event.target.value }))} placeholder="my-bucket" spellCheck={false} value={draft.bucket} />
                </Field>
              </div>
              <div className="grid grid-cols-4 gap-3 max-xl:grid-cols-2 max-lg:grid-cols-1">
                <Field label="Region">
                  <input autoComplete="off" className="input" name="object_storage_region" onChange={(event) => setDraft((d) => ({ ...d, region: event.target.value }))} placeholder="auto" spellCheck={false} value={draft.region} />
                </Field>
                <Field label="Access Key ID">
                  <input className="input" autoComplete="off" name="object_storage_access_key_id" onChange={(event) => setDraft((d) => ({ ...d, accessKeyId: event.target.value }))} spellCheck={false} value={draft.accessKeyId} />
                </Field>
                <Field label="Secret Access Key">
                  <input className="input" autoComplete="new-password" name="object_storage_secret_access_key" type="password" onChange={(event) => setDraft((d) => ({ ...d, secretAccessKey: event.target.value }))} spellCheck={false} value={draft.secretAccessKey} />
                </Field>
                <Field label="Session Token（可选）">
                  <input className="input" autoComplete="new-password" name="object_storage_session_token" type="password" onChange={(event) => setDraft((d) => ({ ...d, sessionToken: event.target.value }))} spellCheck={false} value={draft.sessionToken} />
                </Field>
              </div>
              <div className="flex flex-wrap items-center justify-between gap-3">
                <CheckLabel
                  checked={draft.forcePathStyle}
                  onChange={(checked) => setDraft((d) => ({ ...d, forcePathStyle: checked }))}
                >
                  使用 path-style 寻址
                </CheckLabel>
                <Button disabled={busy === "create"} tone="primary" type="submit">
                  {busy === "create" ? "创建中" : "创建 profile"}
                </Button>
              </div>
            </form>
          ) : null}
        </div>
      </Panel>
      {dangerConfirmDialog}
    </>
  );
}
