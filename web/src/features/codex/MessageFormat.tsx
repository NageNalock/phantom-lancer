import type { ReactNode } from "react";

type MessageBlock =
  | {
      kind: "text";
      text: string;
    }
  | {
      kind: "code";
      code: string;
      language: string;
      complete: boolean;
    };

const FENCE_RE = /^```([A-Za-z0-9_+.#-]*)\s*$/;

const KEYWORDS = new Set([
  "async",
  "await",
  "break",
  "case",
  "catch",
  "chan",
  "class",
  "const",
  "continue",
  "default",
  "defer",
  "else",
  "export",
  "extends",
  "false",
  "finally",
  "for",
  "from",
  "func",
  "function",
  "go",
  "if",
  "import",
  "interface",
  "let",
  "map",
  "new",
  "nil",
  "null",
  "package",
  "range",
  "return",
  "select",
  "struct",
  "switch",
  "true",
  "try",
  "type",
  "undefined",
  "var",
]);

export function RichMessage({ streaming, text }: { streaming?: boolean; text: string }) {
  const blocks = parseMessageBlocks(text);
  return (
    <div className={`message-rich ${streaming ? "chat-streaming-text" : ""}`}>
      {blocks.map((block, index) =>
        block.kind === "code" ? (
          <CodeBlock block={block} key={`code-${index}`} />
        ) : (
          <p key={`text-${index}`}>{renderInline(block.text, index)}</p>
        ),
      )}
    </div>
  );
}

function CodeBlock({ block }: { block: Extract<MessageBlock, { kind: "code" }> }) {
  const language = normalizeLanguage(block.language);
  return (
    <div className={`message-code-block ${block.complete ? "" : "is-streaming"}`}>
      <div className="message-code-lang">
        <span>{language || "text"}</span>
        {!block.complete ? <span>streaming</span> : null}
      </div>
      <pre>
        <code>{renderHighlightedCode(block.code, language)}</code>
      </pre>
    </div>
  );
}

function parseMessageBlocks(text: string): MessageBlock[] {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const blocks: MessageBlock[] = [];
  let paragraph: string[] = [];
  let code: string[] | null = null;
  let codeLanguage = "";

  const flushParagraph = () => {
    const value = paragraph.join("\n").trimEnd();
    paragraph = [];
    if (value.trim()) blocks.push({ kind: "text", text: value });
  };

  for (const line of lines) {
    const fence = line.match(FENCE_RE);
    if (fence && code === null) {
      flushParagraph();
      code = [];
      codeLanguage = fence[1] || "";
      continue;
    }
    if (fence && code !== null) {
      blocks.push({ kind: "code", code: code.join("\n"), language: codeLanguage, complete: true });
      code = null;
      codeLanguage = "";
      continue;
    }
    if (code !== null) {
      code.push(line);
      continue;
    }
    if (!line.trim()) {
      flushParagraph();
      continue;
    }
    paragraph.push(line);
  }

  flushParagraph();
  if (code !== null) blocks.push({ kind: "code", code: code.join("\n"), language: codeLanguage, complete: false });
  return blocks.length ? blocks : [{ kind: "text", text }];
}

function renderInline(text: string, keyPrefix: number): ReactNode[] {
  const nodes: ReactNode[] = [];
  const token = /(`[^`\n]+`|\[[^\]]+\]\(https?:\/\/[^\s)]+\)|https?:\/\/[^\s<]+)/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = token.exec(text))) {
    if (match.index > lastIndex) nodes.push(text.slice(lastIndex, match.index));
    const value = match[0];
    if (value.startsWith("`") && value.endsWith("`")) {
      nodes.push(<code key={`${keyPrefix}-inline-${match.index}`}>{value.slice(1, -1)}</code>);
    } else if (value.startsWith("[") && value.includes("](")) {
      const close = value.indexOf("](");
      const label = value.slice(1, close);
      const href = value.slice(close + 2, -1);
      nodes.push(
        <a href={href} key={`${keyPrefix}-link-${match.index}`} rel="noreferrer" target="_blank">
          {label}
        </a>,
      );
    } else {
      nodes.push(
        <a href={value} key={`${keyPrefix}-url-${match.index}`} rel="noreferrer" target="_blank">
          {value}
        </a>,
      );
    }
    lastIndex = match.index + value.length;
  }
  if (lastIndex < text.length) nodes.push(text.slice(lastIndex));
  return nodes;
}

function renderHighlightedCode(code: string, language: string): ReactNode[] {
  const lines = code.split("\n");
  return lines.flatMap((line, index) => {
    const nodes = language === "diff" ? renderDiffLine(line, index) : highlightLine(line, index, language);
    return index < lines.length - 1 ? [...nodes, "\n"] : nodes;
  });
}

function renderDiffLine(line: string, index: number): ReactNode[] {
  const className = line.startsWith("+") ? "syntax-add" : line.startsWith("-") ? "syntax-remove" : line.startsWith("@@") ? "syntax-meta" : "";
  return className ? [<span className={className} key={`diff-${index}`}>{line}</span>] : [line];
}

function highlightLine(line: string, lineIndex: number, language: string): ReactNode[] {
  const nodes: ReactNode[] = [];
  const token = /\/\/.*$|#.*$|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|\b[A-Za-z_][A-Za-z0-9_]*\b|\b\d+(?:\.\d+)?\b/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = token.exec(line))) {
    if (match.index > lastIndex) nodes.push(line.slice(lastIndex, match.index));
    const value = match[0];
    const className = syntaxClass(value, language);
    nodes.push(className ? <span className={className} key={`${lineIndex}-${match.index}`}>{value}</span> : value);
    lastIndex = match.index + value.length;
  }
  if (lastIndex < line.length) nodes.push(line.slice(lastIndex));
  return nodes.length ? nodes : [line];
}

function syntaxClass(value: string, language: string): string {
  if (value.startsWith("//") || (value.startsWith("#") && language !== "json")) return "syntax-comment";
  if (value.startsWith("\"") || value.startsWith("'") || value.startsWith("`")) return "syntax-string";
  if (/^\d/.test(value)) return "syntax-number";
  if (KEYWORDS.has(value)) return "syntax-keyword";
  if (language === "json" && (value === "true" || value === "false" || value === "null")) return "syntax-keyword";
  return "";
}

function normalizeLanguage(value: string): string {
  const language = value.trim().toLowerCase();
  if (language === "shell" || language === "sh" || language === "zsh") return "bash";
  if (language === "javascript" || language === "jsx") return "js";
  if (language === "typescript" || language === "tsx") return "ts";
  if (language === "golang") return "go";
  return language;
}
