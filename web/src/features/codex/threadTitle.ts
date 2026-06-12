export function shouldDeriveConversationTitle(title?: string): boolean {
  const value = (title || "").trim();
  return value === "" || value === "新对话" || value === "New conversation" || value === "Untitled";
}

export function titleFromPrompt(prompt: string): string {
  const value = prompt.trim().replace(/\s+/g, " ");
  if (!value) return "新对话";
  return truncateRunes(value, 48);
}

function truncateRunes(value: string, maxRunes: number): string {
  const runes = Array.from(value);
  return runes.length > maxRunes ? `${runes.slice(0, maxRunes).join("")}...` : value;
}
