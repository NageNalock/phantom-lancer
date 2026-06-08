import type { FormEvent, RefObject } from "react";
import type { CodexModel } from "../../../app/types";
import { Button } from "../../../components/ui";

export interface ComposerAttachment {
  id: string;
  filename?: string;
}

export interface ComposerProps {
  prompt: string;
  onPrompt: (value: string) => void;
  promptRef: RefObject<HTMLTextAreaElement | null>;
  sandbox: string;
  onSandbox: (value: string) => void;
  approval: string;
  onApproval: (value: string) => void;
  model: string;
  onModel: (value: string) => void;
  models: CodexModel[];
  skills: string[];
  onInsertSkill: (name: string) => void;
  workspaceWriteAllowed: boolean;
  attachments: ComposerAttachment[];
  onUpload: (file: File) => void;
  onRemoveAttachment: (id: string) => void;
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
  const { prompt, attachments, models, skills, workspaceWriteAllowed, busy, interactive, hasActiveTurn, sending, steering } = props;
  const promptEmpty = !prompt.trim();
  return (
    <form className="grid gap-2" onSubmit={props.onSend}>
      <textarea
        className="input min-h-20 resize-y"
        onChange={(event) => props.onPrompt(event.target.value)}
        placeholder="输入 prompt，Codex 将在受控沙箱内执行；可用 $skill 引用技能"
        ref={props.promptRef}
        value={prompt}
      />
      {attachments.length ? (
        <div className="flex flex-wrap gap-2 text-xs">
          {attachments.map((item) => (
            <span className="flex items-center gap-1.5 rounded border border-[var(--line)] bg-[var(--surface-soft)] px-2 py-1" key={item.id}>
              {item.filename || item.id}
              <button aria-label="移除附件" className="text-[var(--muted-strong)] hover:text-[var(--danger)]" onClick={() => props.onRemoveAttachment(item.id)} type="button">
                ×
              </button>
            </span>
          ))}
        </div>
      ) : null}
      <div className="flex flex-wrap items-center gap-2">
        <select className="select" onChange={(event) => props.onSandbox(event.target.value)} value={props.sandbox}>
          <option value="read-only">只读咨询</option>
          <option disabled={!workspaceWriteAllowed} value="workspace-write">
            工作区写入
          </option>
        </select>
        <select className="select" onChange={(event) => props.onApproval(event.target.value)} value={props.approval}>
          <option value="on-request">on-request</option>
        </select>
        {skills.length ? (
          <select
            aria-label="插入技能"
            className="select"
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
        ) : null}
        {models.length ? (
          <select className="select" onChange={(event) => props.onModel(event.target.value)} value={props.model}>
            <option value="">默认模型</option>
            {models.map((item) => (
              <option key={item.id} value={item.id}>
                {item.displayName || item.id}
                {item.isDefault ? "（默认）" : ""}
              </option>
            ))}
          </select>
        ) : (
          <input className="input w-40" onChange={(event) => props.onModel(event.target.value)} placeholder="模型（运行时探测）" value={props.model} />
        )}
        <label className="button cursor-pointer">
          附件
          <input
            accept="image/png,image/jpeg,image/webp,image/gif"
            className="hidden"
            onChange={(event) => {
              const file = event.target.files?.[0];
              if (file) props.onUpload(file);
              event.target.value = "";
            }}
            type="file"
          />
        </label>
        <div className="ml-auto flex gap-2">
          {interactive && hasActiveTurn ? (
            <>
              <Button disabled={steering || promptEmpty} onClick={() => props.onSteer()}>
                {steering ? "追加中" : "追加输入"}
              </Button>
              <Button tone="danger" onClick={() => props.onInterrupt()}>
                中断
              </Button>
            </>
          ) : null}
          {busy ? (
            <Button disabled={sending || promptEmpty} onClick={() => props.onQueue()}>
              排队
            </Button>
          ) : null}
          <Button disabled={sending || busy || promptEmpty} tone="primary" type="submit">
            {sending ? "发送中" : "发送"}
          </Button>
        </div>
      </div>
    </form>
  );
}
