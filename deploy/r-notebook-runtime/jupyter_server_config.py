import os

c = get_config()  # noqa: F821

base_url = os.environ.get("TMA_NOTEBOOK_BASE_URL", "/").strip() or "/"
if not base_url.startswith("/"):
    base_url = f"/{base_url}"
if not base_url.endswith("/"):
    base_url = f"{base_url}/"

c.ServerApp.ip = "0.0.0.0"
c.ServerApp.port = 8888
c.ServerApp.open_browser = False
c.ServerApp.root_dir = "/workspace"
c.ServerApp.base_url = base_url
c.ServerApp.allow_remote_access = True
c.ServerApp.trust_xheaders = True
c.ServerApp.allow_origin = os.environ.get("TMA_NOTEBOOK_ALLOW_ORIGIN", "")
c.ServerApp.disable_check_xsrf = False
c.IdentityProvider.token = os.environ.get("TMA_NOTEBOOK_TOKEN", "")
c.PasswordIdentityProvider.hashed_password = ""
c.LabApp.default_url = "/lab"
