#!/usr/bin/env python3
"""Draw with Grok Imagine through a direct, image-tools-only grok run: one asset per run, N renders, many in parallel.

    grok_draw.py --out DIR --name NAME (--prompt TEXT | --prompt-file FILE) [--count 2] [--aspect 1:1]
                 [--edit SOURCE_IMAGE]
    grok_draw.py --out DIR --batch jobs.json [--jobs 12]          jobs.json: [{"name", "prompt"|"prompt_file",
                                                                   "count"?, "aspect"?, "edit"?}, ...]

This is the default Grok route until agent-infrastructure#2391 (grok-lane ends after one narration line, no
image_gen call) is fixed; SKILL.md says why it is acceptable. Each job:
  1. makes a fresh EMPTY scratch directory and, for an edit, copies the source image into it as source.<ext>;
  2. runs `grok --prompt-file ... --permission-mode dontAsk --tools image_gen|image_edit --disable-web-search
     --no-subagents --cwd <scratch> --output-format plain`: with --tools limited to the image tool the model cannot
     run a shell or edit a file, so there is nothing to sandbox;
  3. collects the renders from ~/.grok/sessions/<url-encoded cwd>/<session>/images/<n>.jpg (the plain output often
     omits the path) into DIR/<name>-<k>.jpg, and writes DIR/<name>.prompt.txt (the exact prompt, not the wrapper
     this script adds) and DIR/<name>.json.
Run it outside the Claude Code Bash sandbox. Look at every render, then `kit.py keep` the one worth keeping with
`--prompt-file DIR/<name>.prompt.txt`. Nothing here draws or edits pixels. Exit 1 if any job made no render.

--via ai-api draws the same Grok Imagine through ai-api's POST /v1/images/generations (the Grok seat on the lane
guests; no xAI key, no spend) for a session with no signed-in `grok`: the x740 Claude lanes, Codex, cloud. --via auto
(the default) takes local `grok` when it is on PATH, else ai-api. ai-api cannot edit (image and mask are a 400) and
knows five aspects (1:1, 3:2, 2:3, 16:9, 9:16), takes at most 4 requests in flight per key and 4 renders a request.
Run it under the design key: `nvault run -p ai-api -e prd -c app-key-design-agents -- python3 grok_draw.py --via
ai-api ...` (AI_API_KEY; AI_API_BASE overrides the URL).
"""
from __future__ import annotations

import argparse
import base64
import http.client
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from concurrent.futures import ThreadPoolExecutor

GROK_HOME = pathlib.Path.home() / '.grok'
TOOLS = {'gen': 'image_gen', 'edit': 'image_edit'}
AI_API_BASE = 'https://ai-api.nardukenterprises.com'
AI_API_SIZES = {'1:1': '1024x1024', '3:2': '1536x1024', '2:3': '1024x1536', '16:9': '1792x1024', '9:16': '1024x1792'}
AI_API_MAX_N = 4
AI_API_IN_FLIGHT = 4


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


# The ai-api key goes only through this opener: no redirect, so it never reaches a host this script did not name,
# and no ambient proxy (agent-infrastructure#1941). A copy of this file runs outside the checkout, so it carries
# its own opener rather than loading scripts/shared_transport.py.
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())


def wrap(prompt: str, count: int, aspect: str, edit: str | None) -> str:
    """The run's instruction: call the one image tool `count` times with the prompt verbatim, and nothing else."""
    if edit:
        call = (f'Use your image_edit tool to produce {count} saved render(s); a call that saves nothing may be retried twice with backoff. Each call: image = ["./{edit}"], prompt = the text '
                'between the markers verbatim.')
    else:
        call = (f'Use your image_gen tool to produce {count} saved render(s); a call that saves nothing may be retried twice with backoff. Each call: aspect_ratio = "{aspect}", prompt = the '
                'text between the markers verbatim.')
    return (f'{call} Make the calls one after another, then reply with the single word "done". Call no other tool, '
            f'write no code and ask no questions.\n\n<<<PROMPT\n{prompt}\nPROMPT>>>\n')


def renders(cwd: pathlib.Path) -> list[pathlib.Path]:
    """Images of every session grok opened in this scratch directory, oldest first."""
    found: list[pathlib.Path] = []
    for c in dict.fromkeys([cwd, cwd.resolve()]):  # grok may record the symlinked or the real path (/var vs /private/var)
        found += (GROK_HOME / 'sessions' / urllib.parse.quote(str(c), safe='')).glob('*/images/*')
    key = lambda p: (p.parent.parent.stat().st_mtime, int(p.stem) if p.stem.isdigit() else 0, p.name)  # noqa: E731
    return sorted({p.resolve(): p for p in found}.values(), key=key)


