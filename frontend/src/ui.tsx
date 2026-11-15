import type { ReactNode } from "react";
import type { AccessRequest, Source } from "./api";

export function Icon({ name, size = 20 }: { name: string; size?: number }) {
  const paths: Record<string, ReactNode> = {
    chat: (
      <path d="M21 11.5a8.4 8.4 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.4 8.4 0 0 1-3.8-.9L3 21l1.9-5.7a8.4 8.4 0 0 1-.9-3.8A8.5 8.5 0 0 1 8.7 3.9a8.4 8.4 0 0 1 3.8-.9h.5a8.5 8.5 0 0 1 8 8v.5Z" />
    ),
    book: (
      <>
        <path d="M12 5v15M3 4h5a4 4 0 0 1 4 3 4 4 0 0 1 4-3h5v15h-5a4 4 0 0 0-4 2 4 4 0 0 0-4-2H3Z" />
      </>
    ),
    check: <path d="m5 12 4 4L19 6" />,
    list: (
      <>
        <rect x="5" y="4" width="15" height="17" rx="3" />
        <path d="M9 3h7v4H9zM9 12h7m-7 4h5" />
      </>
    ),
    arrow: <path d="M12 19V5m-6 6 6-6 6 6" />,
    plus: <path d="M12 5v14M5 12h14" />,
    close: <path d="m6 6 12 12M6 18 18 6" />,
    refresh: (
      <>
        <path d="M20 7v5h-5M4 17v-5h5" />
        <path d="M6.1 7a7 7 0 0 1 11.6-2L20 8M4 16l2.3 3A7 7 0 0 0 17.9 17" />
      </>
    ),
    chevron: <path d="m9 5 7 7-7 7" />,
    spark: (
      <path d="m12 3 2.5 6.5L21 12l-6.5 2.5L12 21l-2.5-6.5L3 12l6.5-2.5ZM20 2v4m-2-2h4" />
    ),
    lock: (
      <>
        <rect x="5" y="10" width="14" height="11" rx="3" />
        <path d="M8 10V7a4 4 0 0 1 8 0v3m-4 5v2" />
      </>
    ),
    search: (
      <>
        <circle cx="10.5" cy="10.5" r="6.5" />
        <path d="m16 16 5 5" />
      </>
    ),
  };
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {paths[name] ?? paths.spark}
    </svg>
  );
}
export function Buddy({ large = false }: { large?: boolean }) {
  return (
    <span className={`buddy ${large ? "large" : ""}`} aria-hidden="true">
      <svg viewBox="0 0 64 64" fill="none">
        <path
          d="M18 27v8a14 14 0 0 0 28 0v-8"
          stroke="currentColor"
          strokeWidth="5"
          strokeLinecap="round"
        />
        <circle cx="23" cy="24" r="3" fill="currentColor" />
        <circle cx="41" cy="24" r="3" fill="currentColor" />
      </svg>
    </span>
  );
}
export function Notice({ children }: { children: ReactNode }) {
  return (
    <div className="notice" role="alert">
      {children}
    </div>
  );
}
export function Loading({ text = "Загружаем…" }: { text?: string }) {
  return (
    <div className="loading" role="status">
      <span className="spinner" />
      {text}
    </div>
  );
}
export function SourceCard({
  source,
  label,
}: {
  source: Source;
  label: string;
}) {
  let href: string | undefined;
  try {
    const url = new URL(source.source_url ?? "");
    if (
      ["https:", "http:"].includes(url.protocol) &&
      !url.username &&
      !url.password
    )
      href = url.href;
  } catch {
    /* A source may have no public URL. */
  }
  return (
    <details className="source-card">
      <summary>
        <span className="source-number">{label}</span>
        <span>
          <strong>{source.title}</strong>
          <small>
            {source.origin === "demo"
              ? "Учебный материал"
              : "Открытый источник"}
            {source.page_number ? ` · стр. ${source.page_number}` : ""}
          </small>
        </span>
        <Icon name="chevron" size={15} />
      </summary>
      <div className="source-content">
        <p className="preserve">{source.text}</p>
        {source.stale && (
          <p className="warning">Срок проверки истёк: {source.review_by}</p>
        )}
        {href && (
          <a href={href} target="_blank" rel="noopener noreferrer">
            Открыть оригинал ↗
          </a>
        )}
        <dl className="source-meta">
          <dt>Документ</dt>
          <dd>{source.document_id}</dd>
          <dt>Версия</dt>
          <dd>{source.version_id}</dd>
          <dt>Фрагмент</dt>
          <dd>
            {source.chunk_id} · символы {source.start_char}–{source.end_char}
          </dd>
        </dl>
      </div>
    </details>
  );
}
export const states: Record<AccessRequest["state"], string> = {
  proposed: "Ждёт подтверждения",
  submitting: "Отправляется",
  submitted: "Отправлена",
  unknown: "Результат уточняется",
  cancelled: "Отменена",
};
export const ticketStates = {
  pending: "На согласовании",
  granted: "Доступ выдан",
  rejected: "Отказано",
};
export function RequestStatus({ request }: { request: AccessRequest }) {
  const status = request.ticket?.status ?? request.state;
  return (
    <span className={`badge ${status}`}>
      <span className="status-dot" />
      {request.ticket
        ? ticketStates[request.ticket.status]
        : states[request.state]}
    </span>
  );
}
export const nextSteps = {
  confirm_proposal: "Проверьте поля и подтвердите отправку заявки.",
  refresh_request: "Уточните результат отправки кнопкой «Проверить статус».",
  wait_for_approval:
    "Дождитесь решения владельца проекта. Пока можно изучить рабочие инструкции.",
  start_local_setup:
    "Доступ получен. Вернитесь к первой задаче и настройке окружения.",
  contact_mentor: "Обсудите причину отказа и дальнейшие шаги с наставником.",
  create_new_proposal: "При необходимости подготовьте новую заявку.",
};
