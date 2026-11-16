import { useEffect, useMemo, useRef, useState } from "react";
import { Api, ApiError, errorText, type Actor } from "./api";
import { Assistant } from "./Assistant";
import { Requests } from "./Requests";
import { Buddy, Icon, Loading, Notice } from "./ui";

const profiles = [
  ["demo-intern-platform", "Стажёр · Platform"],
  ["demo-intern-search", "Стажёр · Search"],
  ["demo-mentor-platform", "Наставник · Platform"],
];
const pages = [
  { id: "assistant", label: "Помощник", icon: "chat" },
  { id: "knowledge", label: "База знаний", icon: "book" },
  { id: "requests", label: "Мои заявки", icon: "lock" },
  { id: "plan", label: "Следующий шаг", icon: "list" },
];
function savedToken() {
  try {
    const saved = sessionStorage.getItem("buddy-profile");
    return profiles.some(([token]) => token === saved)
      ? saved!
      : profiles[0][0];
  } catch {
    return profiles[0][0];
  }
}
export function App() {
  const mainContent = useRef<HTMLElement>(null);
  const [token, setToken] = useState(savedToken);
  const api = useMemo(() => new Api(token), [token]);
  const [actor, setActor] = useState<Actor | null>(null);
  const [error, setError] = useState("");
  const [ready, setReady] = useState<"checking" | "ready" | "unavailable">(
    "checking",
  );
  const [page, setPage] = useState("assistant");
  const [proposal, setProposal] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    const controller = new AbortController();
    setActor(null);
    setError("");
    setReady("checking");
    let timer: ReturnType<typeof setTimeout>;
    async function connect(retries = 0) {
      try {
        const result = await api.me(controller.signal);
        if (!live) return;
        setActor(result);
        const status = await Promise.allSettled([
          api.health(controller.signal),
          api.ready(controller.signal),
        ]);
        if (live)
          setReady(
            status.every((s) => s.status === "fulfilled")
              ? "ready"
              : "unavailable",
          );
      } catch (failure) {
        if (!live) return;
        const transient =
          failure instanceof ApiError &&
          [
            "network_error",
            "temporarily_unavailable",
            "deadline_exceeded",
          ].includes(failure.code);
        if (transient && retries < 20) {
          timer = setTimeout(() => void connect(retries + 1), 2000);
        } else {
          setError(errorText(failure));
          setReady("unavailable");
        }
      }
    }
    void connect();
    return () => {
      live = false;
      clearTimeout(timer);
      controller.abort();
    };
  }, [api, attempt]);
  function switchProfile(value: string) {
    setActor(null);
    setPage("assistant");
    setProposal(false);
    setToken(value);
    try {
      sessionStorage.setItem("buddy-profile", value);
    } catch {
      /* Memory-only session is sufficient. */
    }
  }
  return (
    <div className="app-shell">
      <button
        type="button"
        className="skip-link"
        onClick={() => mainContent.current?.focus()}
      >
        Перейти к содержимому
      </button>
      <aside className="sidebar">
        <a
          className="brand"
          href="#"
          onClick={(e) => {
            e.preventDefault();
            setPage("assistant");
          }}
        >
          <Buddy />
          <span>
            ai<span className="brand-dash">—</span>buddy
            <span className="brand-dot">.</span>
          </span>
        </a>
        <span className="sidebar-label">ТВОЁ ПРОСТРАНСТВО</span>
        <nav aria-label="Основная навигация">
          {pages.map((item) => (
            <button
              key={item.id}
              className={`nav-item ${page === item.id ? "active" : ""}`}
              aria-current={page === item.id ? "page" : undefined}
              onClick={() => setPage(item.id)}
            >
              <Icon name={item.icon} />
              <span>{item.label}</span>
              {page === item.id && <span className="nav-dot" />}
            </button>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <div className="buddy-note">
            <span className="tiny-flower">✳</span>
            <strong>
              Большой путь начинается
              <br />с маленького вопроса.
            </strong>
            <p>
              Не обязательно знать всё
              <br />в первый день.
            </p>
          </div>
          <details className="demo-profiles">
            <summary>Демонстрация прав доступа</summary>
            <p>
              Переключение тестовых пользователей. Меняет доступные материалы,
              переписку и заявки.
            </p>
            <label className="profile-switch">
              <span className="avatar">
                {actor?.role === "mentor" ? "Н" : "С"}
              </span>
              <span>
                <small>Демо-профиль</small>
                <select
                  aria-label="Демо-профиль"
                  value={token}
                  onChange={(e) => switchProfile(e.target.value)}
                >
                  {profiles.map(([value, label]) => (
                    <option value={value} key={value}>
                      {label}
                    </option>
                  ))}
                </select>
              </span>
            </label>
          </details>
        </div>
      </aside>
      <div className="main-shell">
        <header className="topbar">
          <div>
            <span className="breadcrumb">Мой онбординг</span>
            <span className="breadcrumb-divider">/</span>
            <strong>{pages.find((p) => p.id === page)!.label}</strong>
          </div>
          <span className="demo-pill">Учебная среда</span>
        </header>
        <main ref={mainContent} id="main-content" tabIndex={-1}>
          {error ? (
            <div className="workspace">
              <Notice>{error}</Notice>
              <button
                className="button primary"
                onClick={() => setAttempt((v) => v + 1)}
              >
                Повторить подключение
              </button>
            </div>
          ) : !actor ? (
            <div className="workspace">
              <Loading text="Сервисы запускаются. Подключаем твой профиль…" />
            </div>
          ) : (
            <div key={token}>
              {page === "assistant" || page === "knowledge" ? (
                <Assistant
                  key={page}
                  api={api}
                  actor={actor}
                  search={page === "knowledge"}
                  onProposal={() => {
                    setProposal(true);
                    setPage("requests");
                  }}
                />
              ) : (
                <Requests
                  api={api}
                  actor={actor}
                  plan={page === "plan"}
                  initialProposal={proposal}
                  onConsumed={() => setProposal(false)}
                />
              )}
            </div>
          )}
        </main>
        <footer className="app-footer">
          <span>
            <span className={`status-dot ${ready}`} />
            {ready === "ready"
              ? "Сервисы доступны"
              : ready === "checking"
                ? "Проверяем соединение"
                : "Связь с сервисом ограничена"}
          </span>
          <span>AI-buddy · прототип для адаптации</span>
        </footer>
      </div>
    </div>
  );
}
