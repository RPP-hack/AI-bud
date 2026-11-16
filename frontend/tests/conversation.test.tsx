import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Api, ApiError, type ConversationTurn } from "../src/api";
import { Assistant } from "../src/Assistant";
import { actor, answer } from "./fixtures";

const turn: ConversationTurn = {
  id: "turn-1",
  client_id: "client-1",
  question: "Мой сохранённый вопрос",
  mode: "llm",
  top_k: 3,
  state: "completed",
  answer,
  created_at: "2026-11-16T00:00:00Z",
};
const chat = { id: "chat-1", turns: [turn] };

describe("persistent conversation", () => {
  it("restores server answers and sources after unmount, and resets on the server", async () => {
    const api = new Api("token");
    vi.spyOn(api, "conversation").mockResolvedValue(chat);
    vi.spyOn(api, "newConversation").mockResolvedValue({
      id: "chat-2",
      turns: [],
    });
    const user = userEvent.setup();
    const first = render(
      <Assistant api={api} actor={actor} search={false} onProposal={vi.fn()} />,
    );
    expect(await screen.findByText(turn.question)).toBeVisible();
    first.unmount();
    render(
      <Assistant api={api} actor={actor} search={false} onProposal={vi.fn()} />,
    );
    expect(await screen.findByText(turn.question)).toBeVisible();
    expect(screen.getByRole("button", { name: "[S1]" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Новый диалог" }));
    expect(api.newConversation).toHaveBeenCalledWith("chat-1");
    await waitFor(() =>
      expect(screen.queryByText(turn.question)).not.toBeInTheDocument(),
    );
  });

  it("recovers a pending answer without issuing another generation request", async () => {
    const api = new Api("token");
    vi.spyOn(api, "conversation")
      .mockResolvedValueOnce({
        ...chat,
        turns: [{ ...turn, state: "pending", answer: undefined }],
      })
      .mockResolvedValue(chat);
    const send = vi.spyOn(api, "sendMessage");
    render(
      <Assistant api={api} actor={actor} search={false} onProposal={vi.fn()} />,
    );
    expect(await screen.findByText(/Ответ ещё готовится/)).toBeVisible();
    expect(screen.getByRole("button", { name: "Новый диалог" })).toBeDisabled();
    await waitFor(
      () => expect(screen.getByRole("button", { name: "[S1]" })).toBeVisible(),
      { timeout: 3000 },
    );
    expect(send).not.toHaveBeenCalled();
  });

  it("uses the same idempotency key when a transport failure leaves submission uncertain", async () => {
    const api = new Api("token");
    vi.spyOn(api, "conversation").mockResolvedValue({ ...chat, turns: [] });
    const send = vi
      .spyOn(api, "sendMessage")
      .mockRejectedValueOnce(new ApiError("network_error"))
      .mockResolvedValue(turn);
    const user = userEvent.setup();
    render(
      <Assistant api={api} actor={actor} search={false} onProposal={vi.fn()} />,
    );
    await user.type(screen.getByRole("textbox"), "Jira 403{Enter}");
    await user.click(
      await screen.findByRole("button", { name: "Повторить запрос" }),
    );
    await waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    expect(send.mock.calls[0].slice(0, 5)).toEqual(
      send.mock.calls[1].slice(0, 5),
    );
    expect(send.mock.calls[0][1]).toBeTruthy();
  });

  it("does not clear messages when resetting fails and can reload an unavailable history", async () => {
    const api = new Api("token");
    vi.spyOn(api, "conversation")
      .mockRejectedValueOnce(new ApiError("network_error"))
      .mockResolvedValue(chat);
    vi.spyOn(api, "newConversation").mockRejectedValue(
      new ApiError("conflict"),
    );
    const user = userEvent.setup();
    render(
      <Assistant api={api} actor={actor} search={false} onProposal={vi.fn()} />,
    );
    await user.click(
      await screen.findByRole("button", { name: "Обновить переписку" }),
    );
    await screen.findByText(turn.question);
    await user.click(screen.getByRole("button", { name: "Новый диалог" }));
    await screen.findByRole("alert");
    expect(screen.getByText(turn.question)).toBeVisible();
  });
});
