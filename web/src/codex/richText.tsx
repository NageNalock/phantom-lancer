import type { ReactNode } from "react";

type RichSegment =
  | { type: "paragraph"; lines: InlineSegment[][] }
  | { type: "code"; language?: string; text: string };

type InlineSegment =
  | { type: "text"; text: string }
  | { type: "code"; text: string }
  | { type: "link"; text: string; href: string };

export function RichText({ text }: { text: string }) {
  const segments = parseRichText(text);
  if (!segments.length) return <span>&nbsp;</span>;

  return (
    <div className="message-rich">
      {segments.map((segment, index) => {
        if (segment.type === "code") {
          return (
            <div className="message-code-block" key={index}>
              {segment.language ? <div className="message-code-lang">{segment.language}</div> : null}
              <pre>
                <code>{segment.text}</code>
              </pre>
            </div>
          );
        }
        return (
          <p key={index}>
            {segment.lines.map((line, lineIndex) => (
              <span key={lineIndex}>
                {lineIndex > 0 ? <br /> : null}
                {renderInline(line)}
              </span>
            ))}
          </p>
        );
      })}
    </div>
  );
}

export function parseRichText(value: string): RichSegment[] {
  const source = String(value ?? "").replace(/\r\n?/g, "\n");
  const chunks: RichSegment[] = [];
  const textLines: string[] = [];
  const codeLines: string[] = [];
  let fence: { marker: string; length: number; language?: string } | null = null;

  const flushText = () => {
    if (!textLines.length) return;
    chunks.push(...parseParagraphs(textLines));
    textLines.length = 0;
  };

  for (const line of source.split("\n")) {
    const fenceMatch = line.match(/^(`{3,}|~{3,})\s*([A-Za-z0-9_.+#-]*)\s*$/);
    if (fence) {
      if (isClosingFence(line, fence)) {
        chunks.push({ type: "code", language: fence.language, text: codeLines.join("\n") });
        codeLines.length = 0;
        fence = null;
      } else {
        codeLines.push(line);
      }
      continue;
    }

    if (fenceMatch) {
      flushText();
      fence = { marker: fenceMatch[1][0], length: fenceMatch[1].length, language: fenceMatch[2] || undefined };
      continue;
    }

    textLines.push(line);
  }

  if (fence) chunks.push({ type: "code", language: fence.language, text: codeLines.join("\n") });
  flushText();
  return chunks;
}

function parseParagraphs(lines: string[]): RichSegment[] {
  const paragraphs: string[][] = [];
  let current: string[] = [];

  for (const line of lines) {
    if (!line.trim()) {
      if (current.length) {
        paragraphs.push(current);
        current = [];
      }
      continue;
    }
    current.push(line);
  }

  if (current.length) paragraphs.push(current);
  return paragraphs.map((paragraph) => ({ type: "paragraph", lines: paragraph.map(parseInline) }));
}

function parseInline(value: string): InlineSegment[] {
  const result: InlineSegment[] = [];
  const pattern = /`([^`\n]+)`/g;
  let index = 0;
  let match: RegExpExecArray | null;

  while ((match = pattern.exec(value))) {
    result.push(...parseLinks(value.slice(index, match.index)));
    result.push({ type: "code", text: match[1] });
    index = match.index + match[0].length;
  }

  result.push(...parseLinks(value.slice(index)));
  return result;
}

function parseLinks(value: string): InlineSegment[] {
  const result: InlineSegment[] = [];
  const markdownPattern = /\[([^\]\n]{1,180})\]\((https?:\/\/[^\s<>()]+|mailto:[^\s<>()]+)\)/g;
  let index = 0;
  let match: RegExpExecArray | null;

  while ((match = markdownPattern.exec(value))) {
    result.push(...linkifyBareURLs(value.slice(index, match.index)));
    const href = safeMessageHref(match[2]);
    result.push(href ? { type: "link", text: match[1], href } : { type: "text", text: match[0] });
    index = match.index + match[0].length;
  }

  result.push(...linkifyBareURLs(value.slice(index)));
  return result;
}

function linkifyBareURLs(value: string): InlineSegment[] {
  const result: InlineSegment[] = [];
  const pattern = /\bhttps?:\/\/[^\s<>"'`]+/g;
  let index = 0;
  let match: RegExpExecArray | null;

  while ((match = pattern.exec(value))) {
    if (match.index > index) result.push({ type: "text", text: value.slice(index, match.index) });
    const { url, tail } = splitTrailingURLPunctuation(match[0]);
    const href = safeMessageHref(url);
    result.push(href ? { type: "link", text: url, href } : { type: "text", text: url });
    if (tail) result.push({ type: "text", text: tail });
    index = match.index + match[0].length;
  }

  if (index < value.length) result.push({ type: "text", text: value.slice(index) });
  return result;
}

function renderInline(segments: InlineSegment[]): ReactNode {
  return segments.map((segment, index) => {
    if (segment.type === "code") return <code key={index}>{segment.text}</code>;
    if (segment.type === "link") {
      return (
        <a href={segment.href} key={index} rel="noopener noreferrer" target="_blank">
          {segment.text}
        </a>
      );
    }
    return <span key={index}>{segment.text}</span>;
  });
}

function isClosingFence(line: string, fence: { marker: string; length: number }): boolean {
  const trimmed = line.trim();
  return trimmed.length >= fence.length && [...trimmed].every((char) => char === fence.marker);
}

function splitTrailingURLPunctuation(rawURL: string): { url: string; tail: string } {
  let url = rawURL;
  let tail = "";
  while (/[.,!?;:]$/.test(url)) {
    tail = url.slice(-1) + tail;
    url = url.slice(0, -1);
  }
  while (url.endsWith(")") && countChar(url, ")") > countChar(url, "(")) {
    tail = ")" + tail;
    url = url.slice(0, -1);
  }
  return { url, tail };
}

function safeMessageHref(value: string): string {
  const href = value.trim();
  if (!/^(https?:\/\/|mailto:)/i.test(href)) return "";
  try {
    const url = new URL(href);
    return ["http:", "https:", "mailto:"].includes(url.protocol.toLowerCase()) ? href : "";
  } catch {
    return "";
  }
}

function countChar(value: string, char: string): number {
  return [...value].filter((item) => item === char).length;
}
