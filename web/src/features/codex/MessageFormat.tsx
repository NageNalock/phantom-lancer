import { useState } from "react";
import ReactMarkdown from "react-markdown";
import type { Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import css from "highlight.js/lib/languages/css";
import diff from "highlight.js/lib/languages/diff";
import go from "highlight.js/lib/languages/go";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import markdown from "highlight.js/lib/languages/markdown";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";

const FENCE_RE = /^(```+|~~~+)([A-Za-z0-9_+.#-]*)[ \t]*$/;
const CLOSING_FENCE_RE = /^(```+|~~~+)[ \t]*$/;
const MARKDOWN_LANGUAGES = new Set(["md", "markdown", "mdx"]);

registerLanguage("bash", bash);
registerLanguage("css", css);
registerLanguage("diff", diff);
registerLanguage("go", go);
registerLanguage("javascript", javascript);
registerLanguage("json", json);
registerLanguage("markdown", markdown);
registerLanguage("typescript", typescript);
registerLanguage("xml", xml);
registerLanguage("yaml", yaml);

const markdownComponents: Components = {
  a({ children, href, node: _node, ...props }) {
    const safeHref = safeLinkHref(href);
    if (!safeHref) return <>{children}</>;
    return (
      <a {...props} href={safeHref} rel="noreferrer" target="_blank">
        {children}
      </a>
    );
  },
  code({ children, className, node: _node, ...props }) {
    const source = String(children);
    const rawCode = source.replace(/\n$/, "");
    const language = normalizeLanguage(languageFromClassName(className));
    const isBlock = Boolean(language || className?.includes("language-") || source.includes("\n"));
    if (!isBlock) {
      return (
        <code {...props} className={className}>
          {children}
        </code>
      );
    }
    return <CodeBlock code={rawCode} language={language || "text"} />;
  },
  pre({ children }) {
    return <>{children}</>;
  },
};

export function RichMessage({ streaming, text }: { streaming?: boolean; text: string }) {
  return (
    <div className={`message-rich ${streaming ? "chat-streaming-text" : ""}`}>
      <ReactMarkdown components={markdownComponents} remarkPlugins={[remarkGfm]} skipHtml>
        {preprocessMarkdown(text)}
      </ReactMarkdown>
    </div>
  );
}

function CodeBlock({ code, language }: { code: string; language: string }) {
  const [copied, setCopied] = useState(false);
  async function copyCode() {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1200);
    } catch {
      setCopied(false);
    }
  }
  return (
    <div className="message-code-block">
      <div className="message-code-lang">
        <span>{language || "text"}</span>
        <button className="message-code-copy" onClick={() => void copyCode()} type="button">
          {copied ? "已复制" : "复制"}
        </button>
      </div>
      <pre>
        <code dangerouslySetInnerHTML={{ __html: highlightedCode(code, language) }} />
      </pre>
    </div>
  );
}

function preprocessMarkdown(text: string): string {
  return unwrapMarkdownFences(normalizeMessageFences(text));
}

function normalizeMessageFences(text: string): string {
  let value = text.replace(/\r\n/g, "\n");
  if (!value.includes("```")) return value;
  value = value.replace(/([^\n])```/g, "$1\n```");
  value = value.replace(/```([A-Za-z0-9_+.#-]*)[ \t]+/g, "```$1\n");
  value = value.replace(/[ \t]+```/g, "\n```");
  return value;
}

function unwrapMarkdownFences(text: string): string {
  const lines = text.split("\n");
  const firstContentIndex = lines.findIndex((line) => line.trim().length > 0);
  if (firstContentIndex < 0) return text;

  const openingFence = lines[firstContentIndex].match(FENCE_RE);
  if (!openingFence || !MARKDOWN_LANGUAGES.has(normalizeLanguage(openingFence[2] || ""))) return text;

  let lastContentIndex = -1;
  for (let index = lines.length - 1; index > firstContentIndex; index -= 1) {
    if (lines[index].trim().length > 0) {
      lastContentIndex = index;
      break;
    }
  }

  const closingFence = lastContentIndex >= 0 ? lines[lastContentIndex].match(CLOSING_FENCE_RE) : null;
  const hasClosingFence = Boolean(closingFence && closingFence[1][0] === openingFence[1][0] && closingFence[1].length >= openingFence[1].length);
  const bodyEnd = hasClosingFence ? lastContentIndex : lines.length;
  const before = lines.slice(0, firstContentIndex);
  const body = lines.slice(firstContentIndex + 1, bodyEnd);
  const after = hasClosingFence ? lines.slice(lastContentIndex + 1) : [];
  return [...before, ...body, ...after].join("\n");
}

function highlightedCode(code: string, language: string): string {
  const highlighterLanguage = highlightLanguage(language);
  if (!highlighterLanguage) return escapeHtml(code);
  try {
    return hljs.highlight(code, { language: highlighterLanguage, ignoreIllegals: true }).value;
  } catch {
    return escapeHtml(code);
  }
}

function languageFromClassName(className?: string): string {
  return className?.match(/language-([A-Za-z0-9_+.#-]+)/)?.[1] || "";
}

function normalizeLanguage(value: string): string {
  const language = value.trim().toLowerCase();
  if (language === "shell" || language === "sh" || language === "zsh") return "bash";
  if (language === "javascript" || language === "jsx" || language === "mjs" || language === "cjs") return "javascript";
  if (language === "typescript" || language === "tsx" || language === "ts") return "typescript";
  if (language === "golang") return "go";
  if (language === "html" || language === "svg") return "xml";
  if (language === "yml") return "yaml";
  if (language === "md") return "markdown";
  return language;
}

function highlightLanguage(language: string): string {
  if (hljs.getLanguage(language)) return language;
  return "";
}

function safeLinkHref(href?: string): string {
  const value = href?.trim() || "";
  return /^(https?:|mailto:)/i.test(value) ? value : "";
}

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function registerLanguage(name: string, language: Parameters<typeof hljs.registerLanguage>[1]) {
  if (!hljs.getLanguage(name)) hljs.registerLanguage(name, language);
}
