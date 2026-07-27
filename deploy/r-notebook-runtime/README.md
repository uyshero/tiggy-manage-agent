# TMA R Notebook Runtime

This image is the development runtime for the R Survival Workbench extension. It
contains JupyterLab, IRkernel, Git integration, `renv`, Chinese data-cleaning
packages, and survival-analysis packages.

## Local development

```bash
cp deploy/r-notebook-runtime/.env.example deploy/r-notebook-runtime/.env
docker compose -f deploy/r-notebook-runtime/compose.yaml up --build -d
```

Open `http://127.0.0.1:18888/lab?token=<TMA_NOTEBOOK_TOKEN>`, or save that URL
in the extension project's Notebook URL setting.

The Compose service is for local development. Production must place Jupyter
behind an authenticated same-origin TMA HTTP/WebSocket proxy, issue a unique
token per project session, and mount a workspace scoped to organization,
workspace, owner, and project. Never expose a tokenless Jupyter server.

The GitLab connector should initialize repositories from
`examples/r-analysis-project`, while raw or identifiable data stays in the TMA
object store or another governed data source.
