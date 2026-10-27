# `llmock bench`

`llmock bench` is a load generator for any OpenAI- or Anthropic-compatible
endpoint: LLMock itself, LiteLLM, a gateway, or a real provider. It measures
what matters for LLM traffic: time to first token, inter-token latency and
output tokens per second, and it checks that every response delivered the
tokens it promised.

```bash
llmock bench --url http://localhost:4000 --api-key sk-llmock \
  --users 100 --duration 60s --ramp 10s --stream --directives "tokens=500 tps=20"
```

The examples call `llmock` on your `PATH`; in a source checkout,
`go build ./cmd/llmock` builds `./llmock`, or use `go run ./cmd/llmock bench`.

## Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `--url` | required | Base URL (`http://` or `https://`), with or without `/v1`. Bench posts to `/v1/chat/completions` or `/v1/messages`. |
| `--api` | `openai` | API flavour: `openai` or `anthropic`. |
| `--model` | `mock-gpt` | Model name sent in every request. |
| `--api-key` | empty | Sent as `Authorization: Bearer` (OpenAI) or `x-api-key` (Anthropic). Never written to the report. |
| `--users` | `10` | Concurrent virtual users. |
| `--duration` | | Run time. Exactly one of `--duration` and `--requests` is required. |
| `--requests` | | Total requests to send. |
| `--ramp` | `0` | Linear ramp-up: user `u` starts at `u × ramp / users`. |
| `--stream` | `false` | Stream responses (SSE). |
| `--directives` | empty | Mock directives appended to the prompt as ` [[mock ...]]`, e.g. `"tokens=500 tps=20"`. Validated before the run starts. |
| `--prompt` | `Hello` | User message text. |
| `--max-tokens` | `0` | `max_tokens` to send; `0` omits it (Anthropic requires one, so bench then sends `4096`). |
| `--seed` | unset | Base seed: request `i` adds `seed=<seed+i>` to the directives, so a run is reproducible. |
| `--timeout` | `120s` | Per-request timeout; `0` disables it. |
| `--out` | empty | Also write the report as JSON to this file. |

Every request has one user message, `--prompt` plus the directive block.
OpenAI streams ask for `stream_options.include_usage`; Anthropic requests
send `anthropic-version: 2023-06-01`.

## Load model

Bench is **closed-loop**: each virtual user sends a request, waits for the
complete response, and immediately sends the next. Throughput is therefore
an outcome, not an input: a slower system under test gets fewer requests.

- With `--requests`, the run ends when that many requests have been sent
  and answered.
- With `--duration`, no request starts after the deadline; requests in
  flight complete, so the elapsed time can exceed `--duration` by up to one
  response time.
- `Ctrl-C` or SIGTERM (e.g. `docker stop`) ends the run early: requests in
  flight are dropped and the report covers the completed ones.

While running, bench prints one progress line per second to stderr
(`[  12s] users=50 rps=48 ok=255 failed=0`); the summary goes to stdout.
Bench exits 0 after a run even if requests failed (read the summary or the
JSON report), 2 on a flag error and 1 on an invalid configuration.

## Metrics

- **Latency**: from sending the request to the end of the response (the end
  of the body, or the end of the stream).
- **TTFT** (streams only): from sending the request to the first **content**
  frame: a chat delta with non-empty content or an Anthropic
  `content_block_delta`. Role chunks, `message_start` and `ping` do not
  count, so TTFT measures what a user waits for, not the first byte.
- **ITL** (streams only): the gaps between consecutive content frames of a
  response, taken as bench reads each frame. With `chunk=N` this is the time
  between chunks, not tokens. Frames that arrive in the same network read
  show a gap near 0, which is common at high `tps`.
- **Throughput**: requests per second and output tokens per second, over the
  whole elapsed time.
- **Tokens/req**: the response's `usage` (completion / output tokens), or
  the number of content frames when the response reports no usage.
- **Errors**: by HTTP status for non-2xx responses, and by kind:

  | Kind | Meaning |
  |------|---------|
  | `http` | non-2xx status (broken down by status) |
  | `connect` | the request could not be sent or no response arrived |
  | `timeout` | `--timeout` expired |
  | `truncated` | the body or stream ended early: no `[DONE]` / `message_stop`, or an unreadable body |
  | `stream_error` | an in-stream error frame |

  Only successful responses feed the latency, TTFT, ITL and token figures.

Percentiles come from a fixed-memory log histogram with about 1% bucket
width; each reported percentile is the lower bound of its bucket, and `max`
is exact.

### Integrity check

