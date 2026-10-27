# LLMock

A fake LLM server for load-testing everything that sits in front of an LLM:
it speaks the OpenAI and Anthropic APIs, streaming included, and answers
with lorem ipsum at exactly the length, speed and failure rate you ask for.

LLMock does no inference. It runs on a CPU, costs nothing per token, and lets
each request say how it should be answered, so you can test LiteLLM, API
gateways, load balancers, rate limiters, retry and fallback logic and client
apps without GPUs and without paying a provider. Typical questions it
answers: does our LiteLLM deployment survive 100 (or 10,000) concurrent
streaming users? Does the gateway retry on 429? Does a proxy truncate long
streams? How much latency does LiteLLM add?

It ships with `llmock bench`, a load generator that measures time to first
token, inter-token latency and tokens/s against any OpenAI- or
Anthropic-compatible endpoint.

## Features

- **OpenAI-compatible**: chat completions, legacy completions, embeddings
  and models; **Anthropic-compatible**: messages, count_tokens and models.
  Streaming and non-streaming, accepted by the official SDKs and LiteLLM.
- **Per-request control from the prompt**: `[[mock tokens=500 tps=20]]` in
  any message, so it works through proxies that drop custom headers. Also
  an `X-Mock-Directives` header and server-side defaults and overrides.
- **Exact token counts**: ask for N tokens, get exactly N, and `usage` says N.
- **Realistic timing**: time to first token, tokens per second, jitter and
  chunking, on an absolute schedule that does not drift under load.
- **Failure injection**: HTTP errors, error rates, in-stream errors, dropped
  connections, hangs before headers and mid-stream.
- **Simulated limits**: API-key auth, a global rate limit and a concurrency
  limit, with each provider's rate-limit headers and error format.
- **Observable**: Prometheus metrics, and an `X-LLMock-Plan` header on every
  generated response with the parameters it was played with.
- **`llmock bench`**: closed-loop load generator with TTFT, ITL, tokens/s,
  an integrity check on delivered tokens and a JSON report.
- **Lightweight**: one static Go binary, an 11 MB distroless image, one
  dependency (the Prometheus client).

## Quickstart

With Docker:

```bash
docker build -t llmock .
docker run --rm -p 8080:8080 llmock
```

Or from source (Go 1.26):

```bash
go build ./cmd/llmock
./llmock serve
```

`llmock` with no subcommand also serves; `llmock version` prints the
version.

## Examples

A non-streaming chat completion with exactly 5 tokens:

```console
$ curl -s localhost:8080/v1/chat/completions -H 'Content-Type: application/json' \
    -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Hello [[mock tokens=5]]"}]}'
{"choices":[{"finish_reason":"stop","index":0,"logprobs":null,"message":{"content":"Lorem ipsum dolor sit amet,","refusal":null,"role":"assistant"}}],"created":1793139994,"id":"chatcmpl-a698f7b266467df7f43a8f63","model":"mock-gpt","object":"chat.completion","system_fingerprint":"fp_llmock","usage":{"completion_tokens":5,"prompt_tokens":6,"total_tokens":11}}
```

A stream of 50 tokens at 10 tokens/s (5 seconds):

```console
$ curl -sN localhost:8080/v1/chat/completions -H 'Content-Type: application/json' \
    -d '{"model":"mock-gpt","stream":true,"stream_options":{"include_usage":true},
         "messages":[{"role":"user","content":"Hello [[mock tokens=50 tps=10]]"}]}'
data: {"id":"chatcmpl-8eff…","object":"chat.completion.chunk",…,"choices":[{"index":0,"delta":{"role":"assistant","content":""},…}]}

data: {"id":"chatcmpl-8eff…","object":"chat.completion.chunk",…,"choices":[{"index":0,"delta":{"content":"Lorem"},…}]}

data: {"id":"chatcmpl-8eff…","object":"chat.completion.chunk",…,"choices":[{"index":0,"delta":{"content":" ipsum"},…}]}
…
data: {"id":"chatcmpl-8eff…","object":"chat.completion.chunk",…,"choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}]}

data: {"id":"chatcmpl-8eff…","object":"chat.completion.chunk",…,"choices":[],"usage":{"completion_tokens":50,"prompt_tokens":8,"total_tokens":58}}

data: [DONE]
```

An Anthropic stream, 3 tokens after a 200 ms time to first token:

```console
$ curl -sN localhost:8080/v1/messages -H 'Content-Type: application/json' -H 'anthropic-version: 2023-06-01' \
    -d '{"model":"mock-claude","max_tokens":1024,"stream":true,
         "messages":[{"role":"user","content":"Hello [[mock tokens=3 delay=200ms]]"}]}'
event: message_start
data: {"type":"message_start","message":{"id":"msg_66c3…","type":"message","role":"assistant","model":"mock-claude","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":9,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: ping
data: {"type":"ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Lorem"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" ipsum"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" dolor"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":3}}

event: message_stop
data: {"type":"message_stop"}
```