def run_job(job: dict, out: pathlib.Path, timeout: int, keep_scratch: bool) -> dict:
    name = job['name']
    prompt = (pathlib.Path(job['prompt_file']).read_text() if job.get('prompt_file') else job.get('prompt', '')).strip()
    edit = job.get('edit')
    res = {'name': name, 'prompt': prompt, 'tool': 'image_edit' if edit else 'image_gen',
           'count': int(job.get('count', 2)), 'aspect': job.get('aspect', '1:1'), 'renders': [], 'error': None}
    if not prompt:
        res['error'] = 'empty prompt'
        return res
    if edit and not pathlib.Path(edit).is_file():
        res['error'] = f'edit source not found: {edit}'
        return res
    scratch = pathlib.Path(tempfile.mkdtemp(prefix=f'grok-draw-{name}-')).resolve()
    try:
        src_name = None
        if edit:
            src_name = 'source' + pathlib.Path(edit).suffix
            shutil.copy(edit, scratch / src_name)
            res['from_file'] = str(edit)
        pf = scratch.parent / f'{scratch.name}.prompt.txt'  # outside the cwd, so the cwd stays empty but for the source
        pf.write_text(wrap(prompt, res['count'], res['aspect'], src_name))
        cmd = ['grok', '--prompt-file', str(pf), '--permission-mode', 'dontAsk', '--tools', res['tool'],
               '--disable-web-search', '--no-subagents', '--cwd', str(scratch), '--output-format', 'plain']
        t0 = time.time()
        try:
            r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, cwd=scratch)
            res['exit'], res['seconds'] = r.returncode, round(time.time() - t0)
            res['stdout_tail'] = r.stdout.strip()[-400:]
            res['stderr_tail'] = r.stderr.strip()[-400:]
        except subprocess.TimeoutExpired:
            res['error'] = f'timed out after {timeout}s'
        finally:
            pf.unlink(missing_ok=True)
        found = renders(scratch)
        for k, p in enumerate(found, 1):
            dest = out / f'{name}-{k}{p.suffix}'
            shutil.copy2(p, dest)
            res['renders'].append(str(dest))
        if not found and not res['error']:
            blob = (res.get('stdout_tail', '') + res.get('stderr_tail', '')).lower()
            res['error'] = ('not signed in: run `grok` once and sign in (never read ~/.grok/auth.json)'
                            if 'not authenticated' in blob else
                            'no image_gen/image_edit call made (the #2391 failure shape): no renders under '
                            f'~/.grok/sessions/{urllib.parse.quote(str(scratch), safe="")}')
        elif len(found) != res['count']:
            res['note'] = f"asked for {res['count']} renders, found {len(found)}"
    finally:
        if not keep_scratch:
            shutil.rmtree(scratch, ignore_errors=True)
    (out / f'{name}.prompt.txt').write_text(prompt + '\n')
    (out / f'{name}.json').write_text(json.dumps(res, indent=2) + '\n')
    return res


def ai_api_request(job: dict, key: str, base: str, timeout: int) -> dict:
    """One /v1/images/generations call; the OpenAI response, or an `error` string with the gateway's message."""
    if not base.startswith('https://'):
        return {'error': f'AI_API_BASE must be https (the key goes to it): {base}'}
    body = json.dumps({'model': 'grok-imagine', 'prompt': job['prompt'], 'n': job['n'],
                       'size': AI_API_SIZES[job['aspect']], 'response_format': 'b64_json'}).encode()
    req = urllib.request.Request(f'{base.rstrip("/")}/v1/images/generations', data=body, method='POST', headers={
        'Authorization': f'Bearer {key}', 'content-type': 'application/json', 'user-agent': 'narduk-grok-draw/1'})
    try:
        with OPENER.open(req, timeout=timeout) as r:
            return json.loads(r.read())
    except urllib.error.HTTPError as e:
        try:
            msg = json.loads(e.read()).get('error', {}).get('message', '')
        except (ValueError, AttributeError):
            msg = ''
        after = e.headers.get('retry-after') if e.headers else None
        return {'error': f'ai-api {e.code}: {msg or e.reason}' + (f' (retry after {after}s)' if after else '')}
    except (urllib.error.URLError, TimeoutError) as e:
        return {'error': f'ai-api unreachable: {getattr(e, "reason", e)}'}
    except (OSError, ValueError, http.client.HTTPException) as e:
        return {'error': f'ai-api bad response: {type(e).__name__}'}


