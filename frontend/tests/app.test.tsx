import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { App } from "../src/App";
import { actor, answer } from "./fixtures";
function installFetch() {
  const histories = new Map<string, any[]>();
  const mock = vi.fn(async (url: RequestInfo | URL, options?: RequestInit) => {
    const token = (options?.headers as Record<string, string>)?.Authorization;
    const identity = token?.includes("mentor")
      ? { ...actor, role: "mentor" }
      : token?.includes("alex")
        ? { id: "alex", role: "intern", team: "notifications" }
        : token?.includes("sam")
          ? { id: "sam", role: "intern", team: "payments" }
          : actor;
    const owner = token ?? "";
    const turns = histories.get(owner) ?? [];
    let data: unknown;
    if (url === "/api/conversation") data = { id: "conversation-1", turns };
    else if (String(url).includes("/messages")) {
      const body = JSON.parse(String(options?.body));
      const turn = {
        id: String(turns.length + 1),
        client_id: "key",
        ...body,
        state: "completed",
        answer,
        created_at: "2026-11-16T00:00:00Z",
      };
      histories.set(owner, [...turns, turn]);
      data = turn;
    } else
      data =
        url === "/api/me"
          ? identity
          : String(url).includes("/api/")
            ? { items: [] }
            : { status: "ready" };
    return new Response(JSON.stringify(data), { status: 200 });
  });
  vi.stubGlobal("fetch", mock);
  return mock;
}
describe("application shell and identity isolation", () => {
  it("navigates all screens, opens suggested request, and reloads session from server", async () => {
    const mock = installFetch();
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText(/Осваивайся/);
    expect(await screen.findByText("Сервисы доступны")).toBeVisible();
    await user.click(
      screen.getByRole("button", { name: "Перейти к содержимому" }),
    );
    expect(screen.getByRole("main")).toHaveFocus();
    await user.click(
      screen.getByRole("button", { name: /Не открывается Jira/ }),
    );
    await user.click(
      await screen.findByRole("button", {
        name: /Подготовить заявку на чтение/,
      }),
    );
    expect(await screen.findByText("Подготовим заявку")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Следующий шаг" }));
    expect(await screen.findByText("Найти инструкцию")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "База знаний" }));
    expect(await screen.findByText(/Нужная инструкция/)).toBeVisible();
    await user.click(screen.getByRole("link", { name: /ai/ }));
    expect(
      (await screen.findAllByText("Запроси чтение BUD.")).length,
    ).toBeGreaterThan(0);
    await user.click(screen.getByText("Демонстрация прав доступа"));
    await user.selectOptions(
      screen.getByLabelText("Демо-профиль"),
      "demo-mentor-platform",
    );
    await screen.findByText(/Осваивайся/);
    expect(screen.queryByText("Запроси чтение BUD.")).not.toBeInTheDocument();
    expect(sessionStorage.getItem("buddy-profile")).toBe(
      "demo-mentor-platform",
    );
    expect(
      mock.mock.calls.filter((c) => c[0] === "/api/me").at(-1)?.[1]?.headers,
    ).toMatchObject({ Authorization: "Bearer demo-mentor-platform" });
    await user.selectOptions(
      screen.getByLabelText("Демо-профиль"),
      "demo-intern-search",
    );
    await screen.findByText(/Осваивайся/);
    expect(sessionStorage.getItem("buddy-profile")).toBe("demo-intern-search");
  }, 15000);
  it("handles API unavailable, invalid session and manual reconnect", async () => {
    const mock = installFetch();
    const user = userEvent.setup();
    mock.mockImplementation(
      async () =>
        new Response('{"error":{"code":"unauthorized"}}', { status: 401 }),
    );
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Сессия недействительна",
    );
    expect(
      await screen.findByText("Связь с сервисом ограничена"),
    ).toBeVisible();
    mock.mockImplementation(
      async (url) =>
        new Response(
          JSON.stringify(
            url === "/api/me"
              ? actor
              : url === "/api/conversation"
                ? { id: "conversation-1", turns: [] }
                : { status: "ready" },
          ),
        ),
    );
    await user.click(
      screen.getByRole("button", { name: "Повторить подключение" }),
    );
    expect(await screen.findByText(/Осваивайся/)).toBeVisible();
  });
  it("recovers automatically when the gateway starts before the API", async () => {
    const mock = installFetch();
    mock.mockResolvedValueOnce(new Response("Bad gateway", { status: 502 }));
    render(<App />);
    expect(screen.getByText(/Сервисы запускаются/)).toBeVisible();
    expect(
      await screen.findByText(/Осваивайся/, {}, { timeout: 3500 }),
    ).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(mock.mock.calls.filter((c) => c[0] === "/api/me")).toHaveLength(2);
  });
  it("supports disabled browser storage without losing access to the app", async () => {
    installFetch();
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("disabled");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("disabled");
    });
    render(<App />);
    await screen.findByText(/Осваивайся/);
    fireEvent.click(screen.getByText("Демонстрация прав доступа"));
    fireEvent.change(screen.getByLabelText("Демо-профиль"), {
      target: { value: "demo-mentor-platform" },
    });
    await waitFor(() => expect(screen.getByText("Н")).toBeVisible());
  });
});
