#!/usr/bin/env python3
"""becky-web2md helper: deterministic web-page -> clean markdown extraction.

Fetches a URL (or reads pre-fetched HTML from --html-file), extracts the main
content with trafilatura (which yields a confidence-bearing markdown rendering),
then post-processes with BeautifulSoup to recover code-block languages, GFM
tables, and an image manifest. Emits exactly one line of JSON to stdout:

  {"title","author","date","url","sitename","description",
   "markdown","text","confidence","extraction_method","word_count",
   "images":[{"src","localname","alt"}],
   "links":[{"text","href"}],            # only for --extract links
   "tables":[markdown,...],              # only for --extract tables
   "code_blocks":[{"language","code"},]} # only for --extract code

On any failure it prints {"skipped": true, "reason": "..."} and exits 0 so the
Go caller surfaces a clean error rather than a stack trace.

No LLM, fully deterministic. Heavy lifting is in trafilatura's C-backed lxml
parser; this file is glue.
"""
import argparse
import hashlib
import json
import os
import re
import sys
from urllib.parse import urljoin, urlparse


def log(msg):
    """Diagnostics go to stderr so stdout stays pure JSON."""
    print(msg, file=sys.stderr, flush=True)


def looks_like_html(s):
    """True when s is decodable HTML, not garbage. trafilatura.fetch_url can return
    un-decompressed / mis-decoded bytes (brotli/zstd it can't handle): a high
    replacement-char ratio with no tags. Detect that and fall back to a clean
    fetch instead of handing garbage to the extractor."""
    if not s or len(s) < 50:
        return False
    head = s[:6000].lower()
    if "<html" not in head and "<body" not in head and "<!doctype" not in head:
        return False
    return (s.count("\ufffd") / len(s)) < 0.02


def fetch_html(url, timeout):
    """Fetch raw HTML. trafilatura's fetch handles redirects/compression; when it
    returns garbage we fall back to a clean urllib fetch."""
    import trafilatura
    downloaded = trafilatura.fetch_url(url)
    if looks_like_html(downloaded):
        return downloaded
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


def guess_language(node):
    """Best-effort language tag for a <pre>/<code> block from common classes.

    Checks the <pre>, its child <code>, and up to two ancestor wrappers — Sphinx
    and ReadTheDocs put the language on a grandparent
    `div.highlight-<lang>`, GitHub on `div.highlight-source-<lang>`."""
    candidates = []
    els = []
    if node is not None:
        els.append(node)
        code = node.find("code")
        if code is not None:
            els.append(code)
        # Walk up to two ancestors for highlight wrappers.
        parent = node.parent
        for _ in range(2):
            if parent is None:
                break
            els.append(parent)
            parent = parent.parent
    for el in els:
        if el is None:
            continue
        cls = el.get("class") or []
        candidates.extend(cls)
        if el.get("data-lang"):
            candidates.append(el.get("data-lang"))
        if el.get("lang"):
            candidates.append(el.get("lang"))
    # Wrapper-class prefixes that carry the language as a suffix, longest first.
    prefixes = ("highlight-source-", "highlight-text-", "language-", "lang-",
                "brush:", "sourcecode-", "highlight-")
    skip = {"highlight", "hljs", "notranslate", "source", "text", "default",
            "none", "literal-block", "code", "pre"}
    for c in candidates:
        c = c.lower().strip()
        for prefix in prefixes:
            if c.startswith(prefix) and len(c) > len(prefix):
                lang = c[len(prefix):].strip()
                if lang and lang not in skip:
                    return _normalize_lang(lang)
        if c in ("python", "go", "golang", "javascript", "js", "ts", "typescript",
                 "java", "c", "cpp", "c++", "csharp", "cs", "rust", "ruby", "php",
                 "bash", "sh", "shell", "zsh", "json", "yaml", "yml", "toml",
                 "html", "css", "sql", "xml", "kotlin", "swift", "scala", "r",
                 "perl", "lua", "haskell", "dart", "elixir", "clojure", "diff",
                 "python3", "pycon"):
            return _normalize_lang(c)
    return ""


