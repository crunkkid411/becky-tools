#!/usr/bin/env python3
"""
Text embeddings for becky-embed / becky-search glue.

Two backends, selected by whether --server-url is given:

1. RESIDENT SERVER (--server-url, the new DEFAULT for --model qwen3-4b):
   POSTs to a llama.cpp llama-server running Qwen3-Embedding-4B (Q5_K_M GGUF)
   with `--embedding --pooling last`. The server returns the model's NATIVE
   2560-dim embeddings UNNORMALIZED on this path, so this client:
     - applies the query/document asymmetry per --mode (queries get the
       Qwen3 "Instruct: {task}\nQuery: {q}" prefix; documents are raw text),
     - MRL-truncates each vector to the first --truncate-dim dims (1024),
     - L2-normalizes (so cosine == dot product, matching the vec0 cosine table).
   This avoids the per-call sentence-transformers model reload — the server is
   resident. Start it with X:\\AI-2\\becky-tools\\start-embed-server.bat.

2. IN-PROCESS sentence-transformers (no --server-url, kept for --model
   qwen3-0.6b): loads Qwen/Qwen3-Embedding-0.6B from the cache dir and encodes
   with normalize_embeddings=True. This is the original 0.6B fallback path; it
   is a DIFFERENT vector space from the 4B server, so callers must never mix
   them (cmd/embed records the model tag in the DB and cmd/search refuses a
   mismatch).

Contract (stdout JSON):
  {"model": str, "dim": int, "vectors": [[float, ...], ...]}
On failure it prints {"skipped": true, "reason": "..."} and exits 0 so the Go
caller can surface a clean error (or, for the server path, a "start the server"
message) instead of crashing.

Usage:
  python embed_text.py --texts-b64 <base64 json array of strings>
                       [--model Qwen/Qwen3-Embedding-0.6B]
                       [--cache-dir <dir>] [--batch-size 32] [--device cpu|cuda]
                       [--server-url http://127.0.0.1:8088]
                       [--mode document|query] [--truncate-dim 1024]
                       [--task "<instruct task string>"]
"""
import argparse
import base64
import json
import os
import sys

# Default Qwen3-Embedding query instruction. Documents get NO prefix; queries get
# "Instruct: {task}\nQuery: {query}". Using one FIXED task string keeps the query
# vector space consistent across runs (the asymmetry is what lifts retrieval).
DEFAULT_TASK = "Given a search query, retrieve relevant transcript passages"


def l2_normalize(vec):
    """Return vec scaled to unit L2 norm (cosine == dot product). A zero vector
    is returned unchanged to avoid division by zero."""
    s = 0.0
    for x in vec:
        s += x * x
    if s <= 0.0:
        return vec
    inv = 1.0 / (s ** 0.5)
    return [x * inv for x in vec]


def truncate_and_normalize(vec, dim):
    """MRL-truncate to the first `dim` components then L2-normalize. Qwen3
    embeddings are Matryoshka (MRL): the leading dims are a valid lower-dim
    embedding, so truncate-then-renormalize is the correct way to keep the
    existing float[1024] schema from a native-2560 model."""
    if dim and dim > 0 and len(vec) > dim:
        vec = vec[:dim]
    return l2_normalize(vec)


def build_inputs(texts, mode, task):
    """Apply the Qwen3-Embedding query/document asymmetry. Documents are embedded
    as raw text (no prefix); queries are wrapped with the Instruct/Query prompt.
    This asymmetry is critical for retrieval accuracy."""
    if mode == "query":
        return ["Instruct: %s\nQuery: %s" % (task, t) for t in texts]
    return list(texts)


def embed_via_server(texts, server_url, mode, task, truncate_dim, model_label):
    """POST texts to a resident llama-server and return the becky JSON contract.
    Tries the OpenAI-compatible /v1/embeddings first, then falls back to the
    native /embedding endpoint. Applies prefix, MRL-truncation, and L2-norm."""
    inputs = build_inputs(texts, mode, task)
    base = server_url.rstrip("/")

    vectors = _try_openai_embeddings(base, inputs)
    if vectors is None:
        vectors = _try_native_embedding(base, inputs)
    if vectors is None:
        raise RuntimeError(
            "embedding server at %s returned no usable vectors "
            "(is start-embed-server.bat running?)" % base
        )
    if len(vectors) != len(texts):
        raise RuntimeError(
            "server returned %d vectors for %d inputs" % (len(vectors), len(texts))
        )

    out = [truncate_and_normalize([float(x) for x in v], truncate_dim) for v in vectors]
    dim = len(out[0]) if out else 0
    return {"model": model_label, "dim": dim, "vectors": out}


def _http_post_json(url, payload, timeout=120):
    """POST JSON to url, return the decoded JSON response (or raise)."""
    import urllib.request

    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        url, data=data, headers={"Content-Type": "application/json"}, method="POST"
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read().decode("utf-8"))


def _try_openai_embeddings(base, inputs):
    """Try POST {base}/v1/embeddings (OpenAI-compatible). Returns a list of
    vectors ordered by the `index` field, or None if the endpoint is absent /
    shaped differently so the caller can fall back."""
    try:
        body = _http_post_json(base + "/v1/embeddings", {"input": inputs})
    except Exception:
        return None
    data = body.get("data") if isinstance(body, dict) else None
    if not isinstance(data, list) or not data:
        return None
    # Order by the returned index so vectors line up with our inputs.
    try:
        ordered = sorted(data, key=lambda d: d.get("index", 0))
        vecs = [d["embedding"] for d in ordered]
    except (KeyError, TypeError):
        return None
    if not all(isinstance(v, list) and v for v in vecs):
        return None
    return vecs


