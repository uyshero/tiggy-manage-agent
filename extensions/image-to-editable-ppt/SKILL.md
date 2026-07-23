---
name: image-to-editable-ppt
description: Reconstruct an uploaded slide image as a high-fidelity editable PowerPoint through the preinstalled editppt CLI in the TMA CPU cloud sandbox. Stop on runtime misconfiguration; never substitute a simplified python-pptx or PptxGenJS redraw.
---

# Image To Editable PPT For TMA

Use this Skill when the user asks to turn an uploaded slide image into an editable `.pptx`.

This is a TMA workflow. Do not invoke Codex CLI, Codex OAuth, Claude-specific tools, or host filesystem paths. Do not send model credentials into the sandbox. Use TMA `default.*` tools in the current `cloud_sandbox`; uploaded inputs are under `/workspace/uploads`, intermediate data belongs under `/mnt/data/image-to-editable-ppt`, and final deliverables must be copied under `/workspace` and published through `output_paths`.

## Non-Negotiable Runtime Gate

The first execution action must be one `default_run_command` call that runs:

```bash
tma-ppt-sandbox-doctor && command -v editppt && editppt --help >/dev/null
```

Continue only when this command succeeds and `command -v editppt` returns an executable path. If it fails or returns `command not found`:

- Stop the conversion immediately.
- Report that the Session is using the wrong cloud-sandbox image and requires `tma-image-to-editable-ppt-sandbox:local`.
- Do not create or publish any `.pptx`.
- Do not install packages dynamically.
- Do not invoke `python-pptx`, PptxGenJS, LibreOffice scripting, or a custom Python/JavaScript PPT builder as a substitute.

This gate is fail-closed. A simplified fallback deck is a failed task, not a partial success.

## Preconditions

1. Complete the Non-Negotiable Runtime Gate. Do not repeat or bypass it.
2. The current/default Agent model must support image input. Analyze the image already attached to the current user message; do not configure a second vision model.
3. An enabled image-generation Skill/tool must be available for complex visual assets. Use the exact interface in its own tool schema. Prompt-only generation is acceptable; reference-image editing may be used when the enabled tool actually supports it.
4. Use offline `builtin-ink` text hints by default. PaddleOCR is optional and must not be requested as a prerequisite.
5. If the source page or generated preview is only a sandbox file and no artifact-to-vision tool is available, do not pretend the current model saw it. Continue with deterministic validation, publish review images, and label the output as a draft.

## Fidelity Gate

The target is the same slide reconstructed into editable objects, not a new slide inspired by the source.

- Preserve the source canvas ratio, composition, hierarchy, reading order, object count, relative positions, alignment, spacing, colors, and z-order.
- Do not simplify an architecture diagram, merge nodes, omit connectors, replace named components with generic boxes, shorten labels, or rearrange the flow for readability.
- Before building, inventory every visible text block, node, connector, group, icon, illustration, image region, legend, badge, and background region with a stable ID and source-pixel bounding box.
- Every inventory item must map to exactly one manifest object or an explicitly documented grouped asset. Missing mappings block page completion.
- Prompt-only generated PNGs may approximate the internal pixels of complex visual assets, but their semantic identity, count, color role, bounding box, and placement must match the source.
- Never describe a visibly different or simplified page as successful because it is "semantic", "editable", or "structurally equivalent".

## Runtime Sources

The deterministic `editppt` CLI is installed in the sandbox. Its pinned upstream references are under:

```text
/opt/image-to-editable-ppt/skills/image-to-editable-ppt/references/
```

Before rebuilding a page, read these files in full with bounded commands:

```text
page-decision-tree.md
manifest-schema.md
cli-helper.md
```

Use their manifest, coordinate, assembly, and validation contracts. This TMA profile replaces their model-client and Codex orchestration instructions with the current Agent model plus the enabled image-generation Skill.

## TMA Workflow

1. Locate the exact uploaded input under `/workspace/uploads`. Never guess between multiple candidates.
2. Prepare a sequential run:

   ```bash
   editppt prepare <input> --out-root /mnt/data/image-to-editable-ppt --max-concurrent-pages 1 --image-backend builtin-imagegen
   ```

