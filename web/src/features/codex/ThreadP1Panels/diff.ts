export type DiffFile = {
  file: string;
  hunks: DiffHunk[];
};

export type DiffHunk = {
  header: string;
  lines: DiffLine[];
};

export type DiffLine = {
  kind: "add" | "remove" | "context" | "meta";
  text: string;
  oldLine?: number;
  newLine?: number;
};

export function parseDiff(diff: string): DiffFile[] {
  const files: DiffFile[] = [];
  let currentFile: DiffFile | null = null;
  let currentHunk: DiffHunk | null = null;
  let oldLine = 0;
  let newLine = 0;

  for (const rawLine of diff.split("\n")) {
    if (rawLine.startsWith("diff --git ")) {
      const file = parseDiffFile(rawLine);
      currentFile = { file, hunks: [] };
      files.push(currentFile);
      currentHunk = null;
      continue;
    }
    if (!currentFile) continue;
    if (rawLine.startsWith("@@")) {
      const parsed = parseHunkHeader(rawLine);
      oldLine = parsed.oldStart;
      newLine = parsed.newStart;
      currentHunk = { header: rawLine, lines: [] };
      currentFile.hunks.push(currentHunk);
      continue;
    }
    if (!currentHunk) {
      currentHunk = { header: "file header", lines: [] };
      currentFile.hunks.push(currentHunk);
    }
    if (rawLine.startsWith("+") && !rawLine.startsWith("+++")) {
      currentHunk.lines.push({ kind: "add", text: rawLine, newLine });
      newLine += 1;
    } else if (rawLine.startsWith("-") && !rawLine.startsWith("---")) {
      currentHunk.lines.push({ kind: "remove", text: rawLine, oldLine });
      oldLine += 1;
    } else if (rawLine.startsWith(" ")) {
      currentHunk.lines.push({ kind: "context", text: rawLine, oldLine, newLine });
      oldLine += 1;
      newLine += 1;
    } else {
      currentHunk.lines.push({ kind: "meta", text: rawLine });
    }
  }

  return files.filter((file) => file.hunks.some((hunk) => hunk.lines.length));
}

export function diffLineClass(kind: DiffLine["kind"]): string {
  if (kind === "add") return "bg-[var(--good-soft)] text-[var(--text)] hover:bg-[rgba(18,132,79,0.14)]";
  if (kind === "remove") return "bg-[var(--danger-soft)] text-[var(--danger)]";
  if (kind === "meta") return "text-[var(--muted)]";
  return "hover:bg-[var(--surface-soft)]";
}

function parseDiffFile(line: string): string {
  const match = line.match(/^diff --git a\/(.+?) b\/(.+)$/);
  if (!match) return line.replace(/^diff --git\s+/, "");
  return match[2] || match[1];
}

function parseHunkHeader(line: string): { oldStart: number; newStart: number } {
  const match = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
  return { oldStart: match ? Number(match[1]) : 0, newStart: match ? Number(match[2]) : 0 };
}
