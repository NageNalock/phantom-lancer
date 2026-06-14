import { useCallback, useRef, useState } from "react";
import type { ClipboardEvent, DragEvent, FormEvent, RefObject } from "react";
import type { CodexAttachment, CodexModel } from "../../../app/types";
import { Button, Field } from "../../../components/ui";
import { formatBytes } from "../../../utils/format";

const MAX_ATTACHMENTS = 4;

export interface ComposerAttachment extends CodexAttachment {
  previewUrl?: string;
  status?: "uploading" | "ready" | "failed";
  error?: string;
}

interface ComposerProps {
  prompt: string;
  onPrompt: (value: string) => void;
  promptRef: RefObject<HTMLTextAreaElement | null>;
  sandbox: string;
  onSandbox: (value: string) => void;
  approval: string;
  onApproval: (value: string) => void;
  model: string;
  onModel: (value: string) => void;
  modelRequired: boolean;
  models: CodexModel[];
  skills: string[];
  onInsertSkill: (name: string) => void;
  workspaceWriteAllowed: boolean;
  // sandboxLocked fixes kind=chat threads to read-only. The selector is shown
  // disabled so the constraint is visible but cannot be changed.
  sandboxLocked: boolean;
  attachments: ComposerAttachment[];
  onUpload: (file: File) => void;
  onRemoveAttachment: (id: string) => void;
  onClearFailedAttachments: () => void;
  busy: boolean;
  interactive: boolean;
  hasActiveTurn: boolean;
  sending: boolean;
  steering: boolean;
  onSend: (event: FormEvent<HTMLFormElement>) => void;
  onQueue: () => void;
  onInterrupt: () => void;
  onSteer: () => void;
}

