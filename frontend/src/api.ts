export type Actor = {
  id: string;
  role: "intern" | "mentor" | "lead";
  team: string;
};
export type Source = {
  document_id: string;
  version_id: string;
  chunk_id: string;
  title: string;
  text: string;
  source_url: string | null;
  page_number: number | null;
  origin: "demo" | "public";
  stale: boolean;
  review_by: string | null;
  start_char: number;
  end_char: number;
};
export type Answer = {
  request_id: string;
  status:
    | "answered"
    | "sources_found"
    | "insufficient_evidence"
    | "conflict"
    | "needs_review";
  mode: "llm" | "offline" | "rules";
  steps: { text: string; source_ids: string[] }[];
  sources: Record<string, Source>;
  next_step: string;
  model: string | null;
  action: string | null;
};
export type Proposal = {
  resource: string;
  access: "read" | "write";
  reason: string;
};
export type AccessRequest = Proposal & {
  id: string;
  state: "proposed" | "submitting" | "submitted" | "unknown" | "cancelled";
  version: number;
  created_at: string;
  updated_at: string;
  ticket?: { id: string; status: "pending" | "granted" | "rejected" };
};
export type Progress = {
  request_id: string;
  resource: string;
  state: "todo" | "waiting" | "blocked" | "done";
  next_step:
    | "confirm_proposal"
    | "refresh_request"
    | "wait_for_approval"
    | "start_local_setup"
    | "contact_mentor"
    | "create_new_proposal";
};

export class ApiError extends Error {
  constructor(
    public code: string,
    public status = 0,
  ) {
    super(code);
  }
}
export const errorText = (error: unknown): string => {
  const code = error instanceof ApiError ? error.code : "network_error";
  return (
    (
      {
        invalid_input:
          "Проверьте поля запроса. Возможно, текст слишком длинный.",
        unauthorized: "Сессия недействительна. Выберите профиль заново.",
        forbidden: "Эта операция недоступна вашему профилю.",
        not_found: "Заявка не найдена или недоступна вашему профилю.",
        conflict:
          "Заявка уже изменилась. Обновите её и проверьте текущее состояние.",
        temporarily_unavailable:
          "Сервис временно недоступен. Попробуйте ещё раз позже.",
        resource_exhausted:
          "Помощник занят. Подождите немного перед повторной попыткой.",
        deadline_exceeded:
          "Ответ занял слишком много времени. Повторите запрос вручную.",
        request_cancelled:
          "Ожидание остановлено. Операция на сервере могла продолжиться.",
        internal_error: "Не удалось обработать запрос. Попробуйте позже.",
        invalid_response: "Сервис вернул неожиданный ответ. Попробуйте позже.",
        network_error:
          "Нет связи с сервисом. Проверьте подключение и повторите запрос.",
      } as Record<string, string>
    )[code] ?? "Не удалось выполнить запрос. Попробуйте позже."
  );
};

export class Api {
  constructor(
    private token: string,
    private fetcher: typeof fetch = fetch,
  ) {}
  private async request<T>(
    path: string,
    method = "GET",
    body?: unknown,
    extra?: Record<string, string>,
    signal?: AbortSignal,
  ): Promise<T> {
    let response: Response;
    try {
      response = await this.fetcher(path, {
        method,
        signal,
        cache: "no-store",
        credentials: "same-origin",
        headers: {
          Authorization: `Bearer ${this.token}`,
          ...(body === undefined ? {} : { "Content-Type": "application/json" }),
          ...extra,
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
    } catch (error) {
      if (
        typeof error === "object" &&
        error !== null &&
        "name" in error &&
        error.name === "AbortError"
      )
        throw new ApiError("request_cancelled");
      throw new ApiError("network_error");
    }
    let data;
    try {
      data = await response.json();
    } catch {
      throw new ApiError("invalid_response", response.status);
    }
    if (!response.ok)
      throw new ApiError(
        data?.error?.code ?? "internal_error",
        response.status,
      );
    if (!data || typeof data !== "object")
      throw new ApiError("invalid_response", response.status);
    return data as T;
  }
  me(signal?: AbortSignal) {
    return this.request<Actor>("/api/me", "GET", undefined, undefined, signal);
  }
  answer(
    question: string,
    mode: "offline" | "llm",
    top_k: number,
    signal?: AbortSignal,
  ) {
    return this.request<Answer>(
      "/api/rag/answers",
      "POST",
      { question, mode, top_k },
      undefined,
      signal,
    );
  }
  search(query: string, top_k: number, signal?: AbortSignal) {
    return this.request<{ request_id: string; sources: Source[] }>(
      "/api/rag/search",
      "POST",
      { query, top_k },
      undefined,
      signal,
    );
  }
  list(signal?: AbortSignal) {
    return this.request<{ items: AccessRequest[] }>(
      "/api/access-requests",
      "GET",
      undefined,
      undefined,
      signal,
    );
  }
  get(id: string) {
    return this.request<AccessRequest>(
      `/api/access-requests/${encodeURIComponent(id)}`,
    );
  }
  propose(proposal: Proposal, key: string) {
    return this.request<AccessRequest>(
      "/api/access-requests",
      "POST",
      proposal,
      { "Idempotency-Key": key },
    );
  }
  confirm(request: AccessRequest) {
    return this.request<AccessRequest>(
      `/api/access-requests/${encodeURIComponent(request.id)}/confirm`,
      "POST",
      { version: request.version },
    );
  }
  cancel(request: AccessRequest) {
    return this.request<AccessRequest>(
      `/api/access-requests/${encodeURIComponent(request.id)}/cancel`,
      "POST",
      { version: request.version },
    );
  }
  refresh(id: string) {
    return this.request<AccessRequest>(
      `/api/access-requests/${encodeURIComponent(id)}/refresh`,
      "POST",
    );
  }
  progress(signal?: AbortSignal) {
    return this.request<{ items: Progress[] }>(
      "/api/onboarding",
      "GET",
      undefined,
      undefined,
      signal,
    );
  }
  health() {
    return this.request<{ status: string }>("/health");
  }
  ready() {
    return this.request<{ status: string }>("/ready");
  }
}