3. Read `editppt run next <run> --json`. Do not spawn page subagents in this first profile because child Session sandboxes do not share the parent page directory.
4. Build the upstream worker prompt, then claim a single page with `editppt run dispatch <run> --page <page_id> --agent-id main --prompt-file <page_dir>/worker-prompt.md --local`.
5. Use the current model's view of the attached source image to write a complete inventory: readable text, native structural objects, background, non-text foreground assets, approximate source-pixel boxes, grouping, colors, and z-order. Combine this with `text_hints.json`; `builtin-ink` provides geometry and the current model provides transcription and semantics.
6. Classify readable text, cards, panels, simple tables, lines, connectors, and ordinary geometry as native PowerPoint objects. Classify photos, complex illustrations, styled icons, maps, and complex visual regions as independent PNG assets.
7. For complex assets, write a detailed asset-sheet prompt from the visual inventory. Include object count/order, shapes, colors, proportions, style, and a flat chroma-key background with generous spacing. Call the enabled image-generation tool using its real schema. Do not call `editppt image generate/edit`, because the sandbox intentionally has no model credentials.
8. Materialize the generated result as a local PNG. If the image tool returns a URL, download it through an approved `default_run_command`; if it returns a Session artifact or local path, use that directly. Verify the file is a readable image.
9. Record and split the generated sheet:

   ```bash
   editppt image import <page_dir> \
     --job-id icon-sheet \
     --source-image <generated-local.png> \
     --dest assets/icon-sheet.png \
     --role asset_sheet \
     --prompt-file <asset-sheet-prompt> \
     --backend builtin-imagegen

   editppt image process-sheet <page_dir> \
     --job-id icon-sheet \
     --asset-sheet-source assets/icon-sheet.png \
     --assets-dir assets/icons \
     --square-assets
   ```

10. Reconcile every split PNG with the inventory. Regenerate a sheet when an object is missing, fused, clipped, visibly wrong, or contaminated by text/key color.
11. Build `manifest.json` as the authoritative source. Use source-image pixel coordinates for every `box_px`, `points_px`, and `polygon_px` field. Record generated asset provenance as `builtin-imagegen`. Verify that every inventory ID has a manifest mapping before building.
12. Run `editppt page build`, `editppt page contact-sheet`, and `editppt page validate` using the exact syntax in `cli-helper.md`.
13. If an Agent-level artifact vision tool exists, inspect `source.png` and `preview.png` together with the current/default model, then fix missing objects, duplicated text, overlap, clipping, z-order, color drift, and bad substitutions. Do not select or configure another model.
14. If artifact vision is unavailable, rely only on deterministic checks, publish the source/preview contact sheet for human review, and record `visual_qa: pending_user_review`. Do not claim visual equivalence.
15. Write `validation.json` and `page_result.json`, call `editppt run record`, then `editppt run finalize` after all pages are recorded.
16. Copy the final `.pptx` and review contact sheet to `/workspace`. Publish both exact paths through a successful `default_run_command` call with `output_paths`.

## Editability Contract

- Readable text is native PowerPoint text unless it is inseparable brand text inside a photo or logo.
- Cards, panels, simple tables, lines, axes, connectors, and ordinary geometric shapes are native PowerPoint objects.
- Photos, complex illustrations, styled icons, maps, and other complex visuals are separate transparent PNG picture objects. They are movable, resizable, croppable, replaceable, and reorderable, but their internal pixels are not editable.
- Prompt-only image generation may approximate complex raster assets, but it does not permit changing or simplifying the slide layout. State only the raster-asset limitation in the final result.
- Never deliver a full-slide source screenshot with editable text overlaid on top.

## Failure Rules

- An unavailable vision-capable current model blocks source understanding.
- A missing `tma-ppt-sandbox-doctor` or `editppt` command is a blocking runtime error. Never use a different PPT library as fallback.
- An unavailable image-generation tool blocks pages that contain complex non-text assets, but not pages composed only of native text and simple geometry.
- A simplified, rearranged, or generic redraw is a page failure even when all resulting objects are editable.
- A missing foreground object, invalid PPTX, absent manifest provenance, or full-slide screenshot fallback is a page failure.
- Minor antialiasing, shadow, font, or decorative drift may be recorded as a warning after the required object workflow succeeds.
- Keep intermediate files under `/mnt/data`; only final deliverables and explicit review artifacts belong under `/workspace`.