export function Composer(props: ComposerProps) {
  const { prompt, attachments, modelRequired, models, skills, workspaceWriteAllowed, sandboxLocked, busy, interactive, hasActiveTurn, sending, steering } = props;
  const [dragging, setDragging] = useState(false);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const readyAttachmentCount = attachments.filter((item) => item.status !== "uploading" && item.status !== "failed").length;
  const activeAttachmentCount = attachments.filter((item) => item.status !== "failed").length;
  const failedAttachmentCount = attachments.length - activeAttachmentCount;
  const sendable = Boolean(prompt.trim() || readyAttachmentCount > 0);
  const modelMissing = modelRequired && !props.model.trim();
  const attachmentUploading = attachments.some((item) => item.status === "uploading");
  const remaining = Math.max(0, MAX_ATTACHMENTS - activeAttachmentCount);
  const attachFiles = useCallback((files: FileList | File[]) => {
    const images = Array.from(files).filter((file) => file.type.startsWith("image/")).slice(0, remaining);
    for (const file of images) props.onUpload(file);
  }, [props, remaining]);
  const handlePaste = useCallback((event: ClipboardEvent<HTMLTextAreaElement>) => {
    const byFileList = Array.from(event.clipboardData.files || []).filter((file) => file.type.startsWith("image/"));
    const byItems = Array.from(event.clipboardData.items || [])
      .filter((item) => item.kind === "file" && item.type.startsWith("image/"))
      .map((item) => item.getAsFile())
      .filter((file): file is File => Boolean(file));
    const seen = new Set<string>();
    const files = [...byFileList, ...byItems].filter((file) => {
      const key = `${file.name}:${file.type}:${file.size}:${file.lastModified}`;
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });
    if (!files.length) return;
    event.preventDefault();
    attachFiles(files);
  }, [attachFiles]);
  const handleDrop = useCallback((event: DragEvent<HTMLFormElement>) => {
    event.preventDefault();
    setDragging(false);
    if (event.dataTransfer.files?.length) attachFiles(event.dataTransfer.files);
  }, [attachFiles]);
  return (
    <form
      className={`codex-composer ${dragging ? "codex-composer-dragging" : ""}`}
      onDragEnter={(event) => {
        event.preventDefault();
        setDragging(true);
      }}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false);
      }}
      onDragOver={(event) => event.preventDefault()}
      onDrop={handleDrop}
      onSubmit={props.onSend}
    >
      <textarea
        aria-label="Codex prompt"
        autoComplete="off"
        className="chat-composer-input codex-composer-input"
        name="codex_prompt"
        onChange={(event) => props.onPrompt(event.target.value)}
        onKeyDown={(event) => {
          if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
            event.preventDefault();
            if (interactive && hasActiveTurn) {
              props.onSteer();
            } else {
              event.currentTarget.form?.requestSubmit();
            }
          }
        }}
        onPaste={handlePaste}
        placeholder="输入 prompt，Codex 将在受控沙箱内执行；可用 $skill 引用技能"
        ref={props.promptRef}
        value={prompt}
      />
      {attachments.length ? (
        <div className="grid gap-2">
          {failedAttachmentCount ? (
            <div className="flex items-center justify-between gap-2 rounded-lg border border-[rgba(207,31,50,0.18)] bg-[var(--danger-soft)] px-2 py-1.5 text-xs text-[var(--danger)]">
              <span>{failedAttachmentCount} 张图片上传失败，未计入上限。</span>
              <button className="text-[var(--danger)] underline-offset-2 hover:underline" onClick={props.onClearFailedAttachments} type="button">清除失败项</button>
            </div>
          ) : null}
          <div className="grid grid-cols-2 gap-2 text-xs max-xl:grid-cols-1">
            {attachments.map((item) => (
              <div className="flex min-w-0 items-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-2" key={item.id}>
                {item.previewUrl ? <img alt="" className="h-10 w-10 shrink-0 rounded border border-[var(--line)] object-cover" height={40} src={item.previewUrl} width={40} /> : <span className="h-10 w-10 shrink-0 rounded border border-[var(--line)] bg-[var(--surface-strong)]" />}
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium">{item.filename || item.id}</div>
                  <div className="text-[var(--muted)]">
                    {item.status === "uploading" ? "上传中" : item.status === "failed" ? (item.error || "上传失败") : "已添加"}
                    {item.sizeBytes ? ` · ${formatBytes(item.sizeBytes)}` : ""}
                  </div>
                </div>
                <button aria-label="移除附件" className="shrink-0 text-[var(--muted-strong)] hover:text-[var(--danger)]" onClick={() => props.onRemoveAttachment(item.id)} type="button">
                  移除
                </button>
              </div>
            ))}
          </div>
        </div>
      ) : null}
      <input
        accept="image/png,image/jpeg,image/webp,image/gif"
        className="hidden"
        multiple
        onChange={(event) => {
          if (event.target.files) attachFiles(event.target.files);
          event.currentTarget.value = "";
        }}
        ref={fileInputRef}
        type="file"
      />
      <div className="codex-composer-footer">
        <div className="codex-composer-controls">
        <Field label="沙箱">
          <select className="select codex-composer-select" disabled={sandboxLocked} name="codex_sandbox" onChange={(event) => props.onSandbox(event.target.value)} value={sandboxLocked ? "read-only" : props.sandbox}>
            <option value="read-only">只读咨询</option>
            {sandboxLocked ? null : (
              <option disabled={!workspaceWriteAllowed} value="workspace-write">
                工作区写入
              </option>
            )}
          </select>
        </Field>
        <Field label="审批">
          <select className="select codex-composer-select" name="codex_approval_policy" onChange={(event) => props.onApproval(event.target.value)} value={props.approval}>
            <option value="on-request">on-request</option>
          </select>
        </Field>
        {skills.length ? (
          <Field label="技能">
            <select
              className="select codex-composer-select"
              name="codex_insert_skill"
              onChange={(event) => {
                props.onInsertSkill(event.target.value);
                event.target.value = "";
              }}
              value=""
            >
              <option value="">插入技能…</option>
              {skills.map((name) => (
                <option key={name} value={name}>
                  ${name}
                </option>
              ))}
            </select>
          </Field>
        ) : null}
        {models.length ? (
          <Field label="模型">
            <select className="select codex-composer-model" name="codex_model" onChange={(event) => props.onModel(event.target.value)} value={props.model}>
              {!props.model ? <option disabled value="">选择模型</option> : null}
              {models.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.displayName || item.id}
                  {item.isDefault ? "（默认）" : ""}
                </option>
              ))}
            </select>
          </Field>
        ) : (
          <Field label="模型">
            <input autoComplete="off" className="input codex-composer-model" name="codex_model" onChange={(event) => props.onModel(event.target.value)} placeholder={modelRequired ? "模型（必填）" : "模型（可选）"} spellCheck={false} value={props.model} />
          </Field>
        )}
        <div className="codex-composer-control">
          <span className="codex-composer-label">图片</span>
          <Button className="codex-composer-button" disabled={remaining <= 0} onClick={() => fileInputRef.current?.click()}>
            添加 {activeAttachmentCount}/{MAX_ATTACHMENTS}
          </Button>
        </div>
        {modelMissing ? <span className="codex-composer-warning">请选择一个可用模型</span> : null}
        <span className="codex-composer-help">支持多选、拖拽或粘贴截图</span>
          </div>
        <div className="codex-composer-actions">
          {interactive && hasActiveTurn ? (
            <>
              <Button disabled={steering || !sendable} onClick={() => props.onSteer()}>
                {steering ? "追加中" : "追加输入"}
              </Button>
              <Button tone="danger" onClick={() => props.onInterrupt()}>
                中断
              </Button>
            </>
          ) : null}
          {busy ? (
            <Button disabled={sending || !sendable || modelMissing || attachmentUploading} onClick={() => props.onQueue()}>
              排队
            </Button>
          ) : null}
          <Button disabled={sending || busy || !sendable || modelMissing || attachmentUploading} tone="primary" type="submit">
            {sending ? "发送中" : attachmentUploading ? "等待附件" : "发送"}
          </Button>
        </div>
      </div>
    </form>
  );
}
