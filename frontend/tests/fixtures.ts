import type { AccessRequest, Actor, Answer, Source } from "../src/api";
export const actor: Actor = {
  id: "intern-platform",
  role: "intern",
  team: "platform",
};
export const source: Source = {
  document_id: "jira-access",
  version_id: "version-1",
  chunk_id: "chunk-1",
  title: "Доступ к Jira",
  text: "Запроси чтение BUD.",
  source_url: "https://example.org/instructions",
  page_number: 2,
  origin: "demo",
  stale: false,
  review_by: null,
  start_char: 0,
  end_char: 20,
};
export const answer: Answer = {
  request_id: "req-1",
  status: "sources_found",
  mode: "offline",
  steps: [{ text: "Запроси чтение BUD.", source_ids: ["S1"] }],
  sources: { S1: source },
  next_step: "",
  model: null,
  action: "preview_jira_access",
};
export const request: AccessRequest = {
  id: "request-1",
  resource: "jira:BUD",
  access: "read",
  reason: "Первая задача",
  state: "proposed",
  version: 1,
  created_at: "2026-11-15T12:00:00Z",
  updated_at: "2026-11-15T12:00:00Z",
};
