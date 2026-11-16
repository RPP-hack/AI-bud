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
    | "needs_review"
    | "conversation";
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

export type ConversationTurn = {
  id: string;
  client_id: string;
  question: string;
  mode: "offline" | "llm";
  top_k: number;
  state: "pending" | "completed" | "failed";
  answer?: Answer;
  error_code?: string;
  created_at: string;
};
export type Conversation = { id: string; turns: ConversationTurn[] };

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
          "Сервис временно недоступен или ещё запускается. Подождите немного и повторите подключение.",
        search_unavailable:
          "Не удалось выполнить поиск по инструкциям. Повторите запрос позже.",
        model_unconfigured:
          "Модель не подключена: проверьте настройки API-ключа на сервере. Можно найти инструкцию без модели.",
        model_unavailable:
          "Внешняя модель сейчас недоступна. Повторите запрос или найдите инструкцию без модели.",
        model_timeout:
          "Модель не успела ответить. Повторите запрос или найдите инструкцию без модели.",
        model_rate_limited:
          "Провайдер модели ограничил число запросов. Подождите или найдите инструкцию без модели.",
        model_invalid_response:
          "Модель вернула ответ в неподходящем формате. Повторите запрос или найдите инструкцию без модели.",
        model_incomplete:
          "Модель не закончила ответ. Повторите запрос или найдите инструкцию без модели.",
        resource_exhausted:
          "Помощник занят. Подождите немного перед повторной попыткой.",
        deadline_exceeded:
          "Ответ занял слишком много времени. Повторите запрос вручную.",
        request_cancelled:
          "Ожидание остановлено. Операция на сервере могла продолжиться.",
        internal_error: "Не удалось обработать запрос. Попробуйте позже.",
        interrupted:
          "Ответ был прерван перезапуском сервиса. Можно повторить запрос.",
        conversation_full:
          "Этот диалог достиг лимита сообщений. Начните новый диалог.",
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
    private fetcher: typeof fetch = globalThis.fetch.bind(globalThis),
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
      const code =
        response.status === 504
          ? "deadline_exceeded"
          : [502, 503].includes(response.status)
            ? "temporarily_unavailable"
            : "invalid_response";
      throw new ApiError(code, response.status);
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
  conversation(signal?: AbortSignal) {
    return this.request<Conversation>(
      "/api/conversation",
      "GET",
      undefined,
      undefined,
      signal,
    );
  }
  newConversation(conversation_id: string, signal?: AbortSignal) {
    return this.request<Conversation>(
      "/api/conversation/new",
      "POST",
      { conversation_id },
      undefined,
      signal,
    );
  }
  sendMessage(
    id: string,
    key: string,
    question: string,
    mode: "offline" | "llm",
    top_k: number,
    signal?: AbortSignal,
  ) {
    return this.request<ConversationTurn>(
      `/api/conversations/${encodeURIComponent(id)}/messages`,
      "POST",
      { question, mode, top_k },
      { "Idempotency-Key": key },
      signal,
    );
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
  health(signal?: AbortSignal) {
    return this.request<{ status: string }>(
      "/health",
      "GET",
      undefined,
      undefined,
      signal,
    );
  }
  ready(signal?: AbortSignal) {
    return this.request<{ status: string }>(
      "/ready",
      "GET",
      undefined,
      undefined,
      signal,
    );
  }
}
