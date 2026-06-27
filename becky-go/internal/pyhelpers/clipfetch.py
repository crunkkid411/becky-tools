#!/usr/bin/env python3
"""becky-clipcheck helper: re-fetch a page and return its content as ground truth.

Given a URL, fetch the live HTML and return THREE independent views of its text
so the Go side can deterministically score whether a saved markdown clip captured
it faithfully:

  page_text   : trafilatura main-content extraction (favor_recall) — broad body text
  full_text   : ALL visible text (bs4 get_text of <body>) — for precision (did the
                clip invent text not on the page?)
  main_blocks : substantial paragraphs/list items (>= 40 words) — the independent
                "this is real content" signal used for recall (did the clip drop a
                whole block of content?)

Emits exactly one line of JSON to stdout:
  {"ok": true, "title": "...", "page_text": "...", "full_text": "...",
   "main_blocks": ["...", ...]}
or, on any failure: {"ok": false, "reason": "..."} and exits 0 so the Go caller
surfaces a clean "could not verify" rather than a stack trace.

No LLM, fully deterministic. This is the verification counterpart to web2md.py.
"""
import argparse
import json
import sys

# Blocks shorter than this (in words) are treated as navigation/boilerplate, not
# content, so favor_recall noise doesn't drag the recall denominator down.
MIN_BLOCK_WORDS = 40


def log(msg):
    print(msg, file=sys.stderr, flush=True)


def looks_like_html(s):
    """True when s is decodable HTML, not garbage. trafilatura.fetch_url
    occasionally returns un-decompressed / mis-decoded bytes (e.g. brotli/zstd it
    can't handle) — a high replacement-char ratio with no tags. We detect that and
    fall back to a clean fetch instead of feeding garbage downstream."""
    if not s or len(s) < 50:
        return False
    head = s[:6000].lower()
    if "<html" not in head and "<body" not in head and "<!doctype" not in head:
        return False
    return (s.count("\ufffd") / len(s)) < 0.02


def fetch_html(url, timeout):
    """Fetch raw HTML — same path as web2md.py so the two tools see the same page.
    Validates the result and falls back to a clean urllib fetch when trafilatura
    returns garbage."""
    import trafilatura
    downloaded = trafilatura.fetch_url(url)
    if looks_like_html(downloaded):
        return downloaded
    return _plain_fetch(url, timeout)


def _plain_fetch(url, timeout):
    """A clean fetch: no forced compression, gzip handled, charset detected."""
    import urllib.request
    import gzip
    req = urllib.request.Request(
        url,
        headers={
            "User-Agent": (
                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
                "AppleWebKit/537.36 (KHTML, like Gecko) "
                "Chrome/124.0 Safari/537.36"
            ),
            "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
            "Accept-Encoding": "gzip, identity",
        },
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        raw = resp.read()
        charset = resp.headers.get_content_charset()
        if resp.headers.get("Content-Encoding", "").lower() == "gzip" or raw[:2] == b"\x1f\x8b":
            try:
                raw = gzip.decompress(raw)
            except OSError:
                pass
    for cs in (charset, "utf-8", "latin-1"):
        if not cs:
            continue
        try:
            return raw.decode(cs)
        except (UnicodeDecodeError, LookupError):
            continue
    return raw.decode("utf-8", errors="replace")


def soup_of(html):
    from bs4 import BeautifulSoup
    try:
        return BeautifulSoup(html, "lxml")
    except Exception:
        return BeautifulSoup(html, "html.parser")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("url")
    ap.add_argument("--timeout", type=int, default=30)
    args = ap.parse_args()

    try:
        html = fetch_html(args.url, args.timeout)
    except Exception as e:  # noqa: BLE001
        print(json.dumps({"ok": False, "reason": f"fetch failed: {type(e).__name__}: {e}"}))
        return
    if not html or len(html.strip()) < 50:
        print(json.dumps({"ok": False, "reason": "empty or unreachable page"}))
        return

    import trafilatura
    # Article mode (favor_recall=False) — the SAME main-content target web2md
    # saves, so page_text is "what the clip should contain", used both to gate
    # chrome out of the recall units and as the recall ground truth.
    page_text = trafilatura.extract(
        html, url=args.url, output_format="txt",
        favor_recall=False, include_tables=True, include_comments=False,
    ) or ""

    soup = soup_of(html)
    for t in soup(["script", "style", "noscript", "template"]):
        t.decompose()
    body = soup.find("body") or soup
    full_text = " ".join(body.get_text(" ", strip=True).split())

    blocks = []
    for el in body.find_all(["p", "li", "blockquote"]):
        txt = " ".join(el.get_text(" ", strip=True).split())
        if len(txt.split()) >= MIN_BLOCK_WORDS:
            blocks.append(txt)

    title = ""
    try:
        m = trafilatura.extract_metadata(html, default_url=args.url)
        if m is not None:
            d = m.as_dict() if hasattr(m, "as_dict") else dict(m.__dict__)
            title = d.get("title") or ""
    except Exception as e:  # noqa: BLE001
        log(f"metadata warning: {e}")

    print(json.dumps({
        "ok": True,
        "title": title,
        "page_text": page_text,
        "full_text": full_text,
        "main_blocks": blocks,
    }))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"ok": False, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
