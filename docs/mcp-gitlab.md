# GitLab Docker MCP

TMA 可以通过 Docker stdio MCP 连接 GitLab.com 或 GitLab Self-Managed。该集成使用固定镜像摘要，Token 只从 Server 环境解析，不进入数据库、Workbench 配置或日志。

## 默认能力

Workbench 的 **设置 > MCP > GitLab Docker** 模板默认只暴露：

- `search_repositories`：搜索有权访问的项目。
- `get_file_contents`：读取仓库目录或文件。

上游 `mcp/gitlab` 还提供创建仓库、分支、Issue、Merge Request 和写文件等工具。模板不会暴露这些写操作。由于上游 v0.5.1 没有为只读工具提供 MCP annotations，TMA 会保守地将这两个工具标记为外部写入风险并要求审批。

## 配置

1. 在 GitLab 创建 Personal Access Token。只读模板建议仅授予 `read_api` 和 `read_repository`。
2. 在 TMA Server 的 `.env` 或进程环境中设置 Token：

   ```bash
   TMA_GITLAB_PERSONAL_ACCESS_TOKEN=glpat-example
   ```

3. 重启 TMA Server，使新环境变量进入 Server 进程。
4. 打开 **设置 > MCP**，点击 **GitLab Docker**，检查 JSON 后保存。
5. 打开 **设置 > Agent**，把新建的 `gitlab` Registry 服务绑定到目标 Agent，并保存新的 Agent 配置版本。

模板默认连接 `https://gitlab.com/api/v4`。对于自托管 GitLab，保存前将 `GITLAB_API_URL` 改为实例 API 地址，例如 `https://gitlab.example.com/api/v4`。

固定镜像为：

```text
mcp/gitlab@sha256:a1b8571a210a3c8b17b288498d287cd1c3512c10519330ea71ca48e559e78917
```

TMA MCP Host 会按 Session 启动并复用临时容器，空闲回收时容器通过 `--rm` 删除，因此不需要在 `docker-compose.yml` 中运行常驻 GitLab 服务。

## 运行边界

- TMA Server 必须能执行 `docker`。本机运行 Server 时使用本机 Docker；容器化 Server 需要 `server-docker` target 和受控的 Docker socket。
- 挂载 Docker socket 等价于授予宿主机高权限，只应在可信运维边界内使用。
- Token 权限决定最终 GitLab API 权限；工具白名单不能替代最小权限 Token。
- GitLab 官方远程 MCP 使用 OAuth 动态客户端注册，而 TMA 当前不支持浏览器授权码流程，因此此处使用 PAT + Docker stdio。
- 来自仓库、Issue 或 Merge Request 的内容可能包含提示注入。Agent 不应因为远程内容而扩大工具权限或泄露凭据。

## 验证

协议兼容测试不访问 GitLab API，也不需要真实 Token。它会拉取固定摘要镜像，验证 MCP 初始化、JSON Lines framing、工具发现、只读白名单和审批策略：

```bash
make verify-mcp-gitlab-docker
```

配置真实 Token 后，可在 **设置 > MCP** 对注册服务执行“测试”，再让绑定 Agent 搜索一个项目或读取 README，完成真实 API 验收。
