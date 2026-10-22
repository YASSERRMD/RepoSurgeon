// Composable for subscribing to a run's event stream over server-sent events.
// The client resumes with Last-Event-ID so a reconnect replays only the events
// it missed, with no gaps and no duplicates. A heartbeat keeps the connection
// alive, and the stream is torn down when the component unmounts.
import { ref, watch } from 'vue';

export interface SSEEvent {
  id: string;
  type: string;
  data: string;
}

export function useEventStream(runId: string | null, onEvent: (event: SSEEvent) => void) {
  let controller: AbortController | null = null;
  const lastEventId = ref('');

  function connect(): void {
    if (!runId) return;
    if (controller) controller.abort();
    controller = new AbortController();

    fetch(`/api/v1/runs/${encodeURIComponent(runId)}/events/stream`, {
      signal: controller.signal,
      headers: lastEventId.value ? { 'Last-Event-ID': lastEventId.value } : {},
    })
      .then((response) => {
        if (!response.ok) throw new Error(`stream failed: ${response.status}`);
        const reader = response.body?.getReader();
        if (!reader) throw new Error('stream has no body');
        const decoder = new TextDecoder();
        let buffer = '';

        function pump(): void {
          void reader!.read().then(({ done, value }) => {
            if (done) return;
            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split('\n');
            buffer = lines.pop() ?? '';
            for (const line of lines) {
              const trimmed = line.trim();
              if (trimmed.startsWith('id:')) {
                lastEventId.value = trimmed.slice(3).trim();
              } else if (trimmed.startsWith('data:')) {
                const payload = trimmed.slice(5).trim();
                try {
                  const parsed = JSON.parse(payload) as { type?: string };
                  onEvent({
                    id: lastEventId.value,
                    type: parsed.type ?? '',
                    data: payload,
                  });
                } catch {
                  // ignore malformed data frames
                }
              } else if (trimmed.startsWith(':') || trimmed === '') {
                // heartbeat or blank line
              }
            }
            pump();
          });
        }
        pump();
      })
      .catch((error: unknown) => {
        if (error instanceof Error && error.name === 'AbortError') return;
        setTimeout(connect, 3000);
      });
  }

  function close(): void {
    if (controller) {
      controller.abort();
      controller = null;
    }
  }

  watch(
    () => runId,
    (next) => {
      lastEventId.value = '';
      close();
      if (next) connect();
    },
    { immediate: true },
  );

  return { close };
}
