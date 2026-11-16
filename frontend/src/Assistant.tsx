import { useEffect, useRef, useState } from "react";
import {
  Api,
  ApiError,
  errorText,
  type Actor,
  type Answer,
  type Source,
  type Conversation,
  type ConversationTurn,
} from "./api";
import { Buddy, Icon, Loading, Notice, SourceCard } from "./ui";

type Turn = {
  id: number | string;
  key?: string;
  state?: "pending" | "completed" | "failed" | "uncertain";
  question: string;
  answer?: Answer;
  sources?: Source[];
  error?: string;
  mode: "offline" | "llm";
  topK: number;
  search: boolean;
};
function savedTurn(turn: ConversationTurn): Turn {
  return {
    id: turn.id,
    key: turn.client_id,
    question: turn.question,
    mode: turn.mode,
    topK: turn.top_k,
    search: false,
    state: turn.state,
    answer: turn.answer,
    error: turn.error_code
      ? errorText(new ApiError(turn.error_code))
      : undefined,
  };
}
const statusText: Record<Answer["status"], string> = {
  answered: "Ответ по источникам",
  sources_found: "Выдержка из инструкции",
  insufficient_evidence: "Недостаточно сведений",
  conflict: "В инструкциях есть расхождения",
  needs_review: "Нужна актуальная инструкция",
};
const prompts = [
  {
    icon: "lock",
    title: "Не открывается Jira",
    detail: "Разберёмся с доступом к первой задаче",
    question: "Не открывается Jira, ошибка 403",
  },
  {
    icon: "book",
    title: "Мой первый pull request",
    detail: "Репозиторий, тесты и отправка изменений",
    question: "Куда отправить pull request?",
  },
  {
    icon: "chat",
    title: "Кого позвать на ревью?",
    detail: "Найдём правило и проверим источники",
    question: "Кого позвать на ревью?",
  },
];
export function Assistant({
  api,
  actor,
  search,
  onProposal,
}: {
  api: Api;
  actor: Actor;
  search: boolean;
  onProposal: () => void;
}) {
  const [turns, setTurns] = useState<Turn[]>([]);
  const [conversationID, setConversationID] = useState("");
  const [loading, setLoading] = useState(!search);
  const [loadError, setLoadError] = useState("");
  const [loadAttempt, setLoadAttempt] = useState(0);
  const pending = turns.some((turn) => turn.state === "pending");
  const [question, setQuestion] = useState("");
  const [mode, setMode] = useState<"offline" | "llm">("offline");
  const [topK, setTopK] = useState(3);
  const [busy, setBusy] = useState(false);
  const [activeSources, setActiveSources] = useState<Record<string, Source>>(
    {},
  );
  const control = useRef<AbortController | null>(null);
  const live = useRef(true);
  const serial = useRef(0);
  const end = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
      control.current?.abort();
    };
  }, []);
  useEffect(() => {
    end.current?.scrollIntoView?.({ behavior: "smooth", block: "nearest" });
  }, [turns, busy]);
  function restore(conversation: Conversation) {
    setConversationID(conversation.id);
    const restored = conversation.turns.map(savedTurn);
    setTurns(restored);
    const last = [...restored].reverse().find((turn) => turn.answer);
    setActiveSources(last?.answer?.sources ?? {});
  }
  useEffect(() => {
    if (search) return;
    let active = true;
    const controller = new AbortController();
    setLoading(true);
    setLoadError("");
    api
      .conversation(controller.signal)
      .then((conversation) => {
        if (active) restore(conversation);
      })
      .catch((error) => {
        if (active) setLoadError(errorText(error));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [api, search, loadAttempt]);
  useEffect(() => {
    if (search || !pending || busy) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const controller = new AbortController();
    async function poll() {
      try {
        const conversation = await api.conversation(controller.signal);
        if (!active) return;
        restore(conversation);
        setLoadError("");
      } catch (error) {
        if (active) setLoadError(errorText(error));
      }
      if (active) timer = setTimeout(() => void poll(), 2000);
    }
    timer = setTimeout(() => void poll(), 1000);
    return () => {
      active = false;
      clearTimeout(timer);
      controller.abort();
    };
  }, [api, search, pending, busy]);
  async function send(text: string, retry?: Turn) {
    if (
      control.current ||
      !text.trim() ||
      topK < 1 ||
      topK > 50 ||
      pending ||
      loading ||
      (!search && !conversationID)
    )
      return;
    if (
      Array.from(text).length > 4000 ||
      /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/.test(text)
    )
      return;
    const turn: Turn = {
      id: retry?.state === "uncertain" ? retry.id : ++serial.current,
      key: retry?.state === "uncertain" ? retry.key : crypto.randomUUID(),
      question: text.trim(),
      mode: retry?.mode ?? mode,
      topK: retry?.topK ?? topK,
      search: retry?.search ?? search,
    };
    const controller = new AbortController();
    control.current = controller;
    setTurns((old) =>
      retry?.state === "uncertain"
        ? old.map((t) => (t.id === retry.id ? turn : t))
        : [...old, turn],
    );
    setQuestion("");
    setBusy(true);
    const timer = setTimeout(() => controller.abort(), 40000);
    try {
      if (turn.search) {
        const result = await api.search(
          turn.question,
          turn.topK,
          controller.signal,
        );
        if (!live.current) return;
        setTurns((old) =>
          old.map((t) =>
            t.id === turn.id ? { ...t, sources: result.sources } : t,
          ),
        );
        setActiveSources(
          Object.fromEntries(result.sources.map((s, i) => [`S${i + 1}`, s])),
        );
      } else {
        const result = await api.sendMessage(
          conversationID,
          turn.key!,
          turn.question,
          turn.mode,
          turn.topK,
          controller.signal,
        );
        if (!live.current) return;
        setTurns((old) =>
          old.map((t) => (t.id === turn.id ? savedTurn(result) : t)),
        );
        if (result.answer) setActiveSources(result.answer.sources);
      }
    } catch (failure) {
      if (live.current)
        setTurns((old) =>
          old.map((t) =>
            t.id === turn.id
              ? { ...t, error: errorText(failure), state: "uncertain" }
              : t,
          ),
        );
      if (!turn.search && live.current) {
        try {
          const current = await api.conversation();
          if (live.current) {
            if (
              current.id !== conversationID ||
              (failure instanceof ApiError && failure.code === "conflict")
            ) {
              restore(current);
              setQuestion(turn.question);
              setLoadError(
                "Диалог изменился. Проверь переписку перед повторной отправкой.",
              );
            } else if (
              current.turns.some((item) => item.client_id === turn.key)
            )
              restore(current);
          }
        } catch {}
      }
    } finally {
      clearTimeout(timer);
      control.current = null;
      if (live.current) setBusy(false);
    }
  }
  async function clear() {
    if (!search) {
      if (!conversationID || busy || pending) return;
      setLoading(true);
      try {
        restore(await api.newConversation(conversationID));
        setLoadError("");
      } catch (error) {
        setLoadError(errorText(error));
      } finally {
        if (live.current) setLoading(false);
      }
      return;
    }
    control.current?.abort();
    setTurns([]);
    setActiveSources({});
    setQuestion("");
    input.current?.focus();
  }
  return (
    <div className="assistant-layout">
      <section
        className="assistant-main"
        aria-label={search ? "Поиск инструкций" : "Чат с помощником"}
      >
        <div className="chat-toolbar">
          <span>
            <span className="status-dot" />
            {search
              ? "Только доступные вам материалы"
              : "Помощь с контекстом твоей команды"}
          </span>
          <button
            className="text-button"
            disabled={busy || pending || loading}
            onClick={() => void clear()}
          >
            <Icon name="plus" size={17} />
            Новый диалог
          </button>
        </div>
        <div className="conversation">
          {loading && <Loading text="Восстанавливаем переписку…" />}
          {loadError && (
            <>
              <Notice>{loadError}</Notice>
              <button
                className="text-button"
                onClick={() => setLoadAttempt((value) => value + 1)}
              >
                Обновить переписку
              </button>
            </>
          )}
          {pending && !busy && (
            <Loading text="Ответ ещё готовится. Переписка сохранена, можно вернуться позже." />
          )}
          {!turns.length && (
            <div className="welcome">
              <span className="eyebrow">ПЕРВЫЕ ШАГИ. БОЛЬШИЕ ВОЗМОЖНОСТИ.</span>
              <Buddy large />
              <h1>
                {search
                  ? "Нужная инструкция —\nближе, чем кажется."
                  : "Осваивайся.\nЯ рядом."}
              </h1>
              <p>
                {search
                  ? "Найди рабочий материал, проверь его источник и актуальность."
                  : "Помогу найти инструкцию, разобраться с доступами\nи сделать следующий шаг в твоей команде."}
              </p>
              <div className="prompt-grid">
                {prompts.map((p) => (
                  <button
                    className="prompt-card"
                    key={p.title}
                    onClick={() => void send(p.question)}
                    disabled={
                      busy || pending || loading || (!search && !conversationID)
                    }
                  >
                    <span className="prompt-icon">
                      <Icon name={p.icon} />
                    </span>
                    <strong>{p.title}</strong>
                    <span>{p.detail}</span>
                    <Icon name="chevron" size={16} />
                  </button>
                ))}
              </div>
              <p className="context-caption">
                <Icon name="lock" size={13} />
                Покажу только материалы, доступные твоей роли и команде
              </p>
            </div>
          )}
          {turns.map((turn) => (
            <article className="turn" key={turn.id}>
              <div className="user-message">
                <span className="message-label">ТЫ</span>
                <p className="preserve">{turn.question}</p>
              </div>
              {(turn.answer || turn.sources || turn.error) && (
                <div className="assistant-message">
                  <Buddy />
                  <div className="message-body">
                    <span className="message-label">AI-BUDDY</span>
                    {turn.error && (
                      <>
                        <Notice>{turn.error}</Notice>
                        <button
                          className="button secondary"
                          disabled={
                            busy ||
                            pending ||
                            loading ||
                            (!search && !conversationID)
                          }
                          onClick={() => void send(turn.question, turn)}
                        >
                          Повторить запрос
                        </button>
                        {!turn.search && turn.mode === "llm" && (
                          <button
                            className="text-button"
                            disabled={
                              busy ||
                              pending ||
                              loading ||
                              (!search && !conversationID)
                            }
                            onClick={() =>
                              void send(turn.question, {
                                ...turn,
                                mode: "offline",
                                state: "failed",
                              })
                            }
                          >
                            Показать офлайн-выдержку
                          </button>
                        )}
                      </>
                    )}
                    {turn.answer && (
                      <>
                        <span className={`answer-status ${turn.answer.status}`}>
                          {statusText[turn.answer.status]}
                        </span>
                        <ol className="answer-steps">
                          {turn.answer.steps.map((step, index) => (
                            <li key={index}>
                              <span className="preserve">{step.text}</span>
                              <span className="citations">
                                {step.source_ids.map((ref) => (
                                  <button
                                    key={ref}
                                    onClick={() =>
                                      setActiveSources(turn.answer!.sources)
                                    }
                                    title="Показать источники ответа"
                                  >
                                    [{ref}]
                                  </button>
                                ))}
                              </span>
                            </li>
                          ))}
                        </ol>
                        {turn.answer.next_step && (
                          <p className="callout preserve">
                            {turn.answer.next_step}
                          </p>
                        )}
                        {Object.values(turn.answer.sources).some(
                          (s) => s.origin === "demo",
                        ) && (
                          <p className="demo-note">
                            Учебные инструкции — не регламенты Авито.
                          </p>
                        )}
                        <div className="inline-sources">
                          {Object.entries(turn.answer.sources).map(
                            ([label, source]) => (
                              <SourceCard
                                key={label}
                                label={label}
                                source={source}
                              />
                            ),
                          )}
                        </div>
                        {turn.answer.action === "preview_jira_access" &&
                          actor.role === "intern" &&
                          actor.team === "platform" && (
                            <button
                              className="button primary"
                              onClick={onProposal}
                            >
                              <Icon name="lock" size={17} />
                              Подготовить заявку на чтение BUD
                              <Icon name="chevron" size={16} />
                            </button>
                          )}
                        <p className="answer-footnote">
                          {
                            {
                              llm: "Ответ модели",
                              offline: "Офлайн · без обращения к модели",
                              rules: "Проверка по правилам",
                            }[turn.answer.mode]
                          }
                          {turn.answer.model ? ` · ${turn.answer.model}` : ""}
                        </p>
                      </>
                    )}
                    {turn.sources && (
                      <>
                        {turn.sources.length ? (
                          <>
                            <h2>Найденные материалы</h2>
                            {turn.sources.map((source, i) => (
                              <SourceCard
                                key={`${source.version_id}:${source.chunk_id}`}
                                label={`S${i + 1}`}
                                source={source}
                              />
                            ))}
                          </>
                        ) : (
                          <p className="callout">
                            В доступных инструкциях ничего не найдено. Уточни у
                            наставника, затем у руководителя; по кадровому
                            вопросу — у HRBP.
                          </p>
                        )}
                      </>
                    )}
                  </div>
                </div>
              )}
            </article>
          ))}
          {busy && (
            <div className="assistant-message pending">
              <Buddy />
              <div>
                <Loading
                  text={
                    search
                      ? "Ищем доступные инструкции…"
                      : mode === "llm"
                        ? "Ищем источники и ждём ответ модели…"
                        : "Проверяем инструкции…"
                  }
                />
                <button
                  className="text-button"
                  onClick={() => control.current?.abort()}
                >
                  Остановить ожидание
                </button>
              </div>
            </div>
          )}
          <div ref={end} />
        </div>
        <form
          className="composer"
          onSubmit={(e) => {
            e.preventDefault();
            void send(question);
          }}
        >
          <label className="sr-only" htmlFor="question">
            {search ? "Поиск по базе знаний" : "Твой вопрос"}
          </label>
          <textarea
            ref={input}
            id="question"
            value={question}
            onChange={(e) => setQuestion(e.target.value)}
            placeholder={
              search
                ? "Какую инструкцию ищем?"
                : "С чего начнём? Задай свой вопрос…"
            }
            maxLength={4000}
            rows={2}
            onKeyDown={(e) => {
              if (
                e.key === "Enter" &&
                !e.shiftKey &&
                !e.nativeEvent.isComposing
              ) {
                e.preventDefault();
                void send(question);
              }
            }}
          />
          <div className="composer-bottom">
            <div className="composer-options">
              {!search && (
                <label>
                  <span className="sr-only">Режим ответа</span>
                  <select
                    value={mode}
                    onChange={(e) => setMode(e.target.value as typeof mode)}
                  >
                    <option value="offline">Офлайн · источники</option>
                    <option value="llm">С моделью</option>
                  </select>
                </label>
              )}
              <label className="top-k">
                Источников
                <input
                  aria-label="Количество источников"
                  type="number"
                  min="1"
                  max="50"
                  required
                  value={topK}
                  onChange={(e) => setTopK(Number(e.target.value))}
                />
              </label>
            </div>
            <button
              className="send-button"
              aria-label={search ? "Найти" : "Отправить вопрос"}
              disabled={
                busy ||
                pending ||
                loading ||
                (!search && !conversationID) ||
                !question.trim() ||
                topK < 1 ||
                topK > 50
              }
            >
              <Icon name={search ? "search" : "arrow"} />
            </button>
          </div>
        </form>
        <p className="composer-note">
          {search
            ? "Результаты ограничены правами твоего профиля."
            : "Помощник может ошибаться. Проверяй источники перед действием."}
        </p>
      </section>
      <aside className="context-panel" aria-label="Контекст ответа">
        <div className="context-header">
          <Icon name="book" size={18} />
          <h2>Опора для ответа</h2>
        </div>
        {Object.keys(activeSources).length ? (
          <>
            <p className="muted">Материалы последнего выбранного ответа</p>
            {Object.entries(activeSources).map(([label, source]) => (
              <SourceCard key={label} label={label} source={source} />
            ))}
          </>
        ) : (
          <div className="source-empty">
            <span className="paper-art">
              <i />
              <i />
              <i />
            </span>
            <h3>
              Сначала вопрос.
              <br />
              Потом — источники.
            </h3>
            <p>
              Здесь появятся документы,
              <br />
              на которых основан ответ.
            </p>
          </div>
        )}
        <div className="context-tip">
          <span className="tile-icon">
            <Icon name="spark" size={18} />
          </span>
          <h3>Твой контекст уже учтён</h3>
          <p>
            Команда <strong>{actor.team}</strong>, роль{" "}
            <strong>
              {actor.role === "intern"
                ? "стажёр"
                : actor.role === "mentor"
                  ? "наставник"
                  : "руководитель"}
            </strong>
            . Права проверяются на сервере.
          </p>
        </div>
      </aside>
    </div>
  );
}
