import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Api, ApiError, type AccessRequest } from "../src/api";
import { Requests } from "../src/Requests";
import { actor, request } from "./fixtures";

function setup(
  items: AccessRequest[] = [],
  options: {
    plan?: boolean;
    proposal?: boolean;
    role?: "intern" | "mentor";
    team?: string;
  } = {},
) {
  const api = new Api("test");
  vi.spyOn(api, "list").mockResolvedValue({ items });
  vi.spyOn(api, "progress").mockResolvedValue({
    items: items.map((r) => ({
      request_id: r.id,
      resource: r.resource,
      state: "todo",
      next_step: "confirm_proposal",
    })),
  });
  vi.spyOn(api, "propose").mockResolvedValue(request);
  vi.spyOn(api, "confirm").mockResolvedValue({
    ...request,
    state: "submitted",
    ticket: { id: "BUD-ACCESS-1", status: "pending" },
    version: 2,
  });
  vi.spyOn(api, "get").mockResolvedValue(request);
  vi.spyOn(api, "cancel").mockResolvedValue({
    ...request,
    state: "cancelled",
    version: 2,
  });
  vi.spyOn(api, "refresh").mockResolvedValue({
    ...request,
    state: "submitted",
    ticket: { id: "BUD-ACCESS-1", status: "granted" },
    version: 3,
  });
  const onConsumed = vi.fn();
  const view = render(
    <Requests
      api={api}
      actor={{
        ...actor,
        role: options.role ?? "intern",
        team: options.team ?? "platform",
      }}
      plan={options.plan ?? false}
      initialProposal={options.proposal ?? false}
      onConsumed={onConsumed}
    />,
  );
  return { api, onConsumed, user: userEvent.setup(), ...view };
}

describe("saved access requests and progress", () => {
  it("previews exact proposal, requires explicit confirmation and shows server ticket/status", async () => {
    const { api, user } = setup([], { proposal: true });
    await screen.findByText(/Здесь начнётся/);
    expect(api.confirm).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Проверить заявку" }));
    const confirm = await screen.findByRole("button", {
      name: "Подтвердить и отправить",
    });
    expect(confirm).toBeDisabled();
    expect(api.propose).toHaveBeenCalledWith(
      {
        resource: "jira:BUD",
        access: "read",
        reason: "Посмотреть первую задачу стажировки",
      },
      expect.any(String),
    );
    await user.click(screen.getByRole("checkbox"));
    await user.click(confirm);
    await screen.findByText("BUD-ACCESS-1");
    expect(api.confirm).toHaveBeenCalledWith(request);
    expect(screen.getByText(/Доступ ещё не выдан/)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Проверить статус" }));
    expect(await screen.findByText(/Доступ получен/)).toBeVisible();
    expect(api.refresh).toHaveBeenCalledWith(request.id);
    await user.click(screen.getByRole("button", { name: "Обновить данные" }));
    expect(api.get).toHaveBeenCalledWith(request.id);
  });
  it("opens persisted requests, displays next steps, cancels with current version and reloads", async () => {
    const { api, user } = setup([request], { plan: true });
    expect(screen.getByText("Найти инструкцию")).toBeVisible();
    await user.click(await screen.findByRole("button", { name: /jira:BUD/ }));
    await user.click(screen.getByRole("button", { name: "Отменить заявку" }));
    expect(api.cancel).toHaveBeenCalledWith(request);
    expect(await screen.findByText(/Заявка отменена/)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Обновить" }));
    await waitFor(() => expect(api.list).toHaveBeenCalledTimes(3));
  });
  it("retains proposal idempotency key and immutable payload after an ambiguous network failure", async () => {
    const { api, user } = setup([], { proposal: true });
    vi.mocked(api.propose).mockRejectedValueOnce(new ApiError("network_error"));
    await user.click(screen.getByRole("button", { name: "Проверить заявку" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Нет связи");
    expect(screen.getByLabelText("Обоснование")).toBeDisabled();
    await user.click(
      screen.getByRole("button", { name: "Повторить предпросмотр" }),
    );
    await screen.findByRole("button", { name: "Подтвердить и отправить" });
    expect(vi.mocked(api.propose).mock.calls[0]).toEqual(
      vi.mocked(api.propose).mock.calls[1],
    );
  });
  it("validates UTF-8 bytes and allows policy refusal for unsupported permission", async () => {
    const { api, user } = setup([], { proposal: true });
    fireEvent.change(screen.getByLabelText("Обоснование"), {
      target: { value: "я".repeat(501) },
    });
    await user.click(screen.getByRole("button", { name: "Проверить заявку" }));
    expect(screen.getByRole("alert")).toHaveTextContent("1000 байт");
    expect(api.propose).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText("Обоснование"), {
      target: { value: "Нужен доступ" },
    });
    await user.selectOptions(screen.getByLabelText("Уровень доступа"), "write");
    vi.mocked(api.propose).mockRejectedValue(new ApiError("forbidden"));
    await user.click(screen.getByRole("button", { name: "Проверить заявку" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "недоступна вашему профилю",
    );
  });
  it("handles loading failure with retry and hides proposals for unauthorized profiles", async () => {
    const { api, user } = setup([], { role: "mentor" });
    await screen.findByText(/Для этого профиля/);
    expect(
      screen.queryByRole("button", { name: "Подготовить заявку" }),
    ).not.toBeInTheDocument();
    vi.mocked(api.list).mockRejectedValueOnce(
      new ApiError("temporarily_unavailable"),
    );
    await user.click(screen.getByRole("button", { name: "Обновить" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "временно недоступен",
    );
    await user.click(screen.getByRole("button", { name: "Обновить" }));
    await waitFor(() =>
      expect(screen.queryByRole("alert")).not.toBeInTheDocument(),
    );
  });
  it.each(["unknown", "submitting", "cancelled"] as const)(
    "shows saved %s state without automatic resubmission",
    async (state) => {
      const { api, user } = setup([{ ...request, state }]);
      await user.click(await screen.findByRole("button", { name: /jira:BUD/ }));
      expect(api.confirm).not.toHaveBeenCalled();
      if (state !== "cancelled")
        expect(
          screen.getByText(/Результат отправки пока неизвестен/),
        ).toBeVisible();
      else expect(screen.getByText(/Заявка отменена/)).toBeVisible();
    },
  );
  it("shows rejection and lets user close/reopen proposal without a submit", async () => {
    const { user } = setup([
      {
        ...request,
        state: "submitted",
        ticket: { id: "BUD-2", status: "rejected" },
      },
    ]);
    await user.click(await screen.findByRole("button", { name: /jira:BUD/ }));
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Обсудите причину отказа",
    );
    await user.click(
      screen.getByRole("button", { name: "Подготовить заявку" }),
    );
    await user.click(screen.getByRole("button", { name: "Закрыть форму" }));
    expect(screen.queryByLabelText("Обоснование")).not.toBeInTheDocument();
  });
  it("reports version conflict and prevents repeat send while an action is in flight", async () => {
    const { api, user } = setup([request]);
    await user.click(await screen.findByRole("button", { name: /jira:BUD/ }));
    vi.mocked(api.confirm).mockRejectedValueOnce(new ApiError("conflict"));
    await user.click(screen.getByRole("checkbox"));
    await user.click(
      screen.getByRole("button", { name: "Подтвердить и отправить" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "уже изменилась",
    );
    vi.mocked(api.confirm).mockImplementation(() => new Promise(() => {}));
    await user.click(
      screen.getByRole("button", { name: "Подтвердить и отправить" }),
    );
    expect(screen.getByRole("button", { name: "Отправляем…" })).toBeDisabled();
  });
});