def _try_native_embedding(base, inputs):
    """Try POST {base}/embedding (llama.cpp native). The native endpoint accepts
    {"content": [...]} or a single string and returns either a list of
    {"embedding": [...]} objects or a single object. Returns a list of vectors or
    None on shape mismatch."""
    try:
        body = _http_post_json(base + "/embedding", {"content": inputs})
    except Exception:
        return None
    return _extract_native_vectors(body, len(inputs))


def _extract_native_vectors(body, n):
    """Normalize the several shapes llama.cpp's /embedding has used into a flat
    list of n float-vectors. Handles:
      - [{"embedding": [...]}...]            (batched)
      - [{"embedding": [[...]]}, ...]        (embedding wrapped one level)
      - {"embedding": [...]}                 (single)
      - [[...], ...] / [...]                 (bare)
    Returns None if it can't recover n vectors."""
    def unwrap(emb):
        # Some builds nest the vector one level: {"embedding": [[...]]}.
        if isinstance(emb, list) and len(emb) == 1 and isinstance(emb[0], list):
            return emb[0]
        return emb

    if isinstance(body, dict) and "embedding" in body:
        body = [body]

    if isinstance(body, list) and body:
        vecs = []
        for item in body:
            if isinstance(item, dict) and "embedding" in item:
                vecs.append(unwrap(item["embedding"]))
            elif isinstance(item, list):
                vecs.append(item)
            else:
                return None
        if all(isinstance(v, list) and v and isinstance(v[0], (int, float)) for v in vecs):
            if len(vecs) == n:
                return vecs
    return None


def embed_in_process(texts, model, cache_dir, batch_size, device, mode, task, truncate_dim):
    """Original sentence-transformers path (0.6B fallback). Loads the model from
    the cache dir and encodes with normalize_embeddings=True. Applies the same
    query/document asymmetry and (optional) MRL truncation for parity, though the
    0.6B model is natively 1024 so truncation is usually a no-op."""
    from sentence_transformers import SentenceTransformer

    st_kwargs = {}
    if cache_dir:
        st_kwargs["cache_folder"] = cache_dir
    if device:
        st_kwargs["device"] = device
    st = SentenceTransformer(model, **st_kwargs)

    inputs = build_inputs(texts, mode, task)
    vecs = st.encode(
        inputs,
        batch_size=batch_size,
        normalize_embeddings=True,
        convert_to_numpy=True,
        show_progress_bar=False,
    )
    out = [truncate_and_normalize([float(x) for x in v], truncate_dim) for v in vecs]
    dim = len(out[0]) if out else 0
    return {"model": model, "dim": dim, "vectors": out}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--texts-b64", default="",
                    help="base64-encoded JSON array of strings to embed")
    ap.add_argument("--texts-b64-file", default="",
                    help="path to a file holding the base64 payload (for large "
                         "transcripts that exceed the OS command-line length limit)")
    ap.add_argument("--model", default="Qwen/Qwen3-Embedding-0.6B",
                    help="model id/label (in-process ST id, or a label for the server path)")
    ap.add_argument("--cache-dir", default="",
                    help="sentence-transformers cache_folder (in-process path only)")
    ap.add_argument("--batch-size", type=int, default=32)
    ap.add_argument("--device", default="",
                    help="torch device for the in-process path: cpu or cuda")
    ap.add_argument("--server-url", default="",
                    help="if set, embed via this resident llama-server instead of in-process")
    ap.add_argument("--mode", default="document", choices=["document", "query"],
                    help="document = raw text (no prefix); query = Instruct/Query prefix")
    ap.add_argument("--truncate-dim", type=int, default=0,
                    help="MRL-truncate each vector to this many leading dims then L2-normalize (0 = off)")
    ap.add_argument("--task", default=DEFAULT_TASK,
                    help="instruction task string used to build the query prefix (--mode query)")
    args = ap.parse_args()

    # The base64 payload arrives inline (--texts-b64) or, for large transcripts
    # that would blow the OS command-line length limit, from a file
    # (--texts-b64-file). Exactly one must be supplied.
    b64 = args.texts_b64
    if args.texts_b64_file:
        with open(args.texts_b64_file, "r", encoding="utf-8") as fh:
            b64 = fh.read().strip()
    if not b64:
        raise ValueError("supply --texts-b64 or --texts-b64-file")
    texts = json.loads(base64.b64decode(b64))
    if not isinstance(texts, list):
        raise ValueError("texts payload must be a JSON array of strings")
    if not texts:
        print(json.dumps({"model": args.model, "dim": 0, "vectors": []}))
        return

    if args.server_url:
        # Resident-server path: no model cache, no HF env needed.
        result = embed_via_server(
            texts, args.server_url, args.mode, args.task,
            args.truncate_dim, args.model,
        )
    else:
        # In-process path: point HF / ST caches at the supplied dir and stay
        # offline so the already-downloaded weights are reused (no network).
        cache_dir = args.cache_dir or None
        if cache_dir:
            os.makedirs(cache_dir, exist_ok=True)
            os.environ.setdefault("SENTENCE_TRANSFORMERS_HOME", cache_dir)
            os.environ.setdefault("HF_HOME", cache_dir)
        os.environ.setdefault("HF_HUB_OFFLINE", "1")
        os.environ.setdefault("TRANSFORMERS_OFFLINE", "1")
        result = embed_in_process(
            texts, args.model, cache_dir, args.batch_size, args.device,
            args.mode, args.task, args.truncate_dim,
        )

    result["vectors"] = [[round(float(x), 6) for x in v] for v in result["vectors"]]
    print(json.dumps(result))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 — surface any failure as clean JSON
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
