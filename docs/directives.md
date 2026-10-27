# Directive reference

Directives tell LLMock how to answer one request: how many tokens, how fast,
and whether to fail. They travel inside the prompt text, so they survive
LiteLLM and any gateway that drops custom headers and body fields.

## Syntax

A directive block is `[[mock key=value key=value ...]]`: space-separated
`key=value` pairs or bare flags (`hang`), anywhere in a message.

```
Summarize this please [[mock tokens=500 delay=2s tps=20]]
```

- Pairs are separated by whitespace, so a value cannot contain a space; it
  cannot contain `]` either.
- Several blocks may appear in one text; later blocks override earlier keys.
- The header, `--defaults` and `--overrides` use the same `key=value ...`
  syntax without the brackets.

Every mistake is an error, never silently ignored: an unknown key
(`tokns=5`), a missing value (`tokens`), a value on a flag (`hang=1`) or an
out-of-range value returns `400` in the provider's error format, naming the
key:

```console
$ curl -s localhost:8080/v1/chat/completions -H 'Content-Type: application/json' \
    -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Hello [[mock tokns=5]]"}]}'
{"error":{"message":"unknown mock directive \"tokns\"","type":"invalid_request_error","param":null,"code":null}}
```

An invalid `--defaults` or `--overrides` stops the server at startup.

## Precedence

Four layers are merged, lowest to highest; a key set in a higher layer
replaces the same key from a lower one:

1. **Server defaults**: `--defaults` / `LLMOCK_DEFAULTS` (default
   `tokens=100`).
2. **Header**: `X-Mock-Directives: tokens=500 tps=10`. Handy for curl and
   your own code; most proxies drop it.
3. **Prompt directives**: `[[mock ...]]` blocks in the scanned texts (next
   section), in order.
4. **Server overrides**: `--overrides` / `LLMOCK_OVERRIDES`, applied on top of
   everything, e.g. `--overrides "error_rate=0.05"` to inject 5% failures
   without touching any client.