def _normalize_lang(lang):
    aliases = {"golang": "go", "python3": "python", "pycon": "python",
               "py3": "python", "py": "python", "shell-session": "bash",
               "console": "bash", "text": ""}
    return aliases.get(lang, lang)


def extract_code_blocks(soup):
    """Pull code blocks with language hints directly from the DOM."""
    blocks = []
    for pre in soup.find_all("pre"):
        code = pre.get_text()
        if not code.strip():
            continue
        blocks.append({"language": guess_language(pre), "code": code.rstrip("\n")})
    return blocks


def html_table_to_gfm(table):
    """Render one <table> as a GitHub-flavoured markdown table."""
    rows = []
    for tr in table.find_all("tr"):
        cells = tr.find_all(["th", "td"])
        if not cells:
            continue
        rows.append([" ".join(c.get_text(" ", strip=True).split()).replace("|", "\\|")
                     for c in cells])
    if not rows:
        return ""
    width = max(len(r) for r in rows)
    rows = [r + [""] * (width - len(r)) for r in rows]
    header = rows[0]
    body = rows[1:] if len(rows) > 1 else []
    lines = ["| " + " | ".join(header) + " |",
             "| " + " | ".join(["---"] * width) + " |"]
    for r in body:
        lines.append("| " + " | ".join(r) + " |")
    return "\n".join(lines)


def extract_tables(soup):
    out = []
    for table in soup.find_all("table"):
        md = html_table_to_gfm(table)
        if md:
            out.append(md)
    return out


def extract_links(soup, base_url):
    seen = set()
    out = []
    for a in soup.find_all("a", href=True):
        href = a["href"].strip()
        if not href or href.startswith(("#", "javascript:", "mailto:")):
            continue
        full = urljoin(base_url, href)
        text = " ".join(a.get_text(" ", strip=True).split())
        key = (text, full)
        if key in seen:
            continue
        seen.add(key)
        out.append({"text": text, "href": full})
    return out


MD_IMG_RE = re.compile(r'(!\[[^\]]*\]\()([^)\s]+)((?:\s+"[^"]*")?\))')


def collect_images(markdown, soup, base_url):
    """Absolutize every markdown image src in the body and build a manifest.

    Returns (rewritten_markdown, images). Body image srcs are rewritten to their
    absolute URL in place so the Go caller can string-match and swap them for the
    local asset path. trafilatura often strips images, so DOM <img> tags are
    appended to the manifest too (flagged in_body=False) for completeness.
    """
    images = []
    seen = {}

    def repl(m):
        full = urljoin(base_url, m.group(2).strip())
        register_image(full, "", base_url, images, seen, in_body=True)
        return m.group(1) + full + m.group(3)

    rewritten = MD_IMG_RE.sub(repl, markdown)

    for img in soup.find_all("img"):
        src = img.get("src") or img.get("data-src") or ""
        if not src:
            continue
        register_image(urljoin(base_url, src.strip()), img.get("alt", ""),
                       base_url, images, seen, in_body=False)
    return rewritten, images


def register_image(full, alt, base_url, images, seen, in_body):
    if not full or full.startswith("data:"):
        return
    if full in seen:
        if in_body:  # promote an already-seen DOM image to in-body status
            for im in images:
                if im["src"] == full:
                    im["in_body"] = True
        return
    name = local_image_name(full)
    seen[full] = name
    images.append({"src": full, "localname": name, "alt": alt or "",
                   "in_body": in_body})


def local_image_name(url):
    """Deterministic local filename: <slug>-<hash8>.<ext>."""
    parsed = urlparse(url)
    base = os.path.basename(parsed.path) or "image"
    base = base.split("?")[0]
    stem, ext = os.path.splitext(base)
    if not ext or len(ext) > 6:
        ext = ".img"
    stem = re.sub(r"[^A-Za-z0-9._-]", "-", stem)[:48] or "image"
    h = hashlib.sha1(url.encode("utf-8")).hexdigest()[:8]
    return f"{stem}-{h}{ext}"