When `--directives` fixes the length with `tokens=N` (not a range), bench
checks every successful response: it must deliver exactly `N` tokens of
content (counted as whitespace-separated words, the LLMock token), and if it
reports `usage`, the usage must say `N` too. `N` is capped by `--max-tokens`
(or Anthropic's default 4096) just like the mock caps it. Mismatches catch
proxies that truncate, merge or re-count streams:

```
Integrity:    305 checked, 0 mismatches
```

## Summary

```
50 users, 15s, stream, openai http://localhost:4000
Requests:     305 (305 ok, 0 failed)
Throughput:   19.3 req/s, 1927.5 output tok/s
Latency (ms): p50 2536.7  p90 2830.2  p95 2945.1  p99 3189.1  max 3229.1
TTFT (ms):    p50 394.6  p90 501.1  p95 521.4  p99 593.4  max 608.9
ITL (ms):     p50 19.5  p90 24.1  p95 27.4  p99 50.8  max 775.7
Tokens/req:   mean 100.0  min 100  max 100
Integrity:    305 checked, 0 mismatches
```

An `Errors:` line appears when requests failed, e.g.
`Errors: 429: 12, timeout: 3`.

## JSON report

`--out report.json` writes the summary plus the run parameters:

| Field | Meaning |
|-------|---------|
| `config` | `url`, `api`, `model`, `users`, `requests`, `duration`, `ramp`, `stream`, `directives`, `prompt`, `max_tokens`, `seed`, `seeded`, `timeout` (durations as Go strings, e.g. `"15s"`; the API key is never written) |
| `started_at` | RFC 3339 start time |
| `elapsed_seconds` | wall time of the run |
| `requests`, `ok`, `failed` | request counts |
| `errors_by_status` | `{"429": 12}`: non-2xx responses by status |
| `errors_by_kind` | `{"http": 12, "timeout": 3}`: failures by kind |
| `requests_per_second`, `output_tokens_per_second` | throughput |
| `latency`, `ttft`, `itl` | `{"p50_ms", "p90_ms", "p95_ms", "p99_ms", "max_ms"}`, in milliseconds |
| `tokens_per_request_mean`, `tokens_per_request_min`, `tokens_per_request_max` | tokens per successful response |
| `integrity_checked`, `integrity_mismatches` | integrity check counts |

## Recipes

### How much latency does LiteLLM add?

Run the same load against the mock directly and through LiteLLM, using the
stack in [`examples/litellm`](../examples/litellm). The mock's TTFT is the
`delay` you ask for, so the difference in TTFT p50 is the proxy's overhead
per request.

```bash
docker compose -f examples/litellm/docker-compose.yml up -d --build

# 1. Baseline: the mock alone, on :8080.
llmock bench --url http://localhost:8080 --users 50 --duration 30s \
  --stream --directives "tokens=100 tps=50" --out direct.report.json

# 2. The same load through LiteLLM, on :4000.
llmock bench --url http://localhost:4000 --api-key sk-llmock --users 50 --duration 30s \
  --stream --directives "tokens=100 tps=50" --out litellm.report.json

# Overhead in ms: TTFT p50 through LiteLLM minus TTFT p50 direct.
jq -n --slurpfile d direct.report.json --slurpfile l litellm.report.json \
  '$l[0].ttft.p50_ms - $d[0].ttft.p50_ms'

docker compose -f examples/litellm/docker-compose.yml down
```

Raise `--users` to find where the overhead starts to grow. Both runs cross
the same Docker port mapping, so its cost cancels out; for absolute numbers,
run bench on a different machine from the system under test.

### Anthropic through LiteLLM

```bash
llmock bench --url http://localhost:4000 --api anthropic --model mock-claude \
  --api-key sk-llmock --users 20 --requests 200 --stream --directives "tokens=50"
```

### Does the gateway retry?

Make the mock fail a fraction of requests and see how many failures reach
the client:

```bash
llmock bench --url http://localhost:4000 --api-key sk-llmock --users 20 --duration 30s \
  --directives "tokens=50 error=503 error_rate=0.2"
```

Without retries about 20% of the requests fail with a 503; with retries in
the gateway, far fewer do. LiteLLM retries by default: in the example stack
about 1% of the requests failed.

### Does anything truncate long streams?

```bash
llmock bench --url http://localhost:4000 --api-key sk-llmock --users 10 --requests 50 \
  --stream --directives "tokens=4000 tps=1000"
```

Any `truncated` error or integrity mismatch points at the proxy. A latency
well above `tokens/tps` (here 4 s) means the proxy cannot keep up with the
stream rate.
