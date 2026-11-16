import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Api, ApiError } from "../src/api";
import { Assistant } from "../src/Assistant";
import { Buddy, Icon, RequestStatus, SourceCard } from "../src/ui";
import { actor, answer, request, source } from "./fixtures";

function persistence(api: Api) {
  vi.spyOn(api, "conversation").mockResolvedValue({
    id: "conversation-1",
    turns: [],
  });
  vi.spyOn(api, "newConversation").mockResolvedValue({
    id: "conversation-2",
    turns: [],
  });
  vi.spyOn(api, "sendMessage").mockImplementation(
    async (_id, key, question, mode, top_k, signal) => {
      const base = {
        id: key,
        client_id: key,
        question,
        mode,
        top_k,
        created_at: "2026-11-16T00:00:00Z",
      };
      try {
        return {
          ...base,
          state: "completed",
          answer: await api.answer(question, mode, top_k, signal),
        };
      } catch (error) {
        if (error instanceof ApiError && error.code === "request_cancelled")
          throw error;
        return {
          ...base,
          state: "failed",
          error_code: "temporarily_unavailable",
        };
      }
    },
  );
}
function setup(search = false, result = answer) {
  const api = new Api("test");
  persistence(api);
  const onProposal = vi.fn();
  vi.spyOn(api, "answer").mockResolvedValue(result);
  vi.spyOn(api, "search").mockResolvedValue({
    request_id: "id",
    sources: [source],
  });
  render(
    <Assistant
      api={api}
      actor={actor}
      search={search}
      onProposal={onProposal}
    />,
  );
  return { api, onProposal, user: userEvent.setup() };
}
describe("assistant and evidence", () => {
  it("runs the Jira flow with exact inputs, citations and an action that only opens a proposal", async () => {
    const { api, onProposal, user } = setup();
    await user.click(
      screen.getByRole("button", { name: /Не открывается Jira/ }),
    );
    expect(api.answer).toHaveBeenCalledWith(
      "Не открывается Jira, ошибка 403",
      "offline",
      3,
      expect.any(AbortSignal),
    );
    await user.click(
      await screen.findByRole("button", { name: /Подготовить заявку/ }),
    );
    expect(onProposal).toHaveBeenCalledOnce();
    expect(
      screen.getByText("Учебные инструкции — не регламенты Авито."),
    ).toBeVisible();
    await user.click(screen.getByRole("button", { name: "[S1]" }));
    expect(screen.getAllByText("Доступ к Jira").length).toBeGreaterThan(0);
    await user.click(screen.getByRole("button", { name: "Новый диалог" }));
    expect(screen.getByText(/Осваивайся/)).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "[S1]" }),
    ).not.toBeInTheDocument();
  });
  it("searches sources and represents an empty authorized result without disclosing hidden documents", async () => {
    const { api, user } = setup(true);
    await user.type(screen.getByRole("textbox"), "Jira{Enter}");
    expect(api.search).toHaveBeenCalledWith("Jira", 3, expect.any(AbortSignal));
    expect(await screen.findByText("Найденные материалы")).toBeVisible();
    vi.mocked(api.search).mockResolvedValue({ request_id: "id", sources: [] });
    await user.type(screen.getByRole("textbox"), "unknown");
    await user.click(screen.getByRole("button", { name: "Найти" }));
    expect(
      await screen.findByText(/В доступных инструкциях ничего не найдено/),
    ).toBeVisible();
  });
  it("shows actual model, conflict, escalation and stale source evidence", async () => {
    const { user } = setup(false, {
      ...answer,
      status: "conflict",
      mode: "rules",
      model: "test-model",
      next_step: "Обратись к наставнику",
      action: null,
      sources: { S1: { ...source, stale: true, review_by: "2025-01-01" } },
    });
    await user.type(screen.getByRole("textbox"), "Кого позвать?{Enter}");
    expect(
      await screen.findByText("В инструкциях есть расхождения"),
    ).toBeVisible();
    expect(screen.getByText("Обратись к наставнику")).toBeVisible();
    expect(screen.getByText(/Проверка по правилам · test-model/)).toBeVisible();
    expect(
      screen.queryByRole("button", { name: /Подготовить заявку/ }),
    ).not.toBeInTheDocument();
  });
  it("handles LLM errors without fallback, then retries with the original mode and explicit offline choice", async () => {
    const { api, user } = setup();
    vi.mocked(api.answer).mockRejectedValueOnce(
      new ApiError("temporarily_unavailable"),
    );
    await user.selectOptions(screen.getByLabelText("Режим ответа"), "llm");
    fireEvent.change(screen.getByLabelText("Количество источников"), {
      target: { value: "5" },
    });
    await user.type(
      screen.getByRole("textbox"),
      "Jira{Shift>}{Enter}{/Shift}403",
    );
    await user.click(screen.getByRole("button", { name: "Отправить вопрос" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "временно недоступен",
    );
    expect(api.answer).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Повторить запрос" }));
    await waitFor(() => expect(api.answer).toHaveBeenCalledTimes(2));
    expect(vi.mocked(api.answer).mock.calls[1].slice(0, 3)).toEqual([
      "Jira\n403",
      "llm",
      5,
    ]);
    await user.click(
      screen.getByRole("button", { name: "Показать офлайн-выдержку" }),
    );
    await waitFor(() => expect(api.answer).toHaveBeenCalledTimes(3));
    expect(vi.mocked(api.answer).mock.calls[2][1]).toBe("offline");
  });
  it("keeps navigation responsive, prevents double submit and aborts waiting", async () => {
    const { api, user } = setup();
    vi.mocked(api.answer).mockImplementation(
      (_q, _m, _k, signal) =>
        new Promise((_, reject) =>
          signal?.addEventListener("abort", () =>
            reject(new ApiError("request_cancelled")),
          ),
        ),
    );
    await user.type(screen.getByRole("textbox"), "Jira{Enter}{Enter}");
    expect(api.answer).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("status")).toHaveTextContent(
      "Проверяем инструкции",
    );
    await user.click(
      screen.getByRole("button", { name: "Остановить ожидание" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Ожидание остановлено",
    );
  });
  it("ignores responses after unmount and stops a timed out request", async () => {
    const api = new Api("test");
    persistence(api);
    let signal: AbortSignal | undefined;
    vi.spyOn(api, "answer").mockImplementation((_q, _m, _k, s) => {
      signal = s;
      return new Promise(() => {});
    });
    const view = render(
      <Assistant api={api} actor={actor} search={false} onProposal={vi.fn()} />,
    );
    await waitFor(() => expect(api.conversation).toHaveBeenCalled());
    await waitFor(() =>
      expect(
        screen.queryByText("Восстанавливаем переписку…"),
      ).not.toBeInTheDocument(),
    );
    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "Jira" },
    });
    vi.useFakeTimers();
    fireEvent.submit(screen.getByRole("textbox").closest("form")!);
    await act(async () => {
      vi.advanceTimersByTime(40000);
    });
    expect(signal?.aborted).toBe(true);
    view.unmount();
    vi.useRealTimers();
  });
  it("rejects blank/control input and invalid top_k before calling API", async () => {
    const { api } = setup();
    fireEvent.submit(screen.getByRole("textbox").closest("form")!);
    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "bad\x00input" },
    });
    fireEvent.submit(screen.getByRole("textbox").closest("form")!);
    expect(api.answer).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText("Количество источников"), {
      target: { value: "0" },
    });
    expect(
      screen.getByRole("button", { name: "Отправить вопрос" }),
    ).toBeDisabled();
  });
  it("renders untrusted source/answer text safely and only permits safe original links", () => {
    const malicious = {
      ...source,
      text: "<script>alert(1)</script>",
      source_url: "javascript:alert(1)",
      origin: "public" as const,
      stale: true,
      review_by: "2025-01-01",
    };
    const { container, rerender } = render(
      <SourceCard source={malicious} label="S1" />,
    );
    expect(container.querySelector("script")).toBeNull();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText(source.title));
    expect(screen.getByText(/Срок проверки истёк/)).toBeVisible();
    rerender(<SourceCard source={source} label="S1" />);
    expect(screen.getByRole("link")).toHaveAttribute(
      "rel",
      "noopener noreferrer",
    );
    rerender(
      <SourceCard
        source={{ ...source, source_url: null, page_number: null }}
        label="S1"
      />,
    );
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    rerender(
      <>
        <Buddy large />
        <Icon name="missing" />
        <RequestStatus
          request={{ ...request, ticket: { id: "BUD-1", status: "granted" } }}
        />
      </>,
    );
    expect(screen.getByText("Доступ выдан")).toBeVisible();
  });
});