def extract_with_css(soup, selector, base_url):
    """Extract only the subtree matching a CSS selector, then markdownify it."""
    from markdownify import markdownify as mdify
    nodes = soup.select(selector)
    if not nodes:
        return None
    html = "".join(str(n) for n in nodes)
    md = mdify(html, heading_style="ATX", bullets="-")
    return clean_markdown(md)


def clean_markdown(md):
    if not md:
        return ""
    md = re.sub(r"\n{3,}", "\n\n", md)
    md = "\n".join(line.rstrip() for line in md.splitlines())
    return md.strip() + "\n"


def run_trafilatura(html, url, favor_recall):
    """Return (markdown, metadata_dict) from trafilatura."""
    import trafilatura
    from trafilatura.settings import use_config
    cfg = use_config()
    cfg.set("DEFAULT", "EXTRACTION_TIMEOUT", "0")

    md = trafilatura.extract(
        html,
        url=url,
        output_format="markdown",
        include_tables=True,
        include_links=True,
        include_images=True,
        include_formatting=True,
        favor_recall=favor_recall,
        with_metadata=False,
        config=cfg,
    )
    meta = {}
    try:
        m = trafilatura.extract_metadata(html, default_url=url)
        if m is not None:
            meta = m.as_dict() if hasattr(m, "as_dict") else dict(m.__dict__)
    except Exception as e:  # noqa: BLE001
        log(f"metadata extraction warning: {e}")
    return md, meta


def confidence_score(markdown, html):
    """Heuristic 0..1 confidence: extracted text volume rewarded by structure.
    trafilatura exposes no public score, so we derive one for --debug."""
    if not markdown:
        return 0.0
    text = re.sub(r"[#>*`\[\]()_!-]", "", markdown)
    words = len(text.split())
    if words < 30:
        base = 0.35
    elif words < 120:
        base = 0.6
    elif words < 400:
        base = 0.8
    else:
        base = 0.9
    bonus = 0.0
    if re.search(r"^#{1,6} ", markdown, re.M):
        bonus += 0.04
    if "```" in markdown or re.search(r"^    \S", markdown, re.M):
        bonus += 0.03
    if re.search(r"^\|.*\|$", markdown, re.M):
        bonus += 0.03
    return round(min(0.99, base + bonus), 3)


def normalize_date(raw):
    if not raw:
        return ""
    raw = str(raw).strip()
    m = re.match(r"(\d{4}-\d{2}-\d{2})", raw)
    return m.group(1) if m else raw


def _code_signature(text):
    """Whitespace-insensitive signature used to match a markdown fence body to
    its DOM source block, robust to reflow and trafilatura's pruning."""
    return re.sub(r"\s+", "", text)[:120]


def merge_code_languages(markdown, code_blocks):
    """trafilatura emits bare ``` fences; re-attach a language to each by
    matching the fenced body's content signature to a DOM-extracted block.

    Content matching (not positional) keeps this robust when trafilatura emits a
    different number of blocks than the DOM (it prunes command-line / boilerplate
    blocks), and when block order differs."""
    langed = [b for b in code_blocks if b.get("language")]
    if not langed:
        return markdown
    sigs = [(_code_signature(b["code"]), b["language"]) for b in langed]

    # Walk fence pairs, replacing each opening fence with one carrying a language.
    fence_re = re.compile(r"^(```)[ \t]*$", re.M)
    fences = list(fence_re.finditer(markdown))
    if len(fences) < 2:
        return markdown
    out = []
    pos = 0
    i = 0
    while i + 1 < len(fences):
        op, cl = fences[i], fences[i + 1]
        body = markdown[op.end():cl.start()]
        out.append(markdown[pos:op.start()])
        lang = _lang_for_body(body, sigs)
        out.append("```" + lang)
        pos = op.end()
        i += 2
    out.append(markdown[pos:])
    return "".join(out)