Directives in a header instead of the prompt, with the effective plan echoed
back in `X-LLMock-Plan`:

```console
$ curl -si localhost:8080/v1/chat/completions -H 'Content-Type: application/json' \
    -H 'X-Mock-Directives: tokens=3 delay=500ms' \
    -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Hello"}]}'
HTTP/1.1 200 OK
Content-Type: application/json
X-Llmock-Plan: tokens=3 prompt_tokens=2 delay=500ms tps=0
…
```

The [SDK smoke test](examples/sdk/smoke.py) calls every endpoint with the
official OpenAI and Anthropic Python SDKs:

```bash
uv run --with openai --with anthropic examples/sdk/smoke.py \
  --openai-base http://localhost:8080/v1 --anthropic-base http://localhost:8080
```

## Directives

A `[[mock key=value ...]]` block anywhere in the system prompt or the last
user message controls the response. Unknown keys and bad values are a 400,
never silently ignored. Full reference, precedence and timing:
[docs/directives.md](docs/directives.md).

| Key | Example | Effect |
|-----|---------|--------|
| `tokens` | `tokens=500`, `tokens=200-400` | completion length (exact, or a uniform range) |
| `prompt_tokens` | `prompt_tokens=8000` | reported prompt tokens |
| `delay` | `delay=2s` | time to first token |
| `tps` | `tps=20` | tokens per second (`0` = unlimited) |
| `jitter` | `jitter=0.2` | random variation of the token timing |
| `chunk` | `chunk=5` | tokens per stream frame |
| `error` | `error=429` | respond with this HTTP error |
| `error_rate` | `error_rate=0.05` | fail this fraction of requests |
| `error_after` | `error_after=20` | in-stream error after N tokens |
| `fail_after` | `fail_after=20` | drop the connection after N tokens |
| `hang` | `hang` | never send headers |
| `hang_after` | `hang_after=20` | go silent after N tokens |
| `finish_reason` | `finish_reason=length` | force `stop`, `length` or `content_filter` |
| `seed` | `seed=42` | deterministic text and ranges |

Layers, lowest to highest: `--defaults` < `X-Mock-Directives` header <
prompt < `--overrides`. `max_tokens` caps the length but never sets it.

## Configuration

Every setting is a flag of `llmock serve` with an environment variable
equivalent; flags win over environment variables.

| Flag | Environment | Default | Meaning |
|------|-------------|---------|---------|
| `--addr` | `LLMOCK_ADDR` | `:8080` | listen address |
| `--defaults` | `LLMOCK_DEFAULTS` | `tokens=100` | default directives (lowest layer) |
| `--overrides` | `LLMOCK_OVERRIDES` | empty | override directives (highest layer), e.g. `error_rate=0.05` |
| `--api-keys` | `LLMOCK_API_KEYS` | empty | comma-separated accepted keys (`Authorization: Bearer` or `x-api-key`), else 401; empty accepts anything. `/health` and `/metrics` need no key |
| `--models` | `LLMOCK_MODELS` | `mock-gpt,mock-claude` | names listed by `/v1/models` (any model name is accepted in requests) |
| `--rate-limit-rps` | `LLMOCK_RATE_LIMIT_RPS` | `0` | global rate limit in requests/s, `0` = off; over it, 429 with `Retry-After` |
| `--max-concurrency` | `LLMOCK_MAX_CONCURRENCY` | `0` | max requests in flight, `0` = off; over it, 429 |
| `--max-body` | `LLMOCK_MAX_BODY` | `10485760` | max request body in bytes; over it, 413 |
| `--access-log` | `LLMOCK_ACCESS_LOG` | `false` | log every request |
| `--log-level` | `LLMOCK_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `--shutdown-timeout` | `LLMOCK_SHUTDOWN_TIMEOUT` | `30s` | on SIGTERM/SIGINT, how long in-flight requests may finish |

With Docker, pass environment variables:
`docker run --rm -p 8080:8080 -e LLMOCK_DEFAULTS="tokens=200 tps=50" llmock`.

Endpoints, each also served without the `/v1` prefix:

| Method and path | API |
|-----------------|-----|
| `POST /v1/chat/completions` | OpenAI chat, stream and non-stream |
| `POST /v1/completions` | OpenAI legacy completions, stream and non-stream |
| `POST /v1/embeddings` | OpenAI embeddings: deterministic unit vectors, `dimensions` (default 1536), `float` or `base64` |
| `POST /v1/messages` | Anthropic messages, stream and non-stream |
| `POST /v1/messages/count_tokens` | Anthropic token count |
| `GET /v1/models` | OpenAI format, or Anthropic format when `anthropic-version` is sent |
| `GET /health` | liveness, `ok` |
| `GET /metrics` | Prometheus metrics |

## LiteLLM

[`examples/litellm`](examples/litellm) runs LLMock behind LiteLLM with
Docker Compose. LiteLLM routes `mock-gpt` (OpenAI), `mock-claude`
(Anthropic) and `mock-embed` (OpenAI embeddings) to the mock; its master key
is `sk-llmock`. From the repository root:

```bash
docker compose -f examples/litellm/docker-compose.yml up -d --build
# LiteLLM is ready when this prints "I'm alive!" (about 10 s after start).
curl -s localhost:4000/health/liveliness

