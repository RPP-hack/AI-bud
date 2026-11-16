import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import {
  Api,
  errorText,
  type AccessRequest,
  type Actor,
  type Progress,
  type Proposal,
} from "./api";
import { Icon, Loading, nextSteps, Notice, RequestStatus } from "./ui";

export function Requests({
  api,
  actor,
  plan,
  initialProposal,
  onConsumed,
}: {
  api: Api;
  actor: Actor;
  plan: boolean;
  initialProposal: boolean;
  onConsumed: () => void;
}) {
  const [items, setItems] = useState<AccessRequest[]>([]);
  const [progress, setProgress] = useState<Progress[]>([]);
  const [selected, setSelected] = useState<AccessRequest | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [showForm, setShowForm] = useState(initialProposal);
  const [proposal, setProposal] = useState<Proposal>({
    resource: actor.team === "platform" ? "jira:BUD" : actor.team,
    access: "read",
    reason: "Посмотреть первую задачу стажировки",
  });
  // The key is retained across timeouts/network errors and discarded only after a
  // successful proposal. The form is frozen after a failed attempt until reset.
  const key = useRef<string | null>(null);
  const submitting = useRef(false);
  const live = useRef(true);
  const [frozen, setFrozen] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const allowed =
    actor.role === "intern" &&
    ["platform", "notifications", "payments"].includes(actor.team);
  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    const result = await Promise.allSettled([api.list(), api.progress()]);
    if (!live.current) return;
    if (result[0].status === "fulfilled") setItems(result[0].value.items);
    if (result[1].status === "fulfilled") setProgress(result[1].value.items);
    const failed = result.find((r) => r.status === "rejected");
    if (failed?.status === "rejected") setError(errorText(failed.reason));
    setLoading(false);
  }, [api]);
  useEffect(() => {
    live.current = true;
    void load();
    return () => {
      live.current = false;
    };
  }, [load]);
  useEffect(() => {
    if (initialProposal) {
      setShowForm(true);
      onConsumed();
    }
  }, [initialProposal, onConsumed]);
  async function operation(run: () => Promise<AccessRequest>) {
    if (submitting.current) return;
    submitting.current = true;
    setBusy(true);
    setError("");
    try {
      const request = await run();
      if (!live.current) return;
      setSelected(request);
      setConfirmed(false);
      setItems((old) => [request, ...old.filter((r) => r.id !== request.id)]);
      await load();
    } catch (failure) {
      if (live.current) setError(errorText(failure));
    } finally {
      submitting.current = false;
      if (live.current) setBusy(false);
    }
  }
  async function prepare(event: FormEvent) {
    event.preventDefault();
    if (
      new TextEncoder().encode(proposal.reason).length > 1000 ||
      !proposal.reason.trim()
    ) {
      setError(
        "Обоснование должно содержать от 1 до 1000 байт UTF-8. Сократите текст.",
      );
      return;
    }
    key.current ??= crypto.randomUUID();
    setFrozen(true);
    await operation(async () => {
      const result = await api.propose(proposal, key.current!);
      if (live.current) {
        key.current = null;
        setFrozen(false);
        setShowForm(false);
      }
      return result;
    });
  }
  return (
    <section className="workspace requests-page">
      <div className="section-heading">
        <div>
          <span className="eyebrow">ТВОЙ РАБОЧИЙ СТАРТ</span>
          <h1>{plan ? "Следующий шаг" : "Мои заявки"}</h1>
          <p>От запроса доступа — к первой самостоятельной задаче.</p>
        </div>
        <button
          className="button secondary"
          onClick={() => void load()}
          disabled={loading || busy}
        >
          <Icon name="refresh" />
          Обновить
        </button>
      </div>
      {error && <Notice>{error}</Notice>}
      {plan && (
        <div className="journey">
          <div>
            <span>01</span>
            <strong>Найти инструкцию</strong>
            <p>Узнать процедуру своей команды</p>
          </div>
          <div>
            <span>02</span>
            <strong>Проверить и отправить</strong>
            <p>Подтвердить параметры заявки</p>
          </div>
          <div>
            <span>03</span>
            <strong>Получить результат</strong>
            <p>Дождаться решения и продолжить</p>
          </div>
        </div>
      )}
      {loading && <Loading />}
      {!loading && !items.length && (
        <div className="empty-state">
          <span className="tile-icon">
            <Icon name="list" size={30} />
          </span>
          <h2>Здесь начнётся твоя первая задача</h2>
          <p>
            Подготовь заявку на доступ. После подтверждения здесь появятся её
            номер, статус и следующий шаг.
          </p>
        </div>
      )}
      <div className="requests-grid">
        {items.map((request) => {
          const step = progress.find((p) => p.request_id === request.id);
          return (
            <button
              key={request.id}
              className={`request-card ${selected?.id === request.id ? "selected" : ""}`}
              onClick={() => {
                setSelected(request);
                setShowForm(false);
                setConfirmed(false);
                setError("");
              }}
            >
              <span className="request-top">
                <span className="resource-icon">
                  <Icon name="lock" />
                </span>
                <RequestStatus request={request} />
              </span>
              <h2>{request.resource}</h2>
              <p>
                {request.access === "read" ? "Чтение" : "Запись"} ·{" "}
                {request.ticket?.id ?? "Предпросмотр заявки"}
              </p>
              {step && <p className="next-step">{nextSteps[step.next_step]}</p>}
              <span className="text-link">
                Посмотреть заявку <Icon name="chevron" size={15} />
              </span>
            </button>
          );
        })}
      </div>
      {allowed && !showForm && (
        <button
          className="button primary"
          onClick={() => {
            setShowForm(true);
            setSelected(null);
            setError("");
          }}
        >
          <Icon name="plus" />
          Подготовить заявку
        </button>
      )}
      {!allowed && (
        <p className="muted permission-note">
          <Icon name="lock" size={16} />
          Для этого профиля оформление заявки не предусмотрено. Доступные
          инструкции можно найти в помощнике.
        </p>
      )}
      {showForm && (
        <form className="panel proposal" onSubmit={prepare}>
          <div className="panel-heading">
            <div>
              <span className="eyebrow">ШАГ 1 ИЗ 2</span>
              <h2>Подготовим заявку</h2>
            </div>
            <button
              className="icon-button"
              type="button"
              aria-label="Закрыть форму"
              onClick={() => setShowForm(false)}
            >
              <Icon name="close" />
            </button>
          </div>
          <p className="muted">
            Сначала проверь поля. Отправка в сервис доступов потребует
            отдельного подтверждения.
          </p>
          <div className="form-row">
            <label>
              Система / проект
              <input value={proposal.resource} readOnly />
            </label>
            <label>
              Уровень доступа
              <select
                value={proposal.access}
                disabled={busy || frozen}
                onChange={(e) =>
                  setProposal({
                    ...proposal,
                    access: e.target.value as Proposal["access"],
                  })
                }
              >
                <option value="read">Чтение</option>
                <option value="write">Запись (требует разрешения)</option>
              </select>
            </label>
          </div>
          <label>
            Обоснование
            <textarea
              value={proposal.reason}
              disabled={busy || frozen}
              rows={3}
              required
              maxLength={1000}
              onChange={(e) =>
                setProposal({ ...proposal, reason: e.target.value })
              }
            />
          </label>
          {frozen && !busy && (
            <p className="muted">
              Результат предыдущей попытки неизвестен. Повтор использует тот же
              ключ и параметры.
            </p>
          )}
          <button className="button primary" disabled={busy}>
            {busy
              ? "Готовим…"
              : frozen
                ? "Повторить предпросмотр"
                : "Проверить заявку"}
            <Icon name="chevron" size={17} />
          </button>
        </form>
      )}
      {selected && !showForm && (
        <section className="panel request-detail" aria-label="Детали заявки">
          <div className="panel-heading">
            <div>
              <span className="eyebrow">
                {selected.state === "proposed"
                  ? "ШАГ 2 ИЗ 2 · ПОДТВЕРЖДЕНИЕ"
                  : "СОХРАНЁННЫЙ РЕЗУЛЬТАТ"}
              </span>
              <h2>{selected.resource}</h2>
            </div>
            <RequestStatus request={selected} />
          </div>
          <dl className="request-fields">
            <dt>Уровень доступа</dt>
            <dd>{selected.access === "read" ? "Чтение" : "Запись"}</dd>
            <dt>Обоснование</dt>
            <dd className="preserve">{selected.reason}</dd>
            <dt>Номер заявки</dt>
            <dd>
              {selected.ticket?.id ?? "Ещё не отправлена в сервис доступов"}
            </dd>
            <dt>Обновлена</dt>
            <dd>{new Date(selected.updated_at).toLocaleString("ru-RU")}</dd>
          </dl>
          {selected.state === "proposed" && (
            <>
              <label className="confirmation">
                <input
                  type="checkbox"
                  checked={confirmed}
                  onChange={(e) => setConfirmed(e.target.checked)}
                  disabled={busy}
                />
                <span>
                  Проверил(а) проект, уровень доступа и обоснование. Подтверждаю
                  отправку.
                </span>
              </label>
              <div className="actions">
                <button
                  className="button primary"
                  disabled={busy || !confirmed}
                  onClick={() => void operation(() => api.confirm(selected))}
                >
                  {busy ? "Отправляем…" : "Подтвердить и отправить"}
                </button>
                <button
                  className="button secondary"
                  disabled={busy}
                  onClick={() => void operation(() => api.cancel(selected))}
                >
                  Отменить заявку
                </button>
              </div>
            </>
          )}
          {["unknown", "submitting"].includes(selected.state) && (
            <Notice>
              Результат отправки пока неизвестен. Проверьте статус этой заявки;
              создавать новую не нужно.
            </Notice>
          )}
          {selected.ticket?.status === "pending" && (
            <p className="callout">
              Заявка на согласовании у владельца проекта. Доступ ещё не выдан.
            </p>
          )}
          {selected.ticket?.status === "granted" && (
            <p className="callout success">{nextSteps.start_local_setup}</p>
          )}
          {selected.ticket?.status === "rejected" && (
            <Notice>{nextSteps.contact_mentor}</Notice>
          )}
          {selected.state === "cancelled" && (
            <p className="muted">
              Заявка отменена. {nextSteps.create_new_proposal}
            </p>
          )}
          <div className="actions">
            <button
              className="button secondary"
              disabled={busy}
              onClick={() => void operation(() => api.get(selected.id))}
            >
              Обновить данные
            </button>
            {selected.state !== "proposed" &&
              selected.state !== "cancelled" && (
                <button
                  className="button secondary"
                  disabled={busy}
                  onClick={() => void operation(() => api.refresh(selected.id))}
                >
                  <Icon name="refresh" />
                  Проверить статус
                </button>
              )}
          </div>
        </section>
      )}
    </section>
  );
}
