import { FormEvent, useState } from "react";
import { friendlyError } from "../api/client";
import { Button } from "../components/ui";

export function AuthView({ mode, onSubmit }: { mode: "bootstrap" | "login"; onSubmit: (mode: "bootstrap" | "login", username: string, password: string) => Promise<void> }) {
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const isBootstrap = mode === "bootstrap";

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    setSubmitting(true);
    setError("");
    try {
      await onSubmit(mode, String(data.get("username") || ""), String(data.get("password") || ""));
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="grid min-h-dvh place-items-center p-6">
      <section className="w-full max-w-[430px] rounded-xl border border-[var(--line)] bg-[var(--surface)] p-7 shadow-[var(--shadow)]" aria-labelledby="authTitle">
        <div className="mb-5 grid h-10 w-10 place-items-center rounded-lg border border-[var(--line-strong)] bg-[var(--surface)] font-mono text-xs font-bold text-[var(--accent)]">PL</div>
        <h1 className="mb-2 text-2xl font-semibold leading-tight" id="authTitle">
          {isBootstrap ? "初始化管理员" : "控制台登录"}
        </h1>
        <p className="muted mb-0 text-sm">{isBootstrap ? "设置本机控制台的首个管理员账号。" : "进入项目、Codex 会话和审计工作区。"}</p>
        <form className="mt-5 grid gap-4" onSubmit={handleSubmit}>
          {error ? <div className="rounded-lg border border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] p-3 text-sm text-[var(--danger)]">{error}</div> : null}
          <label className="field">
            <span>用户名</span>
            <input className="input" name="username" autoComplete="username" required />
          </label>
          <label className="field">
            <span>密码</span>
            <input className="input" name="password" type="password" autoComplete={isBootstrap ? "new-password" : "current-password"} required />
          </label>
          <div className="flex">
            <Button disabled={submitting} tone="primary" type="submit">
              {submitting ? "处理中" : isBootstrap ? "创建管理员" : "登录"}
            </Button>
          </div>
        </form>
      </section>
    </main>
  );
}