# Check that the official SDKs work through LiteLLM.
uv run --with openai --with anthropic examples/sdk/smoke.py --via-litellm --key sk-llmock \
  --openai-base http://localhost:4000/v1 --anthropic-base http://localhost:4000

# Load test: 50 streaming users, 100 tokens each at 50 tokens/s.
./llmock bench --url http://localhost:4000 --api-key sk-llmock \
  --users 50 --duration 15s --stream --directives "tokens=100 tps=50"

docker compose -f examples/litellm/docker-compose.yml down
```

LiteLLM needs no special setting to talk to the mock. Notes:

- LiteLLM drops the `X-Mock-Directives` header; use `[[mock ...]]` in the
  prompt, which it passes through unchanged. To forward the header anyway,
  set `general_settings: {forward_client_headers_to_llm_api: true}` in
  `config.yaml`.
- LiteLLM returns the mock's response headers with an `llm_provider-` prefix,
  so `llm_provider-x-llmock-plan` shows the plan behind each answer.
- To measure the overhead LiteLLM adds, compare a direct run with a run
  through LiteLLM: see [docs/bench.md](docs/bench.md#recipes).

## Load testing with `llmock bench`

```console
$ ./llmock bench --url http://localhost:4000 --api-key sk-llmock \
    --users 50 --duration 15s --stream --directives "tokens=100 tps=50"
…
50 users, 15s, stream, openai http://localhost:4000
Requests:     305 (305 ok, 0 failed)
Throughput:   19.3 req/s, 1927.5 output tok/s
Latency (ms): p50 2536.7  p90 2830.2  p95 2945.1  p99 3189.1  max 3229.1
TTFT (ms):    p50 394.6  p90 501.1  p95 521.4  p99 593.4  max 608.9
ITL (ms):     p50 19.5  p90 24.1  p95 27.4  p99 50.8  max 775.7
Tokens/req:   mean 100.0  min 100  max 100
Integrity:    305 checked, 0 mismatches
```

N virtual users send requests back to back for `--duration` or until
`--requests` are done. `--api anthropic` targets `/v1/messages`;
`--directives` is appended to the prompt as `[[mock ...]]`, and when it fixes
`tokens=N`, bench checks that every response delivered exactly N tokens.
`--out report.json` saves the report. Every flag, the metric definitions and
recipes: [docs/bench.md](docs/bench.md).

## Metrics

`GET /metrics` serves Prometheus text format:

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `llmock_requests_total` | counter | `route`, `status` | requests served; `status` is the HTTP status, or `aborted` for dropped and hung connections |
| `llmock_request_duration_seconds` | histogram | `route` | request duration |
| `llmock_in_flight_requests` | gauge | | requests being served |
| `llmock_ttft_seconds` | histogram | | time to the first content token (non-streaming: to the body) |
| `llmock_tokens_total` | counter | | completion tokens sent |
| `llmock_injected_total` | counter | `kind` | injected failures: `error`, `abort`, `hang` |

`route` is the route pattern, e.g. `POST /v1/chat/completions`. The standard
Go runtime and process metrics are exported too.

## Performance

Targets: 10,000 concurrent streams at `tokens=500 tps=20` (about 200,000 SSE
writes/s) with each stream's delivered rate within 5% of `tps` and median
time to first token (TTFT) under 10 ms; 20,000 non-streaming requests/s at
`tokens=100`; an image under 20 MB; process startup under 100 ms. The runs
in the table meet all of them; on this shared laptop the per-stream rate
held in two of four 10,000-stream runs (see the note on variance).

**Setup.** An Apple M1 Pro laptop (8 cores: 6 performance, 2 efficiency;
16 GB) running Docker in OrbStack with all 8 cores and 8 GB. LLMock runs in
a container limited to 4 CPUs and `llmock bench` in a second container with
the other 4, on the same Docker network (no port mapping):

```bash
docker build -t llmock . && docker network create llmock-perf
docker run -d --rm --name llmock --network llmock-perf --cpus=4 \
  --ulimit nofile=200000:200000 llmock serve
