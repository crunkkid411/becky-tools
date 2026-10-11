#!/usr/bin/env python3
"""becky-revsearch helper: reverse image search on Yandex and Bing.

Uploads ONE picture to each engine in Camoufox (the agent browser's stealth
Firefox, X:\\agent-browser - see the agent-firefox skill; no login, no account),
waits for the results, and saves a picture of every result thumbnail with the
page it links to and the text around it. It does NOT judge who anyone is: the Go
caller runs both face models over every saved thumbnail and keeps only the ones
that show the same face.

Google is not used: it answers automated uploads with its "unusual traffic"
block page (tested 2026-10-10). Yandex and Bing both work headless.

usage: reverse_search.py <image> --out <dir> [--engines yandex,bing] [--max 40] [--show]
stdout: one JSON line {"engines": [...], "results": [...]}; never a stack trace.
"""
import argparse
import json
import os
import random
import sys
import time

START = {
    "yandex": "https://yandex.com/images/",
    "bing": "https://www.bing.com/visualsearch",
}

# Every visible picture on the page (img tags and background images), with the
# link around it and the nearest readable text (a page title, a caption, a name).
COLLECT = """(minSide) => {
  const out = [];
  document.querySelectorAll('*').forEach((el, i) => {
    const r = el.getBoundingClientRect();
    if (r.width < minSide || r.height < minSide || el.hasAttribute('data-becky')) return;
    const isImg = el.tagName === 'IMG' || (getComputedStyle(el).backgroundImage || '').startsWith('url(');
    if (!isImg || el.closest('form, header, [role=search]')) return;   // not our own query thumbnail
    if (/CbirPreview|Smart|logo/i.test(el.className || '')) return;
    const a = el.closest('a');
    let box = el.parentElement, text = '';
    for (let k = 0; k < 5 && box; k++, box = box.parentElement) {
      text = (box.innerText || '').trim();
      if (text.length > 12) break;
    }
    const id = 'b' + Math.random().toString(36).slice(2, 9);
    el.setAttribute('data-becky', id);
    out.push({id, page: a ? a.href : '', alt: el.alt || '', text: text.slice(0, 300)});
  });
  return out;
}"""


def grab(page, engine, tag, out, cap, results):
    for _ in range(3):  # pull in lazy-loaded results
        page.mouse.wheel(0, random.randint(1200, 1600))
        time.sleep(random.uniform(1.5, 2.5))
    page.evaluate("window.scrollTo(0, 0)")
    time.sleep(1)
    seen, kept = set(), 0
    for f in page.evaluate(COLLECT, 60):
        key = (f["page"], f["text"]) if f["page"] else f["id"]  # a picture and its frame are one result
        if key in seen or kept >= cap:
            continue
        seen.add(key)
        kept += 1
        path = os.path.join(out, f"{engine}_{tag}_{len(results):03d}.png")
        try:
            page.locator(f"[data-becky='{f['id']}']").first.screenshot(path=path, timeout=5000)
        except Exception:  # noqa: BLE001 - a thumbnail that will not render is just skipped
            continue
        results.append({"engine": engine, "tab": tag, "image": path, "page": f["page"], "text": f["text"] or f["alt"]})


def blocked(page):
    body = (page.evaluate("document.body.innerText") or "").lower()
    return "captcha" in body or "unusual traffic" in body or "are you a robot" in body


def search(page, engine, image, out, cap):
    page.goto(START[engine], wait_until="domcontentloaded", timeout=60000)
    time.sleep(random.uniform(3, 5))
    if page.locator("input[type=file]").count() == 0:
        return {"engine": engine, "ok": False, "note": "no upload box on the page"}
    page.locator("input[type=file]").first.set_input_files(image)
    results = []
    if engine == "yandex":
        page.wait_for_url("**cbir_id**", timeout=45000)
        time.sleep(random.uniform(5, 7))
        if blocked(page):
            return {"engine": engine, "ok": False, "note": "the site asked for a CAPTCHA (bot check)"}
        # Sites: pages that hold this picture or one like it, with their titles.
        for tab, n in (("Sites", cap // 2), ("Similar images", cap)):
            try:
                page.get_by_text(tab, exact=True).first.click(timeout=8000)
                time.sleep(random.uniform(5, 7))
                grab(page, engine, tab.split()[0].lower(), out, n, results)
            except Exception:  # noqa: BLE001 - a missing tab just yields fewer results
                pass
    else:
        page.wait_for_url("**bing.com/**", timeout=45000)
        time.sleep(random.uniform(8, 10))
        if blocked(page):
            return {"engine": engine, "ok": False, "note": "the site asked for a CAPTCHA (bot check)"}
        for chip in ("Visual Matches", "Visual matches"):
            if page.get_by_text(chip, exact=True).count():
                page.get_by_text(chip, exact=True).first.click()
                time.sleep(random.uniform(5, 7))
                break
        grab(page, engine, "visual", out, cap, results)
    return {"engine": engine, "ok": True, "results": results, "url": page.url}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("image")
    ap.add_argument("--out", required=True)
    ap.add_argument("--engines", default="yandex,bing")
    ap.add_argument("--max", type=int, default=40, help="most thumbnails kept per results tab")
    ap.add_argument("--show", action="store_true", help="show the browser window (default hidden)")
    a = ap.parse_args()
    os.makedirs(a.out, exist_ok=True)
    from camoufox.sync_api import Camoufox

    engines, results = [], []
    with Camoufox(headless=not a.show, humanize=True) as browser:
        page = browser.new_page(viewport={"width": 1600, "height": 1000})
        for e in [x.strip() for x in a.engines.split(",") if x.strip() in START]:
            try:
                r = search(page, e, os.path.abspath(a.image), a.out, a.max)
            except Exception as ex:  # noqa: BLE001 - one engine failing must not lose the other
                r = {"engine": e, "ok": False, "note": f"{type(ex).__name__}: {ex}"[:200]}
            results += r.pop("results", [])
            r["count"] = sum(1 for x in results if x["engine"] == e)
            engines.append(r)
    print(json.dumps({"engines": engines, "results": results}))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
