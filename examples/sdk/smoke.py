"""Compatibility smoke test: the official OpenAI and Anthropic Python SDKs against LLMock.

Every check asks for exactly 7 tokens with a [[mock tokens=7]] directive and asserts
that usage and the content (or stream deltas) agree. Prints "PASS <name>" per check
and exits non-zero on the first failure.

Against the mock directly:
    uv run --with openai --with anthropic examples/sdk/smoke.py \
        --openai-base http://localhost:8080/v1 --anthropic-base http://localhost:8080

Through LiteLLM (examples/litellm), which has no legacy completions, count_tokens or
models routes to the mock:
    uv run --with openai --with anthropic examples/sdk/smoke.py --via-litellm --key sk-llmock \
        --openai-base http://localhost:4000/v1 --anthropic-base http://localhost:4000
"""

import argparse
import math
import sys

import anthropic
import openai

N = 7
PROMPT = f"Say something [[mock tokens={N}]]"
MESSAGES = [{"role": "user", "content": PROMPT}]


def expect(what, got, want):
    if got != want:
        raise AssertionError(f"{what}: got {got!r}, want {want!r}")


def words(text):
    return len(text.split())


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--openai-base", required=True, help="OpenAI base URL, e.g. http://localhost:8080/v1")
    p.add_argument("--anthropic-base", required=True, help="Anthropic base URL, e.g. http://localhost:8080")
    p.add_argument("--key", default="dummy", help="API key sent to both endpoints")
    p.add_argument("--openai-model", default="mock-gpt")
    p.add_argument("--anthropic-model", default="mock-claude")
    p.add_argument("--embed-model", default="mock-embed")
    p.add_argument("--via-litellm", action="store_true", help="skip legacy completions, count_tokens and models")
    a = p.parse_args()

    oai = openai.OpenAI(base_url=a.openai_base, api_key=a.key, max_retries=0)
    ant = anthropic.Anthropic(base_url=a.anthropic_base, api_key=a.key, max_retries=0)

    def chat():
        r = oai.chat.completions.create(model=a.openai_model, messages=MESSAGES)
        expect("usage.completion_tokens", r.usage.completion_tokens, N)
        expect("words", words(r.choices[0].message.content), N)
        expect("finish_reason", r.choices[0].finish_reason, "stop")

    def chat_stream():
        deltas, usage = 0, None
        for c in oai.chat.completions.create(
            model=a.openai_model, messages=MESSAGES, stream=True, stream_options={"include_usage": True}
        ):
            if c.choices and c.choices[0].delta.content:
                deltas += 1
            usage = c.usage or usage
        expect("content deltas", deltas, N)
        expect("usage.completion_tokens", usage and usage.completion_tokens, N)

    def completions():
        r = oai.completions.create(model=a.openai_model, prompt=PROMPT)
        expect("usage.completion_tokens", r.usage.completion_tokens, N)
        expect("words", words(r.choices[0].text), N)

    def completions_stream():
        deltas, usage = 0, None
        for c in oai.completions.create(
            model=a.openai_model, prompt=PROMPT, stream=True, stream_options={"include_usage": True}
        ):
            if c.choices and c.choices[0].text:
                deltas += 1
            usage = c.usage or usage
        expect("content deltas", deltas, N)
        expect("usage.completion_tokens", usage and usage.completion_tokens, N)

    def embeddings():
        # No encoding_format: the SDK requests base64 and decodes it.
        v = oai.embeddings.create(model=a.embed_model, input=PROMPT).data[0].embedding
        expect("dimensions", len(v), 1536)
        expect("unit norm", round(math.sqrt(sum(x * x for x in v)), 3), 1.0)

    def openai_models():
        ids = [m.id for m in oai.models.list()]
        expect(f"{a.openai_model} listed", a.openai_model in ids, True)

    def messages():
        r = ant.messages.create(model=a.anthropic_model, max_tokens=1024, messages=MESSAGES)
        expect("usage.output_tokens", r.usage.output_tokens, N)
        expect("words", words(r.content[0].text), N)
        expect("stop_reason", r.stop_reason, "end_turn")

    def messages_stream():
        deltas = 0
        with ant.messages.stream(model=a.anthropic_model, max_tokens=1024, messages=MESSAGES) as s:
            for e in s:
                deltas += e.type == "content_block_delta"
            final = s.get_final_message()
        expect("content deltas", deltas, N)
        expect("usage.output_tokens", final.usage.output_tokens, N)
        expect("words", words(final.content[0].text), N)

    def count_tokens():
        r = ant.messages.count_tokens(model=a.anthropic_model, messages=MESSAGES)
        expect("input_tokens > 0", r.input_tokens > 0, True)

    def anthropic_models():
        ids = [m.id for m in ant.models.list()]
        expect(f"{a.anthropic_model} listed", a.anthropic_model in ids, True)

    checks = [
        ("openai chat", chat, True),
        ("openai chat stream", chat_stream, True),
        ("openai completions", completions, False),
        ("openai completions stream", completions_stream, False),
        ("openai embeddings", embeddings, True),
        ("openai models", openai_models, False),
        ("anthropic messages", messages, True),
        ("anthropic messages stream", messages_stream, True),
        ("anthropic count_tokens", count_tokens, False),
        ("anthropic models", anthropic_models, False),
    ]
    for name, fn, via_litellm in checks:
        if a.via_litellm and not via_litellm:
            continue
        try:
            fn()
        except Exception as e:
            print(f"FAIL {name}: {e!r}")
            return 1
        print(f"PASS {name}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