docker run --rm --network llmock-perf --cpus=4 --ulimit nofile=200000:200000 \
  llmock bench --url http://llmock:8080 --users 10000 --ramp 20s \
  --duration 90s --stream --directives "tokens=500 tps=20" --timeout 120s
docker run --rm --network llmock-perf --cpus=4 \
  llmock bench --url http://llmock:8080 --users 256 --duration 30s \
  --directives "tokens=100"
```

| Scenario | Requests | Throughput (run average) | TTFT p50 / p99 | ITL p50 / p99 | LLMock CPU, steady |
|----------|----------|--------------------------|----------------|---------------|--------------------|
| Stream, 10,000 users, `tokens=500 tps=20` | 37,541, 0 failed | 163k tok/s | 1.4 / 40 ms | 49.8 / 97 ms | 2.2–2.8 of 4 |
| Non-stream, 256 users, `tokens=100` | 1,673,992, 0 failed | 55,794 req/s | — | — | 2.6–2.8 of 4 |
| Stream, 5,000 users, `tokens=500 tps=50` | 42,189, 0 failed | 211k tok/s | 3.0 / 357 ms | 19.5 / 72 ms | 2.6–3.8 of 4 |
| Stream, 5,000 users, `tokens=500 tps=100` | 79,669, 0 failed | 418k tok/s | 73 / 769 ms | see below | 3.2–4 of 4 |

Run averages include ramp-up and drain; with all 10,000 streams open the
mock delivers about 200k tok/s. Every response carried exactly 500 (or 100)
tokens. In the 10,000-stream run shown, the slowest stream took 25.25 s
against the ideal 24.95 s, so every stream's delivered rate was within 1.2%
of `tps`. Non-streaming latency was p50 3.4 ms, p99 19 ms.

The `tps=50` and `tps=100` rows are published but not targets. At `tps=100`
both containers are close to their CPU limits, the client often reads
several frames in one socket read, and ITL is reported as ~0 ms (p50); the
end-to-end rate is better read from stream duration: p50 about 5.1 s for
500 tokens against the ideal 4.99 s.

The laptop's 8 cores are shared with the desktop, so tails vary between
runs. Across four runs of the 10,000-stream scenario, none failed and the
medians stayed within TTFT 1.4–3.4 ms and ITL 48.8–49.8 ms. Two runs were
clean (slowest stream 25.25 s and 25.37 s, TTFT p99 40 ms and 102 ms). The
other two missed the per-stream target in the tail: p95 stream duration
stayed within 3.3% of ideal, but p99 was 6% and 19% over it, and TTFT p99
reached 0.6 s and 1.2 s. In one of those runs LLMock's own
`llmock_ttft_seconds` put 99.6% of first tokens under 1 ms, and in a clean
run a light 20-user client beside the full load saw TTFT p99 33 ms against
102 ms for the 10,000-user client. Both point to CPU contention on the
shared host and in the load generator rather than in LLMock's request
handling.

**Image and startup.** The image is 11.4 MB. The native binary answers its
first `/health` request 7 ms after launch (median of 10 launches on the same
laptop; on macOS the first launch of a freshly built binary is slower,
~0.5 s). `docker run` to the first healthy response through a mapped
port takes 150–200 ms, mostly container creation.

## Architecture

A provider-neutral **engine** decides *what* happens; thin **provider
adapters** decide *how it looks on the wire*.

```
HTTP request
   │
   ▼
server: auth, rate limit, concurrency limit, metrics      (internal/server)
   │
   ▼
provider adapter (openai | anthropic)
   │  decode body → engine.Request {Header, Scope, Input, MaxTokens, Seed}
   ▼
directive parser → merged directives (defaults < header < prompt < overrides)
   │
   ▼
engine.Plan {Tokens, PromptTokens, Delay, TPS, Jitter, Chunk, ErrorCode,
             ErrorAfter, FailAfter, HangAfter, Hang, FinishReason, Seed}
   │
   ▼
engine.Env.Respond(ctx, w, plan, encoder, stream) → paced tokens, then
             finish | error | abort | hang
   │
   ▼
provider encoder (engine.Encoder / StreamEncoder) → JSON body or SSE frames
```

| Package | Role |
|---------|------|
| `cmd/llmock` | CLI: `serve`, `bench`, `version` |
| `internal/server` | routes, auth, rate and concurrency limits, metrics middleware |
| `internal/engine` | directives, plans, lorem ipsum tokens, the paced response driver |
| `internal/openai`, `internal/anthropic` | provider adapters |
| `internal/metrics` | Prometheus collectors |
| `internal/bench` | the load generator |

A new API format is one new adapter package plus its routes:
[docs/adding-a-provider.md](docs/adding-a-provider.md).
