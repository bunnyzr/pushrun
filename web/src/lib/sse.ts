import { API_BASE, notifyUnauthorized, parseErrorBody } from "./api";
import { tokenStore } from "./auth";

export interface SSEEvent {
  event: string;
  data: string[];
  comment?: string;
}

export interface StreamSSEOptions {
  signal: AbortSignal;
  onEvent(ev: SSEEvent): void;
}

/**
 * Streams server-sent events from an API-relative path (e.g.
 * `"/runs/{id}/output"`), prefixed with API_BASE exactly like apiFetch.
 * Resolves when the stream ends; rejects with ApiError on HTTP errors and
 * with an AbortError when the signal aborts.
 */
export async function streamSSE(path: string, opts: StreamSSEOptions): Promise<void> {
  const headers = new Headers({ Accept: "text/event-stream" });
  const token = tokenStore.get();
  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }
  const res = await fetch(`${API_BASE}${path}`, { headers, signal: opts.signal });
  if (res.status === 401) {
    notifyUnauthorized();
    throw await parseErrorBody(res);
  }
  if (!res.ok) {
    throw await parseErrorBody(res);
  }
  if (!res.body) {
    throw new Error("SSE response has no body");
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let event = "";
  let data: string[] = [];
  let comment: string | undefined;

  const dispatch = () => {
    if (data.length > 0 || comment !== undefined) {
      const ev: SSEEvent = { event, data, ...(comment !== undefined ? { comment } : {}) };
      opts.onEvent(ev);
    }
    event = "";
    data = [];
    comment = undefined;
  };

  const handleLine = (line: string) => {
    if (line.endsWith("\r")) {
      line = line.slice(0, -1);
    }
    if (line === "") {
      dispatch();
      return;
    }
    if (line.startsWith(":")) {
      comment = line.slice(1).trimStart();
      return;
    }
    const colon = line.indexOf(":");
    const field = colon === -1 ? line : line.slice(0, colon);
    let value = colon === -1 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) {
      value = value.slice(1);
    }
    if (field === "event") {
      event = value;
    } else if (field === "data") {
      data.push(value);
    }
  };

  const pump = async () => {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let nl: number;
      while ((nl = buffer.indexOf("\n")) !== -1) {
        handleLine(buffer.slice(0, nl));
        buffer = buffer.slice(nl + 1);
      }
    }
    buffer += decoder.decode();
    if (buffer !== "") {
      handleLine(buffer);
    }
    dispatch();
  };

  // fetch rejects on abort in the browser, but do not rely on the transport
  // erroring an in-flight body read: race the pump against the signal.
  const aborted = new Promise<never>((_, reject) => {
    const onAbort = () => reject(new DOMException("The operation was aborted.", "AbortError"));
    if (opts.signal.aborted) {
      onAbort();
    } else {
      opts.signal.addEventListener("abort", onAbort, { once: true });
    }
  });

  try {
    await Promise.race([pump(), aborted]);
  } finally {
    // Fire-and-forget: awaiting cancel can hang when the aborted stream's
    // source never closes, and the result does not matter here.
    void reader.cancel().catch(() => {});
  }
}
