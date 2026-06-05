import type { ApiError } from "../app/types";

export interface ApiOptions {
  method?: string;
  body?: unknown | FormData;
  csrf?: string;
}

export function readCookie(name: string): string {
  const match = document.cookie.split("; ").find((part) => part.startsWith(`${name}=`));
  return match ? decodeURIComponent(match.split("=").slice(1).join("=")) : "";
}

export async function api<T>(path: string, options: ApiOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const init: RequestInit = { method: options.method || "GET", headers };

  if (options.body instanceof FormData) {
    init.body = options.body;
  } else if (options.body !== undefined) {
    headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(options.body);
  }

  if (options.method && options.method !== "GET") {
    headers["X-CSRF-Token"] = options.csrf || readCookie("pl_csrf");
  }

  const response = await fetch(path, init);
  const payload = (await response.json().catch(() => ({}))) as {
    error?: { code?: string; message?: string };
  };

  if (!response.ok) {
    const error = new Error(payload.error?.message || `请求失败：${response.status}`) as ApiError;
    error.code = payload.error?.code || "";
    error.status = response.status;
    error.payload = payload;
    throw error;
  }

  return payload as T;
}

export function friendlyError(error: unknown): string {
  const err = error as ApiError;
  if (err.code === "path_out_of_boundary") {
    return `该路径不在允许根目录内。请在设置页更新允许根目录后重试。后端返回：${err.message}`;
  }
  if (err.code === "invalid_allowed_roots") return `允许根目录无效：${err.message}`;
  if (err.code === "git_required") return "该目录不是 Git 仓库。如确认要添加，请勾选允许非 Git 目录。";
  if (err.code === "permission_denied") return "当前项目未允许 Codex 写入。请改用只读沙箱，或在项目配置中允许写入。";
  if (err.code === "codex_app_server_failed") return `Codex app-server 请求失败：${err.message}`;
  if (err.code === "api_key_missing") return "Images 模块尚未配置 xAI API Key。";
  if (err.code === "provider_failed") return `图片模型调用失败：${err.message}`;
  if (err.code === "images_settings_invalid") return `Images 设置无效：${err.message}`;
  if (err.code?.startsWith("image_") || err.code?.startsWith("prompt_") || err.code === "source_count_invalid" || err.code === "mode_invalid") return `图片生成参数无效：${err.message}`;
  if (err.code === "v2ray_config_invalid") return `V2Ray 配置无效：${err.message}`;
  if (err.code === "v2ray_control_failed") return `V2Ray 服务控制失败：${err.message}`;
  if (err.code === "gateway_disabled") return "Codex Gateway 尚未启用。";
  if (err.code === "no_available_accounts") return "没有可用的 Codex Gateway 账号。";
  if (err.code === "model_not_supported") return "当前账号 plan 不支持该模型。";
  if (err.code === "oauth_settings_invalid") return `OAuth 设置无效：${err.message}`;
  if (err.code === "model_refresh_failed") return `模型刷新失败：${err.message}`;
  return err.message || "请求失败";
}