Native request fields are applied after the merge: `max_tokens` caps the
length (see [below](#max_tokens)) and the OpenAI body `seed` is used when no
`seed` directive is set.

## Scope: which texts are scanned

In a chat, only the system prompt and the **last user message** are
scanned, so a directive typed in an earlier turn does not keep applying to
later turns. Within the scope, texts are merged in this order (later wins):

| Endpoint | Scanned texts |
|----------|---------------|
| OpenAI `/v1/chat/completions` | every `system` and `developer` message, then the last `user` message |
| Anthropic `/v1/messages`, `/v1/messages/count_tokens` | `system`, then the last `user` message |
| OpenAI `/v1/completions` | every `prompt` string |
| OpenAI `/v1/embeddings` | every string `input` (token-ID inputs are not scanned) |

Message content may be a string or an array of parts/blocks; only `text`
parts are read (joined with newlines). Assistant messages are never scanned.

So the system prompt can set a conversation-wide default and the last user
message overrides it:

```console
$ curl -s localhost:8080/v1/chat/completions -H 'Content-Type: application/json' -d '{"model":"mock-gpt","messages":[
    {"role":"system","content":"[[mock tokens=4]]"},
    {"role":"user","content":"[[mock tokens=9]]"},
    {"role":"assistant","content":"..."},
    {"role":"user","content":"next question"}]}' | jq -c .usage
{"completion_tokens":4,"prompt_tokens":15,"total_tokens":19}
```

The `tokens=9` in the earlier user turn no longer applies.

## Keys

| Key | Value | Meaning |
|-----|-------|---------|
| `tokens` | `N` or `N-M`, 0 to 1000000 | Completion length. A range picks uniformly in `N..M` (inclusive), seeded if a seed is set. |
| `prompt_tokens` | `N` | Reported prompt/input token count, replacing the estimate. |
| `delay` | Go duration (`500ms`, `2s`) | Time before the first content token. Also delays non-streaming bodies and errors. |
| `tps` | number, `0` = unlimited | Output speed in tokens per second. |
| `jitter` | `0` to `1` | Random variation of each chunk's send time (streaming, `tps > 0`). |
| `chunk` | `N` ≥ 1 (default 1) | Tokens per streaming frame. |
| `error` | HTTP status `400` to `599` | Respond with this status and the provider's error body. |
| `error_rate` | `0` to `1` | Probability of the error; status from `error`, else 500. |
| `error_after` | `N` | Send `N` tokens, then the error. Streaming: an in-stream error frame. |
| `fail_after` | `N` | Drop the connection after `N` tokens, with no closing frames. |
| `hang` | flag | Never send headers; hold the connection open. |
| `hang_after` | `N` | Send `N` tokens, then go silent forever. |
| `finish_reason` | `stop`, `length`, `content_filter` | Force the finish reason. |
| `seed` | integer | Make the text and `tokens` range picks deterministic. |

### `tokens`

`[[mock tokens=500]]` asks for exactly 500 tokens; `usage` reports exactly
what was sent. `[[mock tokens=200-400]]` picks a length per request, which
is handy for realistic mixes. With `[[mock tokens=200-400 seed=7]]` the pick
is the same on every request.

### `prompt_tokens`

`[[mock prompt_tokens=120000]]` reports 120000 prompt (OpenAI) or input
(Anthropic) tokens whatever the actual prompt, to exercise cost tracking and
budget limits. It also sets the `count_tokens` answer and the embeddings
`usage`.

### `delay`

`[[mock delay=2s]]` waits 2 s before the first content token: the time to
first token (TTFT). Streams send headers and opening frames immediately,
then wait `delay` before the first token; non-streaming responses and errors
are sent after the delay.

### `tps`

`[[mock tokens=100 tps=20]]` streams at 20 tokens per second, about 5 s per
response. Token `i` (0-based) is due at `delay + i/tps` measured from the
start of the request, an absolute schedule, so timing does not drift under
load. `tps=0` (the default) sends everything as fast as possible.

### `jitter`

`[[mock tps=20 jitter=0.4]]` moves each chunk's send time by a uniform
random offset of up to `±jitter/2` token intervals: here ±20% of 50 ms, so
±10 ms. Offsets apply to the absolute schedule, so they never accumulate.
Jitter only applies to streams with `tps > 0`, and never to a frame holding
only the first token, so with `chunk=1` TTFT stays exactly `delay`.

### `chunk`

`[[mock tokens=7 chunk=3]]` groups tokens three per frame (`"Lorem ipsum
dolor"`, `" sit amet, consectetur"`, `" adipiscing"`). A chunk is sent when
its last token is due, so `tps` keeps meaning tokens per second.

### `error` and `error_rate`

`[[mock error=429]]` answers with a 429 and the provider's error body after
`delay`, before any other byte; a 429 also carries `Retry-After: 1`.
Error types follow the providers: Anthropic `529` is `overloaded_error`,
OpenAI `5xx` is `server_error`, and so on.

`[[mock error_rate=0.1]]` fails 10% of requests with a 500;
`[[mock error=503 error_rate=0.1]]` fails 10% with a 503. As a server
override it injects chaos without touching clients:

```bash
llmock serve --overrides "error=503 error_rate=0.05"
```

### `error_after`

`[[mock tokens=100 error=529 error_after=20]]` on a stream sends the 200
headers and 20 tokens, then an in-stream error, and closes: Anthropic `event: error`, OpenAI
`data: {"error": ...}` (no `[DONE]`). This is the failure a gateway can no
longer retry transparently. Without `error` the status is 500. Non-streaming,
the error response is sent after the time 20 tokens would take.

### `fail_after`

`[[mock tokens=100 fail_after=20]]` sends 20 tokens and drops the connection:
no `[DONE]` / `message_stop`, no error, no usage, as with a real network
failure. Non-streaming, the connection is dropped after the time 20 tokens
would take, before any byte is sent.

### `hang`

`[[mock hang]]` never sends response headers and holds the connection until
the client disconnects or the server shuts down, then drops it. Use it to
test response-header timeouts. `hang` wins over every other key.

### `hang_after`

`[[mock tokens=100 hang_after=20]]` streams headers and 20 tokens, then goes
silent until the client disconnects or the server shuts down. Use it to test
stream idle timeouts. Non-streaming, it hangs after the time 20 tokens would
take, before any byte is sent.

For `error_after`, `fail_after` and `hang_after`, an `N` larger than the
response length has no effect. When more than one is set, the smallest `N`
wins; on a tie the order is `error_after`, `fail_after`, `hang_after`.

### `finish_reason`

`[[mock finish_reason=length]]` reports `length` even though every requested
token was sent. On Anthropic the values map to `end_turn`, `max_tokens` and
`refusal`.

### `seed`

`[[mock seed=7]]` makes the response deterministic: the same effective
directives and seed produce byte-identical text. See [Seeds](#seeds).

## Stream timeline

For a streaming request without `hang` or a pre-stream `error`:

1. HTTP 200 headers and the opening frames are sent **immediately**: the
   OpenAI chat role chunk (`delta.role`, empty content); Anthropic
   `message_start`, `content_block_start` and `ping`. Legacy completions have
   no opening frame.
2. Wait `delay`.
3. Content frames, `chunk` tokens each, paced by `tps` and `jitter`.
4. Closing frames: the finish-reason chunk, the usage chunk (only with
   `stream_options.include_usage`) and `data: [DONE]` for OpenAI;
   `content_block_stop`, `message_delta` (stop reason and output usage) and
   `message_stop` for Anthropic.

`error_after`, `fail_after` and `hang_after` interrupt step 3 after `N`
tokens. TTFT, in the mock's metrics and in `llmock bench`, is the time to
the first **content** token, not the first byte.

Non-streaming responses follow the same clock: the body is sent when the
last token would have been streamed, `delay + (tokens-1)/tps`.

Every response that went through a plan carries an `X-LLMock-Plan` header
with the effective parameters, e.g.
`tokens=3 prompt_tokens=2 delay=500ms tps=0`. Optional keys appear only when
set; `error=` appears only when the error is actually injected.

## `max_tokens`

The native output limit **caps** the length and never sets it on its own:
the length always comes from `tokens` (directives or `--defaults`).

- OpenAI chat: `max_completion_tokens`, else `max_tokens`. Legacy
  completions and Anthropic: `max_tokens`.
- If `tokens` exceeds the cap, exactly `cap` tokens are sent and the finish
  reason is `length` (Anthropic `max_tokens`), unless `finish_reason` is set
  explicitly.
- `error_after`, `fail_after` and `hang_after` count against the capped
  length.

```console
$ curl -s localhost:8080/v1/chat/completions -H 'Content-Type: application/json' \
    -d '{"model":"mock-gpt","max_tokens":2,"messages":[{"role":"user","content":"[[mock tokens=8]]"}]}' \
    | jq -c '[.choices[0].finish_reason, .usage.completion_tokens]'
["length",2]
```

## Seeds

- A `seed` directive wins; otherwise the OpenAI body `seed` field (chat and
  legacy completions) is used. Anthropic has no body seed.
- Unseeded, the text is the lorem ipsum passage in order, looping. Seeded,
  each word is drawn pseudo-randomly from the same vocabulary, from the seed
  and the word's position.
- A seed also fixes the pick of a `tokens=N-M` range.
- **Chaos is never seeded**: `error_rate` and `jitter` always use a fresh
  random source, regardless of any seed. Otherwise a client sending a
  constant seed would see 0% or 100% failures instead of the configured
  rate.

## Tokens

- One token is one lorem ipsum word. Every token after the first carries a
  leading space, so concatenated deltas read as normal text. Counting the
  whitespace-separated words of a response gives its token count.
- Completion tokens are exact by construction, and `usage` always equals what
  was sent. When `fail_after` cuts a stream no usage is sent at all.
- Prompt and input tokens are an estimate: `ceil(characters / 4)` for each
  input text (every message, including the system prompt), summed. Token-ID
  embedding inputs count one token per ID. `prompt_tokens` replaces the
  estimate. Exact tokenizer parity is a non-goal.