def _lang_for_body(body, sigs):
    """Best language for a fenced body via prefix-overlap of content signatures."""
    bsig = _code_signature(body)
    if not bsig:
        return ""
    for sig, lang in sigs:
        if not sig:
            continue
        # A solid overlap in either direction is a confident match.
        if bsig.startswith(sig[:60]) or sig.startswith(bsig[:60]):
            return lang
    return ""


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("url")
    ap.add_argument("--html-file", default="", help="read HTML from file instead of fetching")
    ap.add_argument("--extract", default="article",
                    choices=["article", "full", "links", "tables", "code"])
    ap.add_argument("--css", default="", help="CSS selector override")
    ap.add_argument("--timeout", type=int, default=30)
    args = ap.parse_args()

    url = args.url

    if args.html_file:
        with open(args.html_file, "r", encoding="utf-8", errors="replace") as f:
            html = f.read()
    else:
        html = fetch_html(url, args.timeout)
    if not html or len(html.strip()) < 50:
        print(json.dumps({"skipped": True, "reason": "empty or unreachable page"}))
        sys.exit(0)

    soup = soup_of(html)

    if args.extract == "links":
        links = extract_links(soup, url)
        meta = run_trafilatura(html, url, False)[1]
        print(json.dumps(_payload(url, "", meta, 0.99, "links", links=links)))
        return
    if args.extract == "tables":
        tables = extract_tables(soup)
        md = "\n\n".join(tables)
        meta = run_trafilatura(html, url, False)[1]
        print(json.dumps(_payload(url, md, meta, 0.95 if tables else 0.0,
                                  "tables", tables=tables)))
        return
    if args.extract == "code":
        blocks = extract_code_blocks(soup)
        md = "\n\n".join(
            "```" + b["language"] + "\n" + b["code"] + "\n```" for b in blocks)
        meta = run_trafilatura(html, url, False)[1]
        print(json.dumps(_payload(url, md, meta, 0.95 if blocks else 0.0,
                                  "code", code_blocks=blocks)))
        return

    if args.css:
        md = extract_with_css(soup, args.css, url)
        if md is None:
            print(json.dumps({"skipped": True,
                              "reason": f"no nodes matched selector: {args.css}"}))
            sys.exit(0)
        meta = run_trafilatura(html, url, False)[1]
        code_blocks = extract_code_blocks(soup)
        md, images = collect_images(md, soup, url)
        md = clean_markdown(md)
        conf = confidence_score(md, html)
        print(json.dumps(_payload(url, md, meta, conf,
                                  "css-selector", images=images,
                                  code_blocks=code_blocks)))
        return

    favor_recall = (args.extract == "full")
    md, meta = run_trafilatura(html, url, favor_recall)
    method = "trafilatura"

    if not md:
        from markdownify import markdownify as mdify
        body = soup.find("body") or soup
        md = clean_markdown(mdify(str(body), heading_style="ATX", bullets="-"))
        method = "markdownify-body"

    if not md or not md.strip():
        print(json.dumps({"skipped": True, "reason": "no extractable content"}))
        sys.exit(0)

    code_blocks = extract_code_blocks(soup)
    md = merge_code_languages(md, code_blocks)
    md, images = collect_images(md, soup, url)
    md = clean_markdown(md)
    conf = confidence_score(md, html)

    print(json.dumps(_payload(url, md, meta, conf, method,
                              images=images, code_blocks=code_blocks)))


def _payload(url, md, meta, confidence, method, images=None, links=None,
             tables=None, code_blocks=None):
    title = (meta.get("title") if meta else "") or ""
    author = (meta.get("author") if meta else "") or ""
    date = normalize_date(meta.get("date") if meta else "")
    sitename = (meta.get("sitename") if meta else "") or ""
    description = (meta.get("description") if meta else "") or ""
    text = re.sub(r"[#>*`\[\]()_!|-]", "", md)
    payload = {
        "title": title,
        "author": author,
        "date": date,
        "url": (meta.get("url") if meta else "") or url,
        "sitename": sitename,
        "description": description,
        "markdown": md,
        "confidence": confidence,
        "extraction_method": method,
        "word_count": len(text.split()),
        "images": images or [],
    }
    if links is not None:
        payload["links"] = links
    if tables is not None:
        payload["tables"] = tables
    if code_blocks is not None:
        payload["code_blocks"] = code_blocks
    return payload


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