def run_job_ai_api(job: dict, out: pathlib.Path, timeout: int, key: str, base: str) -> dict:
    name = job['name']
    prompt = (pathlib.Path(job['prompt_file']).read_text() if job.get('prompt_file') else job.get('prompt', '')).strip()
    res = {'name': name, 'prompt': prompt, 'tool': 'image_gen', 'via': 'ai-api', 'count': int(job.get('count', 2)),
           'aspect': job.get('aspect', '1:1'), 'renders': [], 'error': None}
    if not prompt:
        res['error'] = 'empty prompt'
    elif job.get('edit'):
        res['error'] = 'ai-api has no edit route (image and mask are a 400): run this job with local grok (--via grok)'
    elif res['aspect'] not in AI_API_SIZES:
        res['error'] = f"ai-api has no {res['aspect']} size (it knows {', '.join(AI_API_SIZES)}): use --via grok"
    if res['error']:
        return res
    t0 = time.time()
    left = res['count']
    while left > 0 and not res['error']:
        n = min(left, AI_API_MAX_N)
        r = ai_api_request({'prompt': prompt, 'n': n, 'aspect': res['aspect']}, key, base, timeout)
        if r.get('error'):
            res['error'] = r['error']
            break
        for item in r.get('data', []):
            if not item.get('b64_json'):
                continue
            dest = out / f"{name}-{len(res['renders']) + 1}.jpg"
            try:
                dest.write_bytes(base64.b64decode(item['b64_json']))
            except ValueError:
                res['error'] = 'ai-api sent an image that is not base64'
                break
            res['renders'].append(str(dest))
        left -= n
    res['seconds'] = round(time.time() - t0)
    if not res['renders'] and not res['error']:
        res['error'] = 'ai-api answered with no image'
    elif res['renders'] and len(res['renders']) != res['count']:
        res['note'] = f"asked for {res['count']} renders, found {len(res['renders'])}"
    (out / f'{name}.prompt.txt').write_text(prompt + '\n')
    (out / f'{name}.json').write_text(json.dumps(res, indent=2) + '\n')
    return res


def main(argv=None) -> None:
    ap = argparse.ArgumentParser(description='Draw with Grok Imagine: one direct image-tools-only run per asset.')
    ap.add_argument('--out', type=pathlib.Path, required=True, help='directory for the renders (created)')
    ap.add_argument('--name', help='asset name (kebab-case); renders are DIR/<name>-<k>.jpg')
    ap.add_argument('--prompt')
    ap.add_argument('--prompt-file', type=pathlib.Path)
    ap.add_argument('--count', type=int, default=2, help='renders per asset (default 2)')
    ap.add_argument('--aspect', default='1:1', help='image_gen aspect_ratio (default 1:1)')
    ap.add_argument('--edit', type=pathlib.Path, help='image_edit this source image instead of image_gen')
    ap.add_argument('--batch', type=pathlib.Path, help='JSON list of jobs; relative paths resolve from the CWD')
    ap.add_argument('--jobs', type=int, default=12, help='parallel runs (12 worked; default 12)')
    ap.add_argument('--timeout', type=int, default=900, help='seconds per run (default 900)')
    ap.add_argument('--keep-scratch', action='store_true')
    ap.add_argument('--via', choices=('auto', 'grok', 'ai-api'), default='auto',
                    help='local grok CLI, or ai-api (auto: grok when on PATH, else ai-api)')
    a = ap.parse_args(argv)
    via = a.via if a.via != 'auto' else ('grok' if shutil.which('grok') else 'ai-api')
    key = os.environ.get('AI_API_KEY', '')
    if via == 'grok' and not shutil.which('grok'):
        sys.exit('grok not found on PATH (use --via ai-api)')
    if via == 'ai-api' and not key:
        sys.exit('ai-api needs AI_API_KEY: nvault run -p ai-api -e prd -c app-key-design-agents -- python3 '
                 'grok_draw.py --via ai-api ...')
    if a.batch:
        jobs = json.loads(a.batch.read_text())
    elif a.name and (a.prompt or a.prompt_file):
        jobs = [{'name': a.name, 'prompt': a.prompt, 'prompt_file': a.prompt_file, 'count': a.count,
                 'aspect': a.aspect, 'edit': a.edit}]
    else:
        sys.exit('give --batch, or --name with --prompt/--prompt-file')
    for j in jobs:
        j.setdefault('count', a.count)
        j.setdefault('aspect', a.aspect)
    a.out.mkdir(parents=True, exist_ok=True)
    if via == 'ai-api':
        base = os.environ.get('AI_API_BASE', AI_API_BASE)
        width = min(a.jobs, AI_API_IN_FLIGHT)
        job_fn = lambda j: run_job_ai_api(j, a.out, min(a.timeout, 330), key, base)  # noqa: E731
    else:
        width = a.jobs
        job_fn = lambda j: run_job(j, a.out, a.timeout, a.keep_scratch)  # noqa: E731
    with ThreadPoolExecutor(max(1, min(width, len(jobs)))) as ex:
        results = list(ex.map(job_fn, jobs))
    bad = 0
    for r in results:
        status = 'FAIL ' + r['error'] if r['error'] else f"{len(r['renders'])} render(s) in {r.get('seconds', '?')}s"
        print(f"{r['name']}: {status}" + (f" ({r['note']})" if r.get('note') else ''))
        for p in r['renders']:
            print(f'  {p}')
        bad += bool(r['error'])
    sys.exit(1 if bad else 0)


if __name__ == '__main__':
    main()
