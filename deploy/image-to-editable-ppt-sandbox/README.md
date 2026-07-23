# TMA Image-To-Editable-PPT Sandbox

This image extends TMA's `cloud_sandbox` runtime with the CPU-side tools needed by the editable-PPT workflow. It does not run Codex, does not run an AI model, and does not need a local GPU.

Included components:

- `image-to-editable-ppt-skill` `v0.3.2`, pinned to commit `4d5d935bf6d81929e28d316a3b90feadd5da527a`.
- The deterministic `editppt` CLI and its Python runtime dependencies.
- LibreOffice Impress and Poppler utilities.
- Noto CJK, Noto Core, Liberation, and DejaVu fonts.
- `builtin-ink`, the offline text-geometry detector used by `editppt`.
- `tma-ppt-sandbox-doctor`, a local health check with no model or OCR API calls.

## Responsibility Boundary

The conversion has two execution layers:

| Layer | Responsibility |
| --- | --- |
| TMA Agent | Understand the uploaded source with the Agent's configured vision-capable model; call an enabled image-generation Skill for complex visual assets. |
| Docker sandbox | Prepare pages, measure text geometry, split/clean generated PNG assets, build PPTX files, render previews, and run deterministic validation. |

The Skill fails closed when this image is not active. It must not replace a missing `editppt` CLI with a direct `python-pptx`, PptxGenJS, or custom presentation builder.

Model selection and credentials stay in TMA's model/provider and Skill governance. The sandbox does not read `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `IMAGE_TO_EDITABLE_PPT_IMAGE_MODEL`, or `TMA_PPT_VISION_MODEL`. `PADDLE_OCR_TOKEN` is also not required: the first version uses offline `builtin-ink` for text boxes and the Agent's vision model for text transcription.

The installed OpenAI Python package is an upstream `editppt` CLI import dependency. Its presence does not mean the sandbox is configured to call OpenAI.

## Build

Run from the repository root:

```bash
docker build \
  -f deploy/image-to-editable-ppt-sandbox/Dockerfile \
  -t tma-image-to-editable-ppt-sandbox:local \
  .
```

The build defaults to the Tsinghua PyPI mirror. Production CI can point it at an internal package repository or official PyPI:

```bash
docker build \
  --build-arg PYPI_INDEX_URL=https://pypi.org/simple \
  -f deploy/image-to-editable-ppt-sandbox/Dockerfile \
  -t tma-image-to-editable-ppt-sandbox:local \
  .
```

Verify the image without credentials:

```bash
docker run --rm \
  tma-image-to-editable-ppt-sandbox:local \
  tma-ppt-sandbox-doctor
```

## TMA Configuration

Register a dedicated Environment after building the image:

```bash
bin/tma env create \
  --name "PPT Sandbox" \
  --config '{"runtime_settings":{"tool_runtime":"cloud_sandbox","cloud_sandbox_image":"tma-image-to-editable-ppt-sandbox:local","cloud_sandbox_allow_network":true}}'
```

Use `bin/tma env list` to obtain its ID. Select `PPT Sandbox` when creating the Agent in Workbench, or pass the ID to `bin/tma agent create --env <environment-id> ...`. Sessions created for that Agent inherit the Environment automatically and cannot replace it with another Environment.

Runtime network access is needed only when the enabled image-generation tool returns a temporary URL that must be downloaded into the Session workspace. It can be disabled when that tool directly materializes a Session artifact or local file.

Create and enable a TMA Skill version using `extensions/image-to-editable-ppt/SKILL.md`. Bind it to an Agent with:

- A default/current model whose capability is `text_image`.
- The existing image-generation Skill/tool enabled. Prompt-only generation is sufficient for semantic asset reconstruction; reference-image editing gives higher fidelity when available.
- `default_run_command` and filesystem tools in the `cloud_sandbox` runtime.
- Network approval only when downloading an image-generation result URL.

No model or OCR environment variables are attached to the sandbox Environment.

Uploaded files are synchronized by TMA to `/workspace/uploads`. Intermediate state belongs under `/mnt/data/image-to-editable-ppt`. Final `.pptx` files and review images are copied under `/workspace` and published through `output_paths`, which records them as Session artifacts.

Installing or enabling the Skill does not select this Docker image. The Agent's bound Environment owns that selection. Confirm the first conversion command runs `tma-ppt-sandbox-doctor`; a missing `editppt` command means the Agent is bound to the wrong Environment.

## Manual Compose Check

The Compose file is for inspecting the sandbox independently from TMA:

```bash
docker compose \
  -f deploy/image-to-editable-ppt-sandbox/compose.yaml \
  up -d --build

docker compose \
  -f deploy/image-to-editable-ppt-sandbox/compose.yaml \
  exec sandbox tma-ppt-sandbox-doctor
```

Do not expose this container as a public service. TMA starts a session-scoped container with CPU, memory, PID, workspace, data, and approval boundaries.

## Current Boundary

The first runnable profile is strongest for a slide image attached to the current user message:

- The current model can inspect that attachment directly.
- `builtin-ink` supplies text geometry without OCR credentials.
- Prompt-only image generation rebuilds complex objects as independent PNGs. Their internal pixels may be approximate, but the slide composition, object count, coordinates, hierarchy, and connectors must not be simplified or rearranged.
- `editppt` performs deterministic assembly and validation.

Two cases still need a TMA artifact-to-vision bridge for fully automatic visual QA: pages rendered from an uploaded PDF/PPTX, and generated `preview.png` files. Today TMA sends only current-message image attachments to the configured vision model. Until that bridge exists, publish `source.png`, `preview.png`, or the contact sheet as review artifacts and label the result as a draft rather than claiming model-verified visual fidelity.
