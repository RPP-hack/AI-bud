import { describe, expect, it, vi } from "vitest";
import { Api, ApiError, errorText } from "../src/api";
import { request } from "./fixtures";

describe("explicit same-origin API client", () => {
  it("keeps the browser receiver when using the native fetch default", async () => {
    const browserFetch = vi.fn(function (this: unknown) {
      if (this !== globalThis) throw new TypeError("Illegal invocation");
      return Promise.resolve(
        new Response(
          JSON.stringify({
            id: "intern-platform",
            role: "intern",
            team: "platform",
            status: "ready",
          }),
        ),
      );
    });
    vi.stubGlobal("fetch", browserFetch);
    try {
      const api = new Api("demo-intern-platform");
      expect(await api.me()).toMatchObject({ id: "intern-platform" });
      expect(await api.health()).toMatchObject({ status: "ready" });
      expect(await api.ready()).toMatchObject({ status: "ready" });
      expect(browserFetch).toHaveBeenCalledTimes(3);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("uses every public REST operation with exact methods, versions and delegated auth", async () => {
    // A fresh Response is needed for each call, as its body can only be read once.
    const fetcher = vi
      .fn<typeof fetch>()
      .mockImplementation(async () => new Response("{}", { status: 200 }));
    const api = new Api("demo-intern-platform", fetcher);
    await api.me();
    await api.answer("Jira 403", "llm", 3);
    await api.search("Jira", 1);
    await api.list();
    await api.get("a/b");
    await api.propose(
      { resource: "jira:BUD", access: "read", reason: "Task" },
      "stable-key",
    );
    await api.confirm(request);
    await api.cancel(request);
    await api.refresh(request.id);
    await api.progress();
    await api.health();
    await api.ready();
    expect(
      fetcher.mock.calls.map(([url, options]) => [url, options?.method]),
    ).toEqual([
      ["/api/me", "GET"],
      ["/api/rag/answers", "POST"],
      ["/api/rag/search", "POST"],
      ["/api/access-requests", "GET"],
      ["/api/access-requests/a%2Fb", "GET"],
      ["/api/access-requests", "POST"],
      ["/api/access-requests/request-1/confirm", "POST"],
      ["/api/access-requests/request-1/cancel", "POST"],
      ["/api/access-requests/request-1/refresh", "POST"],
      ["/api/onboarding", "GET"],
      ["/health", "GET"],
      ["/ready", "GET"],
    ]);
    expect(fetcher.mock.calls[1][1]?.body).toBe(
      JSON.stringify({ question: "Jira 403", mode: "llm", top_k: 3 }),
    );
    expect(fetcher.mock.calls[2][1]?.body).toBe(
      JSON.stringify({ query: "Jira", top_k: 1 }),
    );
    expect(
      new Headers(fetcher.mock.calls[5][1]?.headers).get("Idempotency-Key"),
    ).toBe("stable-key");
    expect(fetcher.mock.calls[6][1]?.body).toBe('{"version":1}');
    expect(fetcher.mock.calls[8][1]?.body).toBeUndefined();
    for (const [, options] of fetcher.mock.calls) {
      expect(new Headers(options?.headers).get("Authorization")).toBe(
        "Bearer demo-intern-platform",
      );
      expect(options?.cache).toBe("no-store");
    }
  });
  it("accepts 202 unknown state and maps sanitized HTTP, network and protocol failures", async () => {
    const fetcher = vi.fn();
    const api = new Api("token", fetcher);
    fetcher.mockResolvedValue(
      new Response(JSON.stringify({ ...request, state: "unknown" }), {
        status: 202,
      }),
    );
    expect((await api.refresh("id")).state).toBe("unknown");
    fetcher.mockResolvedValue(
      new Response('{"error":{"code":"conflict"}}', { status: 409 }),
    );
    await expect(api.confirm(request)).rejects.toMatchObject({
      code: "conflict",
      status: 409,
    });
    fetcher.mockResolvedValue(new Response("{}", { status: 500 }));
    await expect(api.list()).rejects.toMatchObject({ code: "internal_error" });
    fetcher.mockResolvedValue(
      new Response("<html lang='en'>bad gateway</html>", { status: 502 }),
    );
    await expect(api.list()).rejects.toMatchObject({
      code: "temporarily_unavailable",
    });
    fetcher.mockResolvedValue(new Response("null"));
    await expect(api.list()).rejects.toMatchObject({
      code: "invalid_response",
    });
    fetcher.mockRejectedValue(new Error("private network details"));
    await expect(api.list()).rejects.toMatchObject({ code: "network_error" });
    fetcher.mockRejectedValue(new DOMException("aborted", "AbortError"));
    await expect(api.list()).rejects.toMatchObject({
      code: "request_cancelled",
    });
    expect(errorText(new ApiError("conflict"))).toMatch("Обновите");
    expect(errorText(new ApiError("unknown-code"))).toMatch("Не удалось");
    expect(errorText(new Error("secret"))).not.toMatch("secret");
  });
});
