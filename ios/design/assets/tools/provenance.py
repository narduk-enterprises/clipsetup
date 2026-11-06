"""Which image model drew a kept render, shared by the kit gate and the icon-pack gate (pure Python).

Every asset and icon records `backend`, `tool` and the exact `prompt`. The backends, and the one tool each draws with:

    grok    Grok Imagine through the grok CLI: image_gen, or image_edit (which also needs `from`)
    sol     Codex gpt-6.1-sol with the built-in tool: image_generation or native image_gen
    astra   Codex gpt-6-astra with the same tools: image_generation or native image_gen
    cursor  Cursor's GenerateImage tool, the fallback when grok cannot run: cursor_generate_image

`model` is the Codex model id for sol and astra. Manifests written before backends existed carry only `tool`; the
backend is read from it (image_gen and image_edit mean grok, cursor_generate_image means cursor).
"""
from __future__ import annotations

BACKENDS = {
    'grok': ('image_gen', 'image_edit'),
    'sol': ('image_generation', 'image_gen'),
    'astra': ('image_generation', 'image_gen'),
    'cursor': ('cursor_generate_image',),
}
MODELS = {'sol': 'gpt-6.1-sol', 'astra': 'gpt-6-astra'}  # default model ids; `kit.py keep --model` overrides
TOOLS = tuple(dict.fromkeys(t for tools in BACKENDS.values() for t in tools))
DEFAULT_TOOL = {'grok': 'image_gen', 'sol': 'image_generation', 'astra': 'image_generation',
                'cursor': 'cursor_generate_image'}


def backend_of(entry: dict) -> str | None:
    """The recorded backend, or the one a legacy entry's tool implies."""
    if entry.get('backend'):
        return entry['backend']
    return {'image_gen': 'grok', 'image_edit': 'grok', 'cursor_generate_image': 'cursor'}.get(entry.get('tool'))


def problems(entry: dict) -> list[str]:
    """What is wrong with one asset's or icon's drawing record; empty when it is complete."""
    out = []
    if not str(entry.get('prompt', '')).strip():
        out.append('missing prompt (the exact prompt of the kept render)')
    backend, tool = backend_of(entry), entry.get('tool')
    if backend not in BACKENDS:
        out.append(f"backend must be one of {', '.join(BACKENDS)}")
    elif tool not in BACKENDS[backend]:
        out.append(f"tool must be {' or '.join(BACKENDS[backend])} for backend {backend}")
    if backend not in BACKENDS and tool not in TOOLS:
        out.append(f"tool must be one of {', '.join(TOOLS)}")
    if backend in MODELS and not str(entry.get('model', '')).strip():
        out.append(f'backend {backend} needs `model` (the Codex model id that drew it)')
    if tool == 'image_edit' and not entry.get('from'):
        out.append('an image_edit needs `from`, the render it edited')
    return out


def drawing_entry(a_backend: str, tool: str | None, model: str | None, prompt: str, round_: int,
                  from_: str | None) -> dict:
    """The provenance fields of one kept render."""
    entry = {'backend': a_backend, 'tool': tool or DEFAULT_TOOL[a_backend]}
    if a_backend in MODELS:
        entry['model'] = model or MODELS[a_backend]
    elif model:
        entry['model'] = model
    entry.update(prompt=prompt, round=round_)
    if from_:
        entry['from'] = from_
    return entry
